package redisuser

import (
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2/server"
)

// fakeRedis is a minimal Redis on the server package of miniredis. It answers
// PING, SCAN and HGETALL from a map and knows neither HELLO, AUTH nor CLIENT,
// like the Redis 3.x and 5.x servers dash may run on. Unlike miniredis it pages
// SCAN replies, and it records every command it receives.
type fakeRedis struct {
	srv *server.Server

	mu        sync.Mutex
	commands  []string
	hashes    map[string]map[string]string // a nil hash is a key that vanished after SCAN listed it
	notHashes map[string]bool              // keys that answer HGETALL with WRONGTYPE
	pageSize  int                          // keys per SCAN reply; 0 means all at once
	hang      chan struct{}                // when not nil, PING waits until it is closed
}

func newFakeRedis(t *testing.T) *fakeRedis {
	t.Helper()
	srv, err := server.NewServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake server: %v", err)
	}
	f := &fakeRedis{srv: srv, hashes: map[string]map[string]string{}, notHashes: map[string]bool{}}
	srv.SetPreHook(func(_ *server.Peer, cmd string, args ...string) bool {
		f.mu.Lock()
		f.commands = append(f.commands, strings.Join(append([]string{cmd}, args...), " "))
		f.mu.Unlock()
		return false
	})
	for name, handler := range map[string]server.Cmd{"PING": f.ping, "SCAN": f.scan, "HGETALL": f.hgetall} {
		if err := srv.Register(name, handler); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		f.mu.Lock()
		if f.hang != nil {
			close(f.hang)
		}
		f.mu.Unlock()
		srv.Close()
	})
	return f
}

func (f *fakeRedis) url() string { return "redis://" + f.srv.Addr().String() }

// add stores users as dash does, under the default prefix.
func (f *fakeRedis) add(users ...user) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range users {
		f.hashes[u.key()] = u.hash()
	}
}

// ghost lists key in SCAN although HGETALL finds nothing; wrongType makes HGETALL answer WRONGTYPE.
func (f *fakeRedis) ghost(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hashes[key] = nil
}

func (f *fakeRedis) wrongType(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notHashes[key] = true
}

func (f *fakeRedis) setPageSize(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageSize = n
}

// hangPings leaves PING unanswered until the test ends.
func (f *fakeRedis) hangPings() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hang = make(chan struct{})
}

// count returns how many commands starting with prefix (such as "SCAN" or "HELLO 2") were received.
func (f *fakeRedis) count(prefix string) int { return len(f.matching(prefix)) }

func (f *fakeRedis) matching(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.commands {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeRedis) ping(c *server.Peer, _ string, _ []string) {
	f.mu.Lock()
	hang := f.hang
	f.mu.Unlock()
	if hang != nil {
		<-hang
	}
	c.WriteInline("PONG")
}

func (f *fakeRedis) scan(c *server.Peer, _ string, args []string) {
	cursor, _ := strconv.Atoi(args[0])
	match := "*"
	for i := 1; i+1 < len(args); i += 2 {
		if strings.EqualFold(args[i], "MATCH") {
			match = args[i+1]
		}
	}
	f.mu.Lock()
	var keys []string
	for k := range f.hashes {
		keys = append(keys, k)
	}
	for k := range f.notHashes {
		keys = append(keys, k)
	}
	pageSize := f.pageSize
	f.mu.Unlock()
	sort.Strings(keys)
	var matching []string
	for _, k := range keys {
		if ok, _ := path.Match(match, k); ok {
			matching = append(matching, k)
		}
	}
	page, next := matching, 0
	if pageSize > 0 {
		page = nil
		if cursor < len(matching) {
			end := min(cursor+pageSize, len(matching))
			page = matching[cursor:end]
			if cursor > 0 {
				page = append([]string{matching[cursor-1]}, page...) // SCAN may return a key again
			}
			if end < len(matching) {
				next = end
			}
		}
	}
	c.WriteLen(2)
	c.WriteBulk(strconv.Itoa(next))
	c.WriteLen(len(page))
	for _, k := range page {
		c.WriteBulk(k)
	}
}

func (f *fakeRedis) hgetall(c *server.Peer, _ string, args []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notHashes[args[0]] {
		c.WriteError("WRONGTYPE Operation against a key holding the wrong kind of value")
		return
	}
	hash := f.hashes[args[0]]
	names := make([]string, 0, len(hash))
	for name := range hash {
		names = append(names, name)
	}
	sort.Strings(names)
	c.WriteLen(2 * len(names))
	for _, name := range names {
		c.WriteBulk(name)
		c.WriteBulk(hash[name])
	}
}
