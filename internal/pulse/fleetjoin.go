package pulse

// `fleet join` is scripts/ts-join-one.sh as a verb: one bench joined to the tailnet.
//
// The one rule this verb exists to keep is where the auth key may be. It reaches this
// process ONLY through the environment variable named by --authkey-env, which
// `nova-secrets exec` fills for the length of the call; it is never a flag value, so it is
// in no argv and no shell history here; and it reaches the bench on the remote shell's
// stdin, piped into `tailscale up --auth-key=file:/dev/stdin`, so it is in no argv there
// either -- a key on a remote command line is readable by every process on that machine
// for as long as the command runs. Nothing prints it: whatever tailscale says back is
// scrubbed of the key before a line of it is shown.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// FleetJoinInput is everything `fleet join` needs apart from flag parsing.
type FleetJoinInput struct {
	Benches    string // the fleet file
	Name       string // the one bench, which is also its tailnet hostname
	SSH        string // the ssh program; empty is "ssh"
	Tailscale  string // the absolute path of tailscale ON THE BENCH
	AuthKeyEnv string // the environment variable holding the auth key
	Getenv     func(string) string
	Timeout    time.Duration
	Runner     FleetRunner
	Stdout     io.Writer
	Stderr     io.Writer
}

// FleetJoin joins one bench to the tailnet and prints one line:
// `FLEET <bench> JOINED ip=<addr>`. Exit 0 when the bench joined, 2 when the invocation was
// refused (no key in the named variable, a tailscale path that is not absolute, a bench the
// file does not carry, `studio`), 3 when the bench could not be reached or tailscale
// refused.
func FleetJoin(in FleetJoinInput) int {
	bench, code := fleetOneBench(in.Benches, in.Name, in.Stdout, in.Stderr, "join")
	if code != 0 {
		return code
	}
	if err := checkTailscalePath(in.Tailscale); err != nil {
		return refusal(in.Stderr, "FLEET", err)
	}
	name := strings.TrimSpace(in.AuthKeyEnv)
	if name == "" {
		return refusal(in.Stderr, "FLEET", fmt.Errorf(
			"missing --authkey-env; the auth key is never a flag value (run: nova-secrets exec --as <seat> -- nova-pulse fleet join --bench %s --authkey-env TAILSCALE_AUTH_KEY)", bench.Name))
	}
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	key := strings.TrimSpace(getenv(name))
	if key == "" {
		return refusal(in.Stderr, "FLEET", fmt.Errorf(
			"--authkey-env %s holds no key (run: nova-secrets exec --as <seat> -- nova-pulse fleet join --bench %s --tailscale %s --authkey-env %s)",
			oneline.Field(name), bench.Name, oneline.Field(in.Tailscale), oneline.Field(name)))
	}
	if strings.ContainsAny(key, "'\n\r") {
		return refusal(in.Stderr, "FLEET", fmt.Errorf(
			"--authkey-env %s holds a value with a quote or a newline in it; a tailscale auth key has neither (check the secret in the store)", oneline.Field(name)))
	}

	run := in.Runner
	if run == nil {
		run = SSHRunner{Program: in.SSH}
	}
	start := time.Now()
	fmt.Fprintf(in.Stderr, "JOIN WALK bench=%s hostname=%s\n", oneline.Field(bench.Name), oneline.Field(bench.Name))

	ctx, cancel := context.WithTimeout(context.Background(), fleetPowerTimeout(in.Timeout))
	defer cancel()
	raw, err := run.Run(ctx, bench.SSH, fleetJoinScript(bench.Home, in.Tailscale, bench.Name, key))
	out := scrubKey(raw, key)
	if what, ok := fleetMarker(out, "FLEETFAIL"); ok {
		fmt.Fprintf(in.Stdout, "FLEET %s JOIN FAILED %s\n", oneline.Field(bench.Name), oneline.Escape(what))
		return 3
	}
	if err != nil {
		return fleetUnreachable(in.Stdout, bench.Name, fleetReason(out, err))
	}
	ip, ok := fleetMarker(out, "FLEETJOIN")
	if !ok {
		return fleetUnreachable(in.Stdout, bench.Name, "no answer")
	}
	if strings.TrimSpace(ip) == "" {
		ip = "-"
	}
	fmt.Fprintf(in.Stdout, "FLEET %s JOINED ip=%s\n", oneline.Field(bench.Name), oneline.Field(strings.TrimSpace(ip)))
	fmt.Fprintf(in.Stderr, "JOIN DONE bench=%s elapsed=%s\n",
		oneline.Field(bench.Name), time.Since(start).Round(time.Millisecond))
	return 0
}

// checkTailscalePath holds --tailscale to an absolute path with nothing a remote command
// line must not carry: the value is run as root on the bench.
func checkTailscalePath(p string) error {
	t := strings.TrimSpace(p)
	if t == "" {
		return fmt.Errorf("missing --tailscale; refusing to guess (run: nova-pulse fleet join --tailscale /usr/local/bin/tailscale)")
	}
	if !strings.HasPrefix(t, "/") {
		return fmt.Errorf("--tailscale %s is not absolute; it is run as root on the bench and is never taken from a PATH (run: nova-pulse fleet join --tailscale /usr/local/bin/tailscale)", oneline.Field(t))
	}
	if strings.ContainsAny(t, " \t'\"`$\\;&|<>()\n") {
		return fmt.Errorf("--tailscale %s holds a character a remote command line must not carry", oneline.Field(t))
	}
	return nil
}

// scrubKey removes the key from anything about to be printed. The remote tools have no
// reason to echo it; this is the belt to that brace.
func scrubKey(out, key string) string {
	if key == "" {
		return out
	}
	return strings.ReplaceAll(out, key, "<redacted>")
}

// fleetJoinScript is the remote side: the key is piped into tailscale's stdin, never given
// as an argument, and the bench's tailnet address comes back on one marker line.
func fleetJoinScript(home, tailscale, hostname, key string) string {
	return strings.Join([]string{
		"HOME=" + fleetQuote(home),
		"export HOME",
		"ts=" + fleetQuote(tailscale),
		`[ -x "$ts" ] || { printf 'FLEETFAIL\tno tailscale at %s\n' "$ts"; exit 0; }`,
		// The key is on this line and on no command line: printf writes it to a pipe, and
		// `--auth-key=file:/dev/stdin` reads the pipe.
		`printf '%s' ` + fleetQuote(key) + ` | sudo -n "$ts" up --auth-key=file:/dev/stdin --hostname=` + fleetQuote(hostname) + ` --accept-dns=false > /tmp/nova-join.$$ 2>&1 || { printf 'FLEETFAIL\t%s\n' "$(tail -n 1 /tmp/nova-join.$$ 2>/dev/null)"; rm -f /tmp/nova-join.$$; exit 0; }`,
		`rm -f /tmp/nova-join.$$`,
		`printf 'FLEETJOIN\t%s\n' "$("$ts" ip -4 2>/dev/null | head -n 1)"`,
	}, "\n")
}
