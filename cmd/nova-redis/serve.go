package main

// serve.go is the instance-owner verb of docs/SPEC-REDIS.md ("Bind, auth and
// persistence"; nova-tools #2281, in the instance-owner role #3582 keeps for
// this binary). `serve` launches the one local redis-server in the foreground:
//
//   - bound to loopback and tailnet addresses only (100.64.0.0/10 and
//     fd7a:115c:a1e0::/48); a wildcard, public or LAN address, or a hostname,
//     is refused before anything starts, and --bind has no default;
//   - with auth taken at run time from NOVA_REDIS_PASSWORD, which `nova-secrets
//     exec` fills; the password reaches redis-server on stdin only, never in
//     an argument, never in the child's environment and never in a file;
//   - with persistence off (`save ""`, `appendonly no`) and a fresh, empty
//     working directory per launch, so a restart has nothing to replay.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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
// argv, the child's environment, the config it reads on stdin, and the fresh
// working directory that config names.
type launchSpec struct {
	Program string
	Args    []string
	Env     []string
	Config  []byte
	Dir     string
}

// launchRedis runs redis-server in the foreground with the config on stdin and
// returns when it exits. A cancelled context (SIGINT/SIGTERM to serve) is
// passed on as SIGTERM, which redis-server treats as a clean shutdown.
func launchRedis(ctx context.Context, spec launchSpec, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, spec.Program, spec.Args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.Env = spec.Env
	cmd.Dir = spec.Dir
	cmd.Stdin = strings.NewReader(string(spec.Config))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func cmdServe(args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	bindText := fs.String("bind", "", "comma-separated loopback or tailnet addresses")
	portText := fs.String("port", "", "port")
	if !parse(fs, args, stderr, "bind", "port") {
		return 2
	}
	binds, err := validBinds(*bindText)
	if err != nil {
		return refuse(stderr, " serve", err.Error())
	}
	port, err := strconv.Atoi(*portText)
	if err != nil || port < 1 || port > 65535 {
		return refuse(stderr, " serve", fmt.Sprintf("--port %q needs a port from 1 to 65535", *portText))
	}
	password := d.getenv(PasswordEnv)
	if password == "" {
		return refuse(stderr, " serve", fmt.Sprintf("%s is empty; run under `nova-secrets exec --only %s -- nova-redis serve ...` so auth comes from nova-secrets at run time, never an argument", PasswordEnv, PasswordEnv))
	}
	program, err := d.lookPath(redisServerProgram)
	if err != nil {
		fmt.Fprintf(stderr, "SERVE FAIL err=%s\n", oneline.Err(fmt.Errorf("%s not found on PATH: %w", redisServerProgram, err)))
		return 1
	}
	// A fresh, empty directory per launch: redis-server loads a dump.rdb or
	// an AOF it finds in its dir at start, so a clean slate needs a dir that
	// has never held one. Nothing is ever written into it: persistence is off.
	dir, err := os.MkdirTemp(d.tempRoot(), "nova-redis-")
	if err != nil {
		fmt.Fprintf(stderr, "SERVE FAIL err=%s\n", oneline.Err(err))
		return 1
	}
	spec := launchSpec{
		Program: program,
		Args:    []string{"-"},
		Env:     withoutEnv(d.environ(), PasswordEnv),
		Config:  redisConfig(binds, port, password, dir),
		Dir:     dir,
	}
	fmt.Fprintf(stdout, "SERVE START bind=%s port=%d auth=on persistence=off dir=%s program=%s\n",
		oneline.Field(strings.Join(binds, ",")), port, oneline.Field(dir), oneline.Field(program))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := d.launch(ctx, spec, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "SERVE FAIL err=%s\n", oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "SERVE STOP bind=%s port=%d\n", oneline.Field(strings.Join(binds, ",")), port)
	return 0
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

// redisConfig is the whole config redis-server reads on stdin. Every value
// that could hold a blank or a quote is written as a quoted redis string.
func redisConfig(binds []string, port int, password, dir string) []byte {
	var b strings.Builder
	b.WriteString("# nova-redis serve: loopback/tailnet only, auth on, persistence off\n")
	fmt.Fprintf(&b, "bind %s\n", strings.Join(binds, " "))
	fmt.Fprintf(&b, "port %d\n", port)
	b.WriteString("protected-mode yes\n")
	fmt.Fprintf(&b, "requirepass %s\n", redisQuote(password))
	b.WriteString("save \"\"\n")
	b.WriteString("appendonly no\n")
	fmt.Fprintf(&b, "dir %s\n", redisQuote(dir))
	b.WriteString("daemonize no\n")
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
