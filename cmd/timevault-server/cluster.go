package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/cluster"
	"timevault/internal/db"
)

const clusterUsage = `usage: timevault-server cluster <command>

Several servers that run TimeVault can share one database, each in a schema of its own, and show each other's folders
(docs/adr/0019-several-servers-one-database-one-schema-each.md). The register of the servers is the schema timevault_cluster.

commands:
  register <name>   put this server in the register under a short name (lower case letters, digits, - and _).
                    This server is the one that TIMEVAULT_DATABASE_URL names: its schema is the first one of the
                    search path of that URL. The register is made when it does not exist yet. Restart the service
                    afterwards: it joins the group when it starts.
  list              the servers of the register, how they look and when they were last heard of
  remove <name>     take a server out of the register (its schema and its data stay as they are)

These commands work on the database that TIMEVAULT_DATABASE_URL names, with the account of that URL. Making the
register needs the right to create a schema; where the account has not got it, the database administrator makes the
schema once (CREATE SCHEMA timevault_cluster AUTHORIZATION <the account>;).
`

// registrySchema is the schema of the register of the servers; empty means db.RegistrySchema, which is what every installation
// uses. Only the tests set it, so that they do not meet in the one register of the database.
var registrySchema string

// openDatabase connects with the bootstrap settings, the way the commands that work on the database do.
func openDatabase(ctx context.Context) (*pgxpool.Pool, string, error) {
	boot, _, err := loadBootstrap()
	if err != nil {
		return nil, "", err
	}
	if err := boot.ValidateDatabase(); err != nil {
		return nil, "", err
	}
	pool, err := db.Connect(ctx, boot.DatabaseURL, db.Options{MaxConns: 2})
	if err != nil {
		return nil, "", err
	}
	return pool, boot.DatabaseURL, nil
}

// runCluster registers this server in the register of the servers, lists it, or takes a server out of it.
func runCluster(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(clusterUsage)
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, url, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	return clusterCommand(ctx, os.Stdout, pool, url, args)
}

// clusterCommand carries out one command of runCluster; url is what the register is made with.
func clusterCommand(ctx context.Context, out io.Writer, pool *pgxpool.Pool, url string, args []string) error {
	schema, err := db.CurrentSchema(ctx, pool)
	if err != nil {
		return err
	}
	reg := cluster.NewRegistry(pool, registrySchema)
	switch args[0] {
	case "register":
		if len(args) != 2 {
			return errors.New("usage: timevault-server cluster register <name>")
		}
		return clusterRegister(ctx, out, pool, reg, url, schema, args[1])
	case "list":
		return clusterList(ctx, out, reg, schema)
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: timevault-server cluster remove <name>")
		}
		if exists, err := reg.Exists(ctx); err != nil {
			return err
		} else if !exists {
			return errors.New("there is no register of servers in this database")
		}
		servers, err := reg.List(ctx)
		if err != nil {
			return err
		}
		ok, err := reg.Remove(ctx, args[1])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no server is registered under the name %q (see: timevault-server cluster list)", args[1])
		}
		fmt.Fprintf(out, "%s was taken out of the register; its schema and its data were not touched\n", args[1])
		for _, s := range servers {
			if s.Name == args[1] && s.Schema == schema {
				fmt.Fprintln(out, "That is this server: it leaves the group when the service is restarted, and the other servers stop reading its history.")
			}
		}
		return nil
	}
	return fmt.Errorf("unknown cluster command %q\n\n%s", args[0], clusterUsage)
}

func clusterRegister(ctx context.Context, out io.Writer, pool *pgxpool.Pool, reg *cluster.Registry, url, schema, name string) error {
	if err := cluster.ValidName(name); err != nil {
		return err
	}
	if err := db.ValidSchemaName(schema); err != nil {
		return fmt.Errorf("the schema of this server (%s) cannot be registered: %w", schema, err)
	}
	if schema == db.RegistrySchema {
		return fmt.Errorf("TIMEVAULT_DATABASE_URL selects the schema %q, which is the register of the servers; give it the search_path of this server's own schema", schema)
	}
	if _, found, err := db.CatalogVersion(ctx, pool, schema); err != nil {
		return err
	} else if !found {
		return fmt.Errorf("the schema %q holds no TimeVault catalog yet: run \"timevault-server migrate\" (or start the service once) first", schema)
	}
	applied, err := cluster.Install(ctx, url, registrySchema)
	if err != nil {
		return fmt.Errorf("the register could not be made: %w", err)
	}
	if len(applied) > 0 {
		fmt.Fprintf(out, "the register of the servers was made in the schema %s\n", db.RegistrySchema)
	}
	if err := reg.Register(ctx, name, schema, registeredBy()); err != nil {
		return err
	}
	fmt.Fprintf(out, "this server (schema %s) is registered as %q; restart the service so that it joins the group\n", schema, name)
	return nil
}

// registeredBy names who registered a server: the account of the operating system that ran the command.
func registeredBy() string {
	if name := os.Getenv("USERNAME"); name != "" {
		return "cli:" + name
	}
	if name := os.Getenv("USER"); name != "" {
		return "cli:" + name
	}
	return "cli"
}

func clusterList(ctx context.Context, out io.Writer, reg *cluster.Registry, schema string) error {
	exists, err := reg.Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(out, "there is no register of servers in this database: this server (schema %s) runs on its own\n", schema)
		return nil
	}
	servers, err := reg.List(ctx)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Fprintln(out, "the register is empty")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSCHEMA\tSTATE\tVERSION\tLAST HEARD OF\t")
	// The state is what the register says about every server, this one included: this command is not the service, and a
	// service that is not running is not online.
	for _, e := range cluster.Entries(servers, "") {
		name := e.Name
		if e.Schema == schema {
			name += " (this server)"
		}
		heard := "never"
		if e.LastSeen != nil {
			heard = e.SeenAgo.Round(time.Second).String() + " ago"
		}
		version := e.Version
		if version == "" {
			version = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t\n", name, e.Schema, e.State, version, heard)
	}
	return tw.Flush()
}

const moveSchemaUsage = `usage: timevault-server move-schema --to <schema> [--from <schema>] [--apply]

Moves the tables of this server (the catalog, the jobs, the audit trail and the Site settings) from one schema of the database
into another, in one transaction. It takes a few milliseconds whatever the size of the tables, and it can be undone with the
same command and the two names swapped. Without --apply it only says what it would do and changes nothing.

  --from   the schema to move from; by default the first schema of the search path of TIMEVAULT_DATABASE_URL
           (public when the URL has no search_path)
  --to     the schema to move into; it is made when it does not exist and the account may make schemas, otherwise the
           database administrator makes it once:  CREATE SCHEMA <name> AUTHORIZATION <owner of the tables>;
  --apply  make the move

The service must be stopped (the command refuses while it runs). The order is: stop the service, run the command,
add  search_path=<schema>  to the query string of TIMEVAULT_DATABASE_URL, start the service. If this server is in the
register of the servers, the register is changed to name the new schema.
`

// runMoveSchema moves the catalog of this server into another schema (see db.MoveSchema).
func runMoveSchema(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(moveSchemaUsage)
		return nil
	}
	fs := flag.NewFlagSet("move-schema", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	to := fs.String("to", "", "")
	from := fs.String("from", "", "")
	apply := fs.Bool("apply", false, "")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n\n%s", err, moveSchemaUsage)
	}
	if fs.NArg() > 0 || *to == "" {
		return fmt.Errorf("--to <schema> is required, and nothing else is expected\n\n%s", moveSchemaUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, _, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	return moveSchema(ctx, os.Stdout, pool, *from, *to, *apply)
}

func moveSchema(ctx context.Context, out io.Writer, pool *pgxpool.Pool, from, to string, apply bool) error {
	if from == "" {
		var err error
		if from, err = db.CurrentSchema(ctx, pool); err != nil {
			return err
		}
	}
	move, err := db.MoveSchema(ctx, pool, from, to, apply)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "catalog at version %d in schema %q: %d tables (%s) and %d function, owned by %q\n",
		move.Version, move.From, len(move.Tables), strings.Join(move.Tables, ", "), len(move.Functions), move.Owner)
	create := "uses the existing schema"
	if move.CreatesSchema {
		create = "makes the schema"
	}
	if !move.Applied {
		fmt.Fprintf(out, "preview: the move into schema %q %s and moves them all in one transaction. Nothing was changed. Stop the service and run the command again with --apply to do it.\n", move.To, create)
		return nil
	}
	fmt.Fprintf(out, "moved into schema %q (%s)\n", move.To, create)
	reg := cluster.NewRegistry(pool, registrySchema)
	if exists, err := reg.Exists(ctx); err == nil && exists {
		name, found, err := reg.MoveSchema(ctx, move.From, move.To)
		switch {
		case err != nil:
			fmt.Fprintf(out, "WARNING: the register of the servers could not be changed to name the new schema: %v\n"+
				"The other servers read the old schema until it is: run  UPDATE %s.servers SET schema = '%s' WHERE schema = '%s';\n",
				err, db.RegistrySchema, move.To, move.From)
		case found:
			fmt.Fprintf(out, "the register now names schema %q for the server %q\n", move.To, name)
		}
	}
	fmt.Fprintf(out, "\nNext: add  search_path=%s  to the query string of TIMEVAULT_DATABASE_URL (in the .env file or the environment;\n"+
		"for example  ...?sslmode=disable&search_path=%s ), then start the service.\n", move.To, move.To)
	return nil
}
