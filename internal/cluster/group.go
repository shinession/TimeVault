package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/db"
	"timevault/internal/service"
)

const (
	// peerConns is the size of the read pool that is opened for each other server: a page reads one thing at a time.
	peerConns = 3
	// peerStatementTimeout ends a read of another server's schema that takes too long, so a slow server cannot hold the
	// pages of this one.
	peerStatementTimeout = 15 * time.Second
)

// State is how a server of the register looks to this one.
type State string

const (
	// StateOnline: it reported a short while ago.
	StateOnline State = "online"
	// StateOffline: it has not reported for a couple of minutes. Its history can still be read from the database.
	StateOffline State = "offline"
	// StateWaiting: it is registered but has not reported yet, so nothing is known about its version.
	StateWaiting State = "waiting"
	// StateIncompatible: it speaks another read contract than this program, so its schema is not read.
	StateIncompatible State = "incompatible"
)

// Entry is a server of the register and how it looks.
type Entry struct {
	Server
	// Self is the server that is running this program.
	Self  bool
	State State
}

// Readable says whether the history of the server can be read from the database: it is this server, or it reported the read
// contract that this program speaks, whether it is online or not.
func (e Entry) Readable() bool {
	return e.Self || e.State == StateOnline || e.State == StateOffline
}

func stateOf(s Server, self bool) State {
	switch {
	case self:
		return StateOnline
	case s.Contract == 0:
		return StateWaiting
	case s.Contract != Contract:
		return StateIncompatible
	case s.LastSeen == nil || s.SeenAgo > OnlineWithin:
		return StateOffline
	}
	return StateOnline
}

// Errors of reading another server.
var (
	// ErrUnknownServer: the register has no such server.
	ErrUnknownServer = errors.New("cluster: no such server")
	// ErrSelf: the server asked for is the one that runs the program, which reads its own schema directly.
	ErrSelf = errors.New("cluster: that is this server")
	// ErrUnreachable: the read pool of the server could not be opened.
	ErrUnreachable = errors.New("cluster: the schema of that server cannot be read")
)

// NotReadableError says that a server is in the register but its history is not read: it has not reported yet, or it speaks a
// read contract that this program does not know.
type NotReadableError struct{ Entry Entry }

func (e *NotReadableError) Error() string {
	return fmt.Sprintf("cluster: the history of server %s is not read (%s)", e.Entry.Name, e.Entry.State)
}

// Options builds a Group.
type Options struct {
	// Pool is the pool of this server: its schema is the one that is looked up in the register.
	Pool *pgxpool.Pool
	// URL is the connection URL of the database, for the read pools of the other servers.
	URL string
	// Report says what this server tells the register about itself each time it reports.
	Report func() Report
	Log    *slog.Logger
	// Every is how often this server reports; zero means HeartbeatEvery.
	Every time.Duration
	// Registry is the schema of the register; empty means db.RegistrySchema. Only tests set it.
	Registry string
}

// Group is this server as a member of a group of servers that share a database: it reports that it is alive, lists the
// servers, and gives the readers of the others.
type Group struct {
	reg    *Registry
	url    string
	report func() Report
	log    *slog.Logger
	every  time.Duration
	schema string
	name   string

	failing atomic.Bool
	mu      sync.Mutex
	peers   map[string]*peer
}

type peer struct {
	schema string
	pool   *pgxpool.Pool
}

// Open returns the Group of this server, or nil when it is on its own: the register is not installed, or this server's
// schema is not in it. Nothing is asked of a server that is on its own, and it runs as it always did. A register that cannot
// be read is not an error either, because the service must start (it is told in the log); the group is what is lost.
func Open(ctx context.Context, o Options) (*Group, error) {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	schema, err := db.CurrentSchema(ctx, o.Pool)
	if err != nil {
		return nil, err
	}
	reg := NewRegistry(o.Pool, o.Registry)
	exists, err := reg.Exists(ctx)
	if err == nil && exists {
		var servers []Server
		if servers, err = reg.List(ctx); err == nil {
			for _, s := range servers {
				if s.Schema == schema {
					g := &Group{reg: reg, url: o.URL, report: o.Report, log: log, every: o.Every, schema: schema, name: s.Name, peers: map[string]*peer{}}
					if g.every <= 0 {
						g.every = HeartbeatEvery
					}
					if g.report == nil {
						g.report = func() Report { return Report{} }
					}
					g.beat(ctx)
					return g, nil
				}
			}
		}
	}
	if err != nil {
		log.Warn("the register of the servers cannot be read, so this server runs on its own; the other servers will not see it", "error", err)
	}
	return nil, nil
}

// Name is the name of this server in the register.
func (g *Group) Name() string { return g.name }

// Schema is the schema of this server.
func (g *Group) Schema() string { return g.schema }

// beat reports to the register; a failure is told once, and its end too.
func (g *Group) beat(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ok, err := g.reg.Heartbeat(ctx, g.schema, g.report())
	switch {
	case err != nil:
		if !g.failing.Swap(true) {
			g.log.Warn("this server could not report to the register of the servers, so the other servers see it as offline until it can", "error", err)
		}
	case !ok:
		if !g.failing.Swap(true) {
			g.log.Warn("this server is no longer in the register of the servers: register it again with \"timevault-server cluster register\"")
		}
	default:
		if g.failing.Swap(false) {
			g.log.Info("this server reports to the register of the servers again")
		}
	}
}

// Run reports until ctx ends.
func (g *Group) Run(ctx context.Context) {
	t := time.NewTicker(g.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.beat(ctx)
		}
	}
}

// Entries describes servers of the register as they look to the server whose schema is selfSchema.
func Entries(servers []Server, selfSchema string) []Entry {
	out := make([]Entry, len(servers))
	for i, s := range servers {
		self := s.Schema == selfSchema
		out[i] = Entry{Server: s, Self: self, State: stateOf(s, self)}
	}
	return out
}

// Servers lists the servers of the register, this one included, with how each looks now.
func (g *Group) Servers(ctx context.Context) ([]Entry, error) {
	servers, err := g.reg.List(ctx)
	if err != nil {
		return nil, err
	}
	return Entries(servers, g.schema), nil
}

// Reader returns the Reader of another server and how that server looks. The server is read from its schema in the database
// whether it is online or not; one that has not reported or that speaks another read contract is not read
// (*NotReadableError). The pool behind the reader is kept for the next call.
func (g *Group) Reader(ctx context.Context, name string) (*service.Reader, Entry, error) {
	entries, err := g.Servers(ctx)
	if err != nil {
		return nil, Entry{}, err
	}
	for _, e := range entries {
		if e.Name != name {
			continue
		}
		rd, err := g.ReaderOf(ctx, e)
		return rd, e, err
	}
	return nil, Entry{}, ErrUnknownServer
}

// ReaderOf is Reader for an entry that Servers returned, so that a page that reads several servers asks the register once.
func (g *Group) ReaderOf(ctx context.Context, e Entry) (*service.Reader, error) {
	switch {
	case e.Self:
		return nil, ErrSelf
	case !e.Readable():
		return nil, &NotReadableError{Entry: e}
	}
	pool, err := g.poolFor(ctx, e)
	if err != nil {
		return nil, err
	}
	roots := service.Roots{Repository: e.RepositoryRoot, Restore: e.RestoreRoot, RestoreDisplay: e.RestoreDisplayRoot}
	return service.NewReader(pool, func() service.Roots { return roots }), nil
}

// poolFor returns the read-only pool on the schema of a server, opening it the first time (or when the register names
// another schema for the server).
func (g *Group) poolFor(ctx context.Context, e Entry) (*pgxpool.Pool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if p := g.peers[e.Name]; p != nil {
		if p.schema == e.Schema {
			return p.pool, nil
		}
		p.pool.Close()
		delete(g.peers, e.Name)
	}
	pool, err := db.Connect(ctx, g.url, db.Options{MaxConns: peerConns, SearchPath: e.Schema, ReadOnly: true, StatementTimeout: peerStatementTimeout})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	g.peers[e.Name] = &peer{schema: e.Schema, pool: pool}
	return pool, nil
}

// Close closes the read pools of the other servers.
func (g *Group) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name, p := range g.peers {
		p.pool.Close()
		delete(g.peers, name)
	}
}
