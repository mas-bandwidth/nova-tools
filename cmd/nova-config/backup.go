// backup.go holds the backup verb: the configuration class's backup
// (docs/DATA.md, Backups). PostgreSQL is the source of truth for
// configuration, and this is its verified copy off the database: pg_dump
// in the custom format into --dir, read back whole by pg_restore --list,
// checked to hold schema config's table data, its SHA-256 written beside
// it, then the directory pruned to --keep. A dump that fails a check is
// removed and the older ones stay, so the directory holds only verified
// dumps, and one is pruned only after a newer one has verified: the same
// keep law as nova-sprint snapshot for the Redis classes.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

const (
	backupPrefix      = "config-"
	backupSuffix      = ".dump"
	backupSumSuffix   = ".sha256"
	backupKeepDefault = 14
	// backupBudget is how long one pg_dump or pg_restore --list may run.
	backupBudget = 30 * time.Minute
	// envPGBin names a directory holding pg_dump and pg_restore, for a machine
	// whose PostgreSQL client is not on PATH (the throwaway servers' variable).
	envPGBin = "NOVA_PG_BIN"
)

const backupMore = "a dump is pg_dump --format=custom, read back whole by pg_restore --list and refused unless it holds schema config's table data; its SHA-256 is written beside it as <file>.sha256; a dump that fails a check is removed and the older ones stay; the oldest are pruned past --keep only after a newer one verified; --every runs until interrupted, and a failed take leaves the older dumps and tries again after --every; restore onto an empty database with pg_restore --dbname <dsn> --no-owner <file>, then nova-config migrate (docs/DATA.md, Restore order)\n"

// pgRun runs a PostgreSQL client program with env added to a clean
// environment and returns its combined output.
type pgRun func(ctx context.Context, env []string, prog string, args ...string) ([]byte, error)

// runPGTool is pgRun on the machine: the program from NOVA_PG_BIN or PATH,
// under backupBudget, with an environment that names nothing but what the
// caller passes (no PGHOST or PGDATABASE of the shell's can redirect it).
func runPGTool(ctx context.Context, env []string, prog string, args ...string) ([]byte, error) {
	bin := prog
	if dir := os.Getenv(envPGBin); dir != "" {
		bin = filepath.Join(dir, prog)
	} else if p, err := exec.LookPath(prog); err == nil {
		bin = p
	} else {
		return nil, fmt.Errorf("%s is not on PATH and %s is unset; install the PostgreSQL client (at least the server's major version)", prog, envPGBin)
	}
	cmd, cancel := subproc.CommandFor(ctx, backupBudget, bin, args...)
	defer cancel()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "LC_ALL=C"}, env...)
	return cmd.CombinedOutput()
}

// pgEnv is the libpq environment that reaches the database dsn names, with
// the password in PGPASSWORD and never on the command line, where a ps
// reads it.
func pgEnv(dsn string) ([]string, error) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("the DSN could not be parsed; want postgres://user@host:5432/nova (%T)", err)
	}
	env := []string{"PGHOST=" + cfg.Host, fmt.Sprintf("PGPORT=%d", cfg.Port), "PGUSER=" + cfg.User, "PGDATABASE=" + cfg.Database}
	if cfg.Password != "" {
		env = append(env, "PGPASSWORD="+cfg.Password)
	}
	if mode := sslMode(dsn); mode != "" {
		env = append(env, "PGSSLMODE="+mode)
	}
	return env, nil
}

// sslMode is the sslmode the DSN names, in either spelling; "" leaves
// libpq's default.
func sslMode(dsn string) string {
	if strings.Contains(dsn, "://") {
		if u, err := url.Parse(dsn); err == nil {
			return u.Query().Get("sslmode")
		}
		return ""
	}
	for _, f := range strings.Fields(dsn) {
		if v, ok := strings.CutPrefix(f, "sslmode="); ok {
			return strings.Trim(v, "'")
		}
	}
	return ""
}

// backupTaken is one verified dump.
type backupTaken struct {
	File    string
	SHA256  string
	Bytes   int
	Entries int
	Tables  int
	Pruned  []string
}

// backupper takes, verifies and prunes the dumps in dir.
type backupper struct {
	dir  string
	keep int
	env  []string
	run  pgRun
	now  func() time.Time
}

// take is one dump: written to a temporary name, read back by pg_restore
// --list, renamed, its sum written, then the directory pruned.
func (b *backupper) take(ctx context.Context) (backupTaken, error) {
	var out backupTaken
	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		return out, fmt.Errorf("the backup directory %s cannot be made: %v; run: nova-config backup --dir <a writable directory>", b.dir, err)
	}
	name, err := b.name()
	if err != nil {
		return out, err
	}
	path := filepath.Join(b.dir, name)
	tmp := path + ".tmp"
	fail := func(err error) (backupTaken, error) {
		_ = os.Remove(tmp) // ignored: removing the failed dump; the error that failed it is what is returned
		return out, err
	}
	if msg, err := b.run(ctx, b.env, "pg_dump", "--format=custom", "--no-password", "--file", tmp); err != nil {
		return fail(fmt.Errorf("pg_dump failed: %v: %s", err, strings.TrimSpace(string(msg))))
	}
	body, err := os.ReadFile(tmp)
	if err != nil {
		return fail(fmt.Errorf("the dump %s cannot be read back: %v", name, err))
	}
	if len(body) == 0 {
		return fail(fmt.Errorf("pg_dump wrote an empty %s; the dump was removed", name))
	}
	list, err := b.run(ctx, nil, "pg_restore", "--list", tmp)
	if err != nil {
		return fail(fmt.Errorf("pg_restore --list cannot read the dump %s: %v: %s; the dump was removed", name, err, strings.TrimSpace(string(list))))
	}
	entries, tables := tocCounts(string(list))
	if tables == 0 {
		return fail(fmt.Errorf("the dump %s holds no table data of schema config (%d entries); the dump was removed; run: nova-config migrate, then backup again", name, entries))
	}
	sum := sha256.Sum256(body)
	hexsum := hex.EncodeToString(sum[:])
	if err := os.Rename(tmp, path); err != nil {
		return fail(fmt.Errorf("%s cannot be written: %v", path, err))
	}
	if err := writeSum(path+backupSumSuffix, hexsum+"  "+name+"\n"); err != nil {
		_ = os.Remove(path) // ignored: removing the dump whose sum could not be written; that error is what is returned
		return out, err
	}
	out = backupTaken{File: path, SHA256: hexsum, Bytes: len(body), Entries: entries, Tables: tables}
	if out.Pruned, err = b.prune(); err != nil {
		return out, err
	}
	return out, nil
}

// tocCounts reads pg_restore --list: every entry, and the entries that are
// table data of schema config.
func tocCounts(list string) (entries, tables int) {
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		entries++
		if strings.Contains(line, " TABLE DATA config ") {
			tables++
		}
	}
	return entries, tables
}

func writeSum(path, body string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return fmt.Errorf("%s cannot be written: %v", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // ignored: a leftover temp file; the write error is what is returned
		return fmt.Errorf("%s cannot be written: %v", path, err)
	}
	return nil
}

// name is the next file name: the clock's second, and a counter when two
// dumps fall in one second.
func (b *backupper) name() (string, error) {
	base := backupPrefix + b.now().UTC().Format("20060102T150405Z")
	for i := 0; i < 1000; i++ {
		n := base + backupSuffix
		if i > 0 {
			n = fmt.Sprintf("%s-%d%s", base, i, backupSuffix)
		}
		if _, err := os.Stat(filepath.Join(b.dir, n)); os.IsNotExist(err) {
			return n, nil
		}
	}
	return "", errors.New("a thousand dumps in one second; the clock is stuck")
}

// backupFiles is the dumps in dir, oldest first (names sort by time).
func backupFiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(n, backupSuffix) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (b *backupper) prune() ([]string, error) {
	files, err := backupFiles(b.dir)
	if err != nil {
		return nil, err
	}
	var gone []string
	for len(files) > b.keep {
		p := filepath.Join(b.dir, files[0])
		if err := os.Remove(p); err != nil {
			return gone, fmt.Errorf("%s cannot be pruned: %v", p, err)
		}
		_ = os.Remove(p + backupSumSuffix) // ignored: the sidecar of a dump already pruned; its absence is the goal
		gone = append(gone, p)
		files = files[1:]
	}
	return gone, nil
}

// runBackupTool dispatches backup through internal/tool.
func runBackupTool(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	return backupTool(d).RunContext(ctx, args, os.Stdin, stdout, stderr)
}

func backupTool(d deps) *tool.Tool {
	return &tool.Tool{
		Name:      toolName,
		What:      "a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis",
		ExitTable: "0 done, 1 refused (the dump failed or did not verify), 2 could not run (usage, or no store)",
		How: "each kind is a table in PostgreSQL schema config, applied into Redis.\n" +
			"backup is the configuration's verified copy off the database (docs/DATA.md).",
		Verbs: []tool.Verb{{
			Name:      "backup",
			Usage:     "backup --dir <dir> [--pg <dsn>] [--keep <n>] [--every <duration>] [--json]",
			Detail:    backupMore + "example: " + toolExamples["backup"],
			Effect:    tool.LocalWrite + "; reads PostgreSQL through pg_dump and writes nothing to it",
			ExitTable: "0 done (with --every: interrupted), 1 refused (the dump failed or did not verify), 2 could not run (usage, or no store)",
			Flags:     backupFlags,
			Run:       func(c *tool.Call) *tool.Out { return tool.Exit(runBackup(c, d)) },
		}},
	}
}

func backupFlags(f *tool.Flags) {
	f.Prints()
	storeFlags(f.FlagSet)
	f.String("dir", "", "the `directory` the dumps are written to, on a volume that outlives the database's host (required)")
	f.Int("keep", backupKeepDefault, "how many verified dumps stay; older ones are pruned after a newer one verifies")
	f.Duration("every", 0, "take a dump now and again each time this passes, until interrupted (default: once)")
	jsonFlag(f.FlagSet)
}

// runBackup is backup's body: one dump, or with --every one each time it
// passes until the run's context ends.
func runBackup(call *tool.Call, d deps) int {
	const verb = "backup"
	ctx, stdout, stderr := call.Ctx, call.Stdout, call.Stderr
	dir, keep, every, asJSON := call.Str("dir"), call.Int("keep"), call.Dur("every"), call.Bool("json")
	pg, file := call.Str("pg"), call.Str("file")
	if dir == "" || keep < 1 || every < 0 {
		return refuse(stderr, verb, "wants --dir <dir>, --keep of at least 1 and --every above zero when given; run: nova-config backup --dir /backup/postgres --keep 14")
	}
	if file != "" {
		return refuse(stderr, verb, "a --file store is a local JSON file, not a database: copy the file to back it up; backup reads PostgreSQL (--pg or NOVA_PG_DSN)")
	}
	dsn, err := conn{pg: &pg, file: &file}.dsn(d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	env, err := pgEnv(dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	run := d.pgRun
	if run == nil {
		run = runPGTool
	}
	now := d.now
	if now == nil {
		now = time.Now
	}
	key, value := where(dsn)
	b := &backupper{dir: dir, keep: keep, env: env, run: run, now: now}
	report := func(got backupTaken) {
		if asJSON {
			o := tool.Done().Fact(key, value).Fact("file", got.File).Fact("sha256", got.SHA256).Fact("bytes", got.Bytes).
				Fact("entries", got.Entries).Fact("tables", got.Tables).Fact("pruned", len(got.Pruned)).Fact("keep", keep)
			o.Verb = verb
			emit(stdout, o)
			return
		}
		fmt.Fprintf(stdout, "CONFIG BACKUP %s=%s file=%s sha256=%s bytes=%d entries=%d tables=%d verified=list+sum pruned=%d keep=%d\n",
			key, config.Value(value), config.Value(got.File), got.SHA256, got.Bytes, got.Entries, got.Tables, len(got.Pruned), keep)
	}
	if every == 0 {
		got, err := b.take(ctx)
		if err != nil {
			return refused(stderr, verb, err.Error(), "nova-config backup -h")
		}
		report(got)
		return 0
	}
	pause := d.pause
	if pause == nil {
		pause = sleepCtx
	}
	for {
		got, err := b.take(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			fmt.Fprintf(stderr, "%s %s FAILED: %s; the older dumps stay, the next try is in %s\n", toolName, verb, plain(err.Error()), every)
		} else {
			report(got)
		}
		if pause(ctx, every) != nil {
			return 0
		}
	}
}

// sleepCtx waits d, or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
