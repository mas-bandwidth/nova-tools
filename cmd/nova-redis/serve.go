package main

// serve.go owns the local Redis instance. `serve` launches one redis-server in
// the foreground under the store's binding, authentication, and persistence
// rules, keeping one config for the store:
//
//   - bound to loopback and tailnet addresses only (100.64.0.0/10 and
//     fd7a:115c:a1e0::/48); a wildcard, public or LAN address, or a hostname,
//     is refused before anything starts, and --bind has no default;
//   - with auth taken at run time from NOVA_REDIS_PASSWORD, which `nova-secrets
//     exec` fills; the password reaches redis-server on stdin only, never in
//     an argument, never in the child's environment and never in a file;
//   - with the store in --dir, which has no default: the AOF on and fsynced
//     every second, an RDB snapshot every 60 s after any write, and no
//     eviction, so a restart on the same --dir replays every key and a full
//     instance refuses a write rather than drop a card. Nothing sets a TTL
//     policy: store keys do not expire;
//   - with the store's ACL users in --dir/users.acl (mode 0600), so the users
//     nova-redis acl apply sets (and saves with ACL SAVE) are loaded again on
//     a restart. serve writes the file's default user before each launch with
//     the password's SHA-256 (never the password), because redis-server
//     ignores requirepass once an ACL file is named and would bring the
//     default user up with no password; every other line is kept as the store
//     saved it. --users names the users the file must hold, and a file that
//     lacks one is refused before anything is written or launched.
//
// serve's output is redis-server's own stream plus its START and STOP lines,
// so it is a Prints verb.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// redisServerProgram is the instance program, found on PATH.
const redisServerProgram = "redis-server"

// The tailnet ranges Tailscale assigns: the CGNAT block for IPv4 and the
// Tailscale ULA prefix for IPv6.
var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// launchSpec is everything `serve` hands the launch seam: the program and its
// argv, the child's environment, the config it reads on stdin, and the store
// directory that config names.
type launchSpec struct {
	Program string
	Args    []string
	Env     []string
	Config  []byte
	Dir     string
}

// launchRedis runs redis-server in the foreground with the config on stdin and
// returns when it exits. A cancelled context (SIGINT/SIGTERM to serve) is
// passed on as SIGTERM, which redis-server treats as a clean shutdown: it
// fsyncs the AOF, saves the RDB and exits 0, and that is a stop, not a failure
// (exec reports the cancelled context even when the child exited 0).
func launchRedis(ctx context.Context, spec launchSpec, stdout, stderr io.Writer) error {
	cmd := subproc.Long(ctx, spec.Program, spec.Args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.Env = spec.Env
	cmd.Dir = spec.Dir
	cmd.Stdin = strings.NewReader(string(spec.Config))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil && ctx.Err() != nil && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		return nil
	}
	return err
}

// serveVerb is the serve verb: runs redis-server in the foreground. A doctor repair
// supplies all five public login metadata flags; it never relaxes authentication.
func serveVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:    "serve",
		Usage:   "serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <name>[,<name>...]] [--secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME>] [--dry-run]",
		Example: "version",
		Effect:  tool.LocalWrite,
		DryRun:  true,
		Detail: `The store's ACL users live in <store-dir>/` + aclFileName + ` (mode 0600), which nova-redis acl apply
writes through with ACL SAVE, so a restart keeps them; serve writes its default user
(the password's SHA-256, never the password) before each launch. --users names the users
that file must hold: a store whose file lacks one is refused and nothing starts.
--dry-run validates --bind, --port and --dir's absolute shape, checks --users against
the ACL file, and prints the binding, the port, the store directory, the ACL file and
the persistence, auth and eviction rules it would use. It defers every effect: it
creates no directory, writes no file, reads no password or environment, looks up no
redis-server and launches nothing.`,
		Flags: func(f *tool.Flags) {
			f.Prints()
			f.String("bind", "", "comma-separated IP addresses to listen on, loopback (127.0.0.1, ::1) or tailnet (100.64.0.0/10, fd7a:115c:a1e0::/48) only")
			f.String("port", "", "the TCP port to listen on, 1 to 65535 (6379 is Redis's own)")
			f.String("dir", "", "the absolute path of the store directory (AOF, RDB and ACL files), created 0700 when missing")
			f.String("users", "", "comma-separated ACL users the store's ACL file must hold (those nova-redis acl apply set); a store missing one is refused")
			loginUnitFlags(f)
			f.Check(func(c *tool.Call) {
				if !c.Given("bind") {
					c.Problem("--bind is required: comma-separated IP addresses to listen on, loopback (127.0.0.1, ::1) or tailnet (100.64.0.0/10, fd7a:115c:a1e0::/48) only; refusing to guess")
				} else if _, err := validBinds(c.Str("bind")); err != nil {
					c.Problem(err.Error())
				}
				if !c.Given("port") {
					c.Problem("--port is required: the TCP port to listen on, 1 to 65535 (6379 is Redis's own); refusing to guess")
				} else if port, err := strconv.Atoi(c.Str("port")); err != nil || port < 1 || port > 65535 {
					c.Problem(fmt.Sprintf("--port %q needs a port from 1 to 65535", c.Str("port")))
				}
				if !c.Given("dir") {
					c.Problem("--dir is required: the absolute path of the store directory (AOF and RDB files), created 0700 when missing; refusing to guess")
				} else if !filepath.IsAbs(c.Str("dir")) {
					c.Problem(fmt.Sprintf("--dir %q is not absolute; name the store directory in full", c.Str("dir")))
				}
				// The password is one more input the real run cannot start
				// without, so it is checked HERE with the flags: one refusal
				// names every problem at once (STANDARD section 2, "recovery
				// takes one turn"). A dry run reads no password or environment.
				if !c.DryRun() && !serveLoginGiven(c) && d.getenv(PasswordEnv) == "" {
					c.Problem(servePasswordProblem())
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out { return serveRun(c, d) },
	}
}

// serveOptions is the truth serve parses from the line: the binding, the port
// and the store directory. It carries no password and no process: those are
// effects the real run adds after the parse, so the preview reports the same
// options without reading a secret or looking one up.
type serveOptions struct {
	binds []string
	port  int
	dir   string
	users []string
}

// parseServe validates the line serve and its preview share: --bind parses to
// loopback and tailnet addresses only, --port is 1 to 65535, and --dir is an
// absolute path. It is pure: it creates no directory, reads no secret or
// environment and looks up no process, so --dry-run can print the plan the
// real run takes and the two cannot drift.
func parseServe(c *tool.Call) (serveOptions, error) {
	binds, err := validBinds(c.Str("bind"))
	if err != nil {
		return serveOptions{}, err
	}
	port, err := strconv.Atoi(c.Str("port"))
	if err != nil || port < 1 || port > 65535 {
		return serveOptions{}, fmt.Errorf("--port %q needs a port from 1 to 65535", c.Str("port"))
	}
	if !filepath.IsAbs(c.Str("dir")) {
		return serveOptions{}, fmt.Errorf("--dir %q is not absolute; name the store directory in full", c.Str("dir"))
	}
	users, err := validUsers(c.Str("users"))
	if err != nil {
		return serveOptions{}, err
	}
	return serveOptions{binds: binds, port: port, dir: filepath.Clean(c.Str("dir")), users: users}, nil
}

// validUsers parses --users: ACL user names, none empty, none holding whitespace or a quote.
func validUsers(text string) ([]string, error) {
	if text == "" {
		return nil, nil
	}
	var out []string
	for _, raw := range strings.Split(text, ",") {
		u := strings.TrimSpace(raw)
		if u == "" || strings.ContainsAny(u, " \t\r\n\"") {
			return nil, fmt.Errorf("--users %q holds an empty name or one with whitespace or a quote; name each ACL user once, comma-separated", text)
		}
		out = append(out, u)
	}
	return out, nil
}

// serveRun is serve's body: launches redis-server in the foreground.
// --dry-run prints the parsed launch options and the store's persistence and
// auth rules and returns without effects: it creates no directory, reads no
// secret or environment, looks up no process and launches nothing. The real
// run uses the same options, then creates the store directory, checks
// authentication and launches.
func serveRun(c *tool.Call, d deps) *tool.Out {
	// Read --dry-run before any refusal: the skeleton fails a --dry-run call
	// whose verb never read it.
	dryRun := c.DryRun()
	opts, err := parseServe(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	acl := filepath.Join(opts.dir, aclFileName)
	// The check reads the ACL file and writes nothing, so the preview makes
	// it too and refuses exactly when the real run would.
	if _, err := aclUsers(acl, opts.users); err != nil {
		return tool.Refuse(err.Error())
	}
	if dryRun {
		fmt.Fprintf(c.Stdout, "SERVE OK bind=%s port=%d auth=on persistence=aof eviction=none dir=%s aclfile=%s users=%d dry_run=true created=0 launched=0\n",
			oneline.Field(strings.Join(opts.binds, ",")), opts.port, oneline.Field(opts.dir), oneline.Field(acl), len(opts.users))
		return tool.Exit(0)
	}
	dir, err := storeDir(opts.dir)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	password, err := servePassword(c, d)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	program, err := d.lookPath(redisServerProgram)
	if err != nil {
		fmt.Fprintf(c.Stderr, "SERVE FAILED err=%s remedy=%q\n", oneline.Err(fmt.Errorf("%s not found on PATH: %w", redisServerProgram, err)),
			"install redis-server (Redis 7 or later) so it is on PATH, then run nova-redis serve again")
		return tool.Exit(1)
	}
	kept, err := writeACLFile(filepath.Join(dir, aclFileName), password, opts.users)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	spec := launchSpec{
		Program: program,
		Args:    []string{"-"},
		Env:     withoutEnv(d.environ(), PasswordEnv),
		Config:  redisConfig(opts.binds, opts.port, password, dir),
		Dir:     dir,
	}
	fmt.Fprintf(c.Stdout, "SERVE START bind=%s port=%d auth=on persistence=aof eviction=none dir=%s aclfile=%s users=%d program=%s\n",
		oneline.Field(strings.Join(opts.binds, ",")), opts.port, oneline.Field(dir), oneline.Field(filepath.Join(dir, aclFileName)), kept, oneline.Field(program))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := d.launch(ctx, spec, c.Stdout, c.Stderr); err != nil {
		fmt.Fprintf(c.Stderr, "SERVE FAILED err=%s remedy=%q\n", oneline.Err(err), "run: ls -ld -- "+oneline.ShellWord(dir)+"; compare directory access and the explicit --bind/--port with the launch error and any redis-server output")
		return tool.Exit(1)
	}
	fmt.Fprintf(c.Stdout, "SERVE STOP bind=%s port=%d\n", oneline.Field(strings.Join(opts.binds, ",")), opts.port)
	return tool.Exit(0)
}

// serveLoginGiven reports whether the line names any part of the secrets login
// serve reads its password from: the login is all five flags or none.
func serveLoginGiven(c *tool.Call) bool {
	return c.Str("secrets") != "" || c.Str("as") != "" || c.Str("key") != "" || c.Str("sops") != "" || c.Str("secret") != ""
}

// servePasswordProblem is serve's refusal when the line names no login and
// PasswordEnv is empty: it names both ways to give serve its password, so the
// reader fixes the call in one turn.
func servePasswordProblem() string {
	return fmt.Sprintf("%s is empty; name the login serve reads the password from (--secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME>), or run under `nova-secrets exec --only %s -- nova-redis serve ...`; auth comes from nova-secrets at run time, never an argument", PasswordEnv, PasswordEnv)
}

// servePassword is the instance's password, read in this process: from the login
// the flags name (--secrets, --as, --key, --sops, --secret: one name in one seat of a
// secrets store, read through internal/secrets.ReadLogin, the path nova-secrets exec
// takes), else from PasswordEnv, which nova-secrets exec fills. The login is how a
// unit written by install store or install bus runs serve with no wrapper and no
// password in the unit; the value is handed to redis-server on stdin only.
func servePassword(c *tool.Call, d deps) (string, error) {
	l := secrets.Login{Store: c.Str("secrets"), As: c.Str("as"), Key: c.Str("key"), Sops: c.Str("sops"), Name: c.Str("secret")}
	given := serveLoginGiven(c)
	if !given {
		password := d.getenv(PasswordEnv)
		if password == "" {
			return "", errors.New(servePasswordProblem())
		}
		return password, nil
	}
	var missing []string
	for _, f := range []struct{ v, flag string }{{l.Store, "--secrets"}, {l.As, "--as"}, {l.Key, "--key"}, {l.Sops, "--sops"}, {l.Name, "--secret"}} {
		if f.v == "" {
			missing = append(missing, f.flag)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("%w; it names no %s", errNoLogin, strings.Join(missing, ", "))
	}
	read := d.readLogin
	if read == nil {
		read = secrets.ReadLogin
	}
	s, err := read(l)
	if err != nil {
		return "", fmt.Errorf("the password of the login %s does not resolve: %v", l.String(), err)
	}
	password := ""
	_ = s.Use(func(p string) error { password = p; return nil }) // ignored: the function never fails
	return password, nil
}

// validBinds parses --bind and refuses every address that is not loopback or
// tailnet. One bad entry refuses the whole list: a partial bind is a guess.
func validBinds(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("--bind is empty; name 127.0.0.1 and/or a tailnet address, refusing to guess")
	}
	var out []string
	for _, raw := range strings.Split(text, ",") {
		a := strings.TrimSpace(raw)
		if a == "" {
			return nil, fmt.Errorf("--bind %q holds an empty entry", text)
		}
		ip, err := netip.ParseAddr(a)
		if err != nil {
			return nil, fmt.Errorf("--bind %q is not an IP address; name the address, a hostname can resolve to a public interface", a)
		}
		if ip.Zone() != "" {
			return nil, fmt.Errorf("--bind %q carries a zone; only loopback and tailnet addresses are bound", a)
		}
		ip = ip.Unmap()
		switch {
		case ip.IsUnspecified():
			return nil, fmt.Errorf("--bind %q binds every interface; name 127.0.0.1 or a tailnet address", a)
		case ip.IsLoopback(), tailnetV4.Contains(ip), tailnetV6.Contains(ip):
			out = append(out, ip.String())
		default:
			return nil, fmt.Errorf("--bind %q is neither loopback nor tailnet (100.64.0.0/10, fd7a:115c:a1e0::/48); a public interface is never bound", a)
		}
	}
	return out, nil
}

// storeDir checks --dir: an absolute path that is a directory, created 0700
// when missing. A relative path would put the store wherever serve was
// started, which is a guess; a file there is refused, never replaced.
func storeDir(text string) (string, error) {
	if !filepath.IsAbs(text) {
		return "", fmt.Errorf("--dir %q is not absolute; name the store directory in full", text)
	}
	dir := filepath.Clean(text)
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return "", fmt.Errorf("--dir %q is not a directory", dir)
		}
		return dir, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("--dir %q: %v", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("--dir %q cannot be created: %v", dir, err)
	}
	return dir, nil
}

// redisConfig is the complete config redis-server reads on stdin. It applies
// the store's binding, authentication, persistence, and eviction rules.
// Every value that could hold a blank or a quote is a quoted redis string.
func redisConfig(binds []string, port int, password, dir string) []byte {
	var b strings.Builder
	b.WriteString("# nova-redis serve: loopback/tailnet only, auth on, the fleet store's rules\n")
	fmt.Fprintf(&b, "bind %s\n", strings.Join(binds, " "))
	fmt.Fprintf(&b, "port %d\n", port)
	b.WriteString("protected-mode yes\n")
	fmt.Fprintf(&b, "requirepass %s\n", redisQuote(password))
	fmt.Fprintf(&b, "dir %s\n", redisQuote(dir))
	// The ACL users live in the store's own file: acl apply's ACL SAVE writes
	// it and a restart loads it. With it named, redis-server takes the
	// default user from the file (writeACLFile), not from requirepass.
	fmt.Fprintf(&b, "aclfile %s\n", redisQuote(filepath.Join(dir, aclFileName)))
	b.WriteString("daemonize no\n")
	// Durability: the AOF fsyncs every second, so a crash loses at most one
	// second; an RDB snapshot every 60 s after any write is the second copy.
	b.WriteString("appendonly yes\n")
	b.WriteString("appendfsync everysec\n")
	b.WriteString("save 60 1\n")
	// No eviction: a full instance refuses the write; a card never vanishes.
	b.WriteString("maxmemory-policy noeviction\n")
	b.WriteString("notify-keyspace-events Ex\n")
	b.WriteString("latency-monitor-threshold 100\n")
	return []byte(b.String())
}

// redisQuote writes s as a double-quoted redis config argument: backslash and
// quote escaped, anything outside printable ASCII as \xHH.
func redisQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' || c == '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 0x20 || c > 0x7e:
			fmt.Fprintf(&b, "\\x%02x", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// withoutEnv returns env minus every entry for name, so the password the
// parent was handed by nova-secrets does not ride into redis-server's
// environment as well as its stdin.
func withoutEnv(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, name+"=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// aclFileName is the store's ACL file, under --dir.
const aclFileName = "users.acl"

// aclLines reads the ACL file at path: its lines, and the names of the users
// they define. A missing file is an empty one; anything at the path that is
// not a regular file (a directory, a link) is refused, never followed.
func aclLines(path string) (lines []string, users map[string]bool, err error) {
	users = map[string]bool{}
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, users, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("the ACL file %q: %v", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("the ACL file %q is not a regular file; move it aside, then run nova-redis serve again", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("the ACL file %q cannot be read: %v", path, err)
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines = append(lines, l)
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "user" {
			users[f[1]] = true
		}
	}
	return lines, users, nil
}

// aclUsers checks the ACL file at path holds every user in need, and returns
// how many users other than default it holds. It writes nothing.
func aclUsers(path string, need []string) (int, error) {
	_, users, err := aclLines(path)
	if err != nil {
		return 0, err
	}
	var missing []string
	for _, u := range need {
		if u != "default" && !users[u] {
			missing = append(missing, u)
		}
	}
	if len(missing) > 0 {
		return 0, fmt.Errorf("the ACL file %q is missing the users %s that --users names, so they could not log in; nothing was started. Restore the file from a copy, or start the store without --users, set them with nova-redis acl apply --password-env-for <user>=<VARIABLE>, then serve with --users again",
			path, strings.Join(missing, ","))
	}
	n := len(users)
	if users["default"] {
		n--
	}
	return n, nil
}

// writeACLFile checks the file holds need, then writes it back, mode 0600,
// with its default user set from the password: on, the password's SHA-256,
// every key, channel and command, as redis-server's own default user. Every
// other line is kept as the store saved it. The write is a temporary file in
// the same directory renamed over the old one, so a crash leaves the old file
// or the new one, never half of either. It returns how many users other than
// default the file holds.
func writeACLFile(path, password string, need []string) (int, error) {
	n, err := aclUsers(path, need)
	if err != nil {
		return 0, err
	}
	lines, _, err := aclLines(path)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256([]byte(password))
	var b strings.Builder
	fmt.Fprintf(&b, "user default on sanitize-payload #%s ~* &* +@all\n", hex.EncodeToString(sum[:]))
	for _, l := range lines {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "user" && f[1] == "default" {
			continue
		}
		b.WriteString(l + "\n")
	}
	if err := replaceFile(path, []byte(b.String())); err != nil {
		return 0, fmt.Errorf("the ACL file %q cannot be written: %v", path, err)
	}
	return n, nil
}

// replaceFile writes data to a temporary file beside path, mode 0600, syncs
// it and renames it over path.
func replaceFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// ignored: the temporary file is gone after a rename, and after a failure it is removed here
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close() // ignored: the chmod's error is the report
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close() // ignored: the write's error is the report
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() // ignored: the sync's error is the report
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
