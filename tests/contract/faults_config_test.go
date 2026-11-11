//go:build functional

package contract

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// One PostgreSQL for the whole run, one database per row.
var pg struct {
	once sync.Once
	port string
	data string
	err  string
}

func pgBin(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	found, _ := filepath.Glob("/usr/lib/postgresql/*/bin/" + name)
	if len(found) > 0 {
		return found[len(found)-1]
	}
	return ""
}

func startPG() {
	initdb, ctl := pgBin("initdb"), pgBin("pg_ctl")
	if initdb == "" || ctl == "" || pgBin("postgres") == "" {
		pg.err = "initdb, pg_ctl or postgres is not installed"
		return
	}
	pg.data, pg.port = filepath.Join(rootDir, "pg"), freePort()
	steps := [][]string{
		{initdb, "-D", pg.data, "-U", "nova", "--auth=trust", "-E", "UTF8"},
		{ctl, "-D", pg.data, "-w", "-l", filepath.Join(rootDir, "pg.log"), "-o", fmt.Sprintf("-p %s -k %s -c listen_addresses=127.0.0.1 -c fsync=off", pg.port, rootDir), "start"},
	}
	for _, s := range steps {
		cmd := exec.Command(s[0], s[1:]...)
		if os.Getenv("LC_ALL") == "" && os.Getenv("LANG") == "" {
			cmd.Env = append(os.Environ(), "LC_ALL=C") // Homebrew postgres on macOS will not start without a locale
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			log, _ := os.ReadFile(filepath.Join(rootDir, "pg.log")) // ignored: the log is a diagnostic
			pg.err = fmt.Sprintf("%v: %s%s", err, out, log)
			return
		}
	}
}

func stopPG() {
	if pg.data != "" && pg.err == "" {
		exec.Command(pgBin("pg_ctl"), "-D", pg.data, "-m", "immediate", "stop").Run()
	}
}

// PG makes a database for this row, points NOVA_PG_DSN at it and returns the
// DSN; load puts the schema in it.
var pgSeq atomic.Int64

func (e *Env) PG(load bool) string {
	pg.once.Do(startPG)
	if pg.err != "" {
		e.skip("%s", pg.err)
	}
	psql, err := exec.LookPath("psql")
	if err != nil {
		e.skip("psql is not on PATH")
	}
	db := fmt.Sprintf("r%d_%d", os.Getpid(), pgSeq.Add(1))
	base := "postgres://nova@127.0.0.1:" + pg.port + "/"
	dsn := base + db + "?sslmode=disable"
	if out, err := exec.Command(psql, base+"postgres?sslmode=disable", "-qc", "CREATE DATABASE "+db).CombinedOutput(); err != nil {
		e.t.Fatalf("create database: %v: %s", err, out)
	}
	e.Set("NOVA_PG_DSN", dsn)
	if load {
		schema := e.Exec("config", "", "schema")
		cmd := exec.Command(psql, dsn, "-q", "-v", "ON_ERROR_STOP=1")
		cmd.Stdin = strings.NewReader(schema.out)
		if out, err := cmd.CombinedOutput(); err != nil {
			e.t.Fatalf("load schema: %v: %s", err, out)
		}
	}
	return dsn
}

// SQL runs statements in a database made by PG.
func (e *Env) SQL(dsn, stmts string) {
	if out, err := exec.Command(pgBin("psql"), dsn, "-q", "-v", "ON_ERROR_STOP=1", "-c", stmts).CombinedOutput(); err != nil {
		e.t.Fatalf("sql: %v: %s", err, out)
	}
}

func init() {
	list := []string{"machine", "list"}
	dsn := func(v string) func(e *Env) { return func(e *Env) { e.Set("NOVA_PG_DSN", v) } }
	stopped := func(e *Env) {
		if _, err := exec.LookPath("psql"); err != nil {
			e.skip("psql is not on PATH") // the remedy the row is held to runs it
		}
		e.Set("NOVA_PG_DSN", "postgres://u@"+e.Dead()+"/db?sslmode=disable")
	}
	psqlFails := []string{`remedy "PGPASSWORD=\"$NOVA_PG_PASSWORD\" psql \"$NOVA_PG_DSN\" -c 'select 1'" starts with "PGPASSWORD=\"$NOVA_PG_PASSWORD\"", which is not a tool of this repository or a standard command`}
	register(
		Fault{Tool: "config", Name: "store unset", Argv: list, Want: Want{Reason: []string{"NOVA_PG_DSN"}}},
		Fault{Tool: "config", Name: "store unset and a flag missing", Argv: []string{"machine", "add", "m1", "--actor", "a"},
			Want: Want{Reason: []string{"--user", "NOVA_PG_DSN"}}},
		Fault{Tool: "config", Name: "dsn in key=value form", Setup: dsn("host=x dbname=y"), Argv: list,
			Want: Want{Reason: []string{"postgres://"}}},
		Fault{Tool: "config", Name: "dsn carries a password", Setup: dsn("postgres://u:pw@127.0.0.1:1/db"), Argv: list,
			Want: Want{Reason: []string{"NOVA_PG_PASSWORD"}}},
		Fault{Tool: "config", Name: "postgres stopped", Setup: stopped, Argv: list,
			Want: Want{Reason: []string{"cannot reach postgres"}}, Known: "config-store-refusal", Gap: psqlFails},
		Fault{Tool: "config", Name: "inventory: postgres stopped", Setup: stopped, Argv: []string{"inventory"},
			Want: Want{Reason: []string{"cannot reach postgres"}}, Known: "config-store-refusal", Gap: psqlFails},
		Fault{Tool: "config", Name: "schema not loaded", Setup: func(e *Env) { e.PG(false) }, Argv: list,
			Want: Want{Reason: []string{"schema"}}},
		Fault{Tool: "config", Name: "database missing", Setup: func(e *Env) {
			e.PG(false)
			e.Set("NOVA_PG_DSN", "postgres://nova@127.0.0.1:"+pg.port+"/nodb?sslmode=disable")
		}, Argv: list, Want: Want{Reason: []string{"does not exist"}}, Known: "config-store-refusal"},
		Fault{Tool: "config", Name: "inventory: a service row with corrupt argv", Setup: func(e *Env) {
			d := e.PG(true)
			e.SQL(d, `INSERT INTO config.machines(name,"user") VALUES ('m1','u'); INSERT INTO config.services(name,machine,argv,every) VALUES ('s1','m1','[not json]',5)`)
		}, Argv: []string{"inventory"}, Want: Want{Reason: []string{"s1", "argv"}}},
	)
}
