package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// redis-cli is the rare hand read (#4052): one redis-cli command under the
// seat's login, with no wrapper script. The password reaches redis-cli as
// REDISCLI_AUTH in that child's environment only -- never an argument, never
// this process's environment, never printed. stdout is redis-cli's; the
// receipt is one line on stderr. Exit 0 the command ran, 1 redis-cli failed,
// 2 refused.
func init() {
	register(Verb{
		Name:    "redis-cli",
		Summary: "[--seat <name>] [--redis <addr>] -- <cmd...>: one redis-cli command under the seat's Redis login (password in the child's env only)",
		Run:     cmdRedisCLI,
	})
}

const redisCLIWants = "wants [--seat <name>] [--redis <host:port>] -- <redis command...>, for example: nova-sprint redis-cli --seat studio --redis 127.0.0.1:6380 -- ZCARD sprint:S:cards"

func cmdRedisCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("redis-cli")
	addr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	i := indexOf(args, "--")
	if i < 0 || i == len(args)-1 {
		// -h before the "--" still answers (verbflag); anything else is refused.
		if err := fs.Parse(args); err != nil {
			return refuse(stderr, "redis-cli", redisCLIWants)
		}
		return refuse(stderr, "redis-cli", "no command after --; "+redisCLIWants)
	}
	if err := fs.Parse(args[:i]); err != nil {
		return refuse(stderr, "redis-cli", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "redis-cli", redisCLIWants)
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(*addr))
	if err != nil || host == "" || port == "" {
		return refuse(stderr, "redis-cli", "--redis <host:port> (or NOVA_SPRINT_REDIS) is required; "+redisCLIWants)
	}
	c, ok, err := seatcred.Active()
	if !ok {
		return refuse(stderr, "redis-cli", "no seat: pass --seat <name> or set "+seatcred.SeatEnv+"; the password is read from the seat's file, never a flag")
	}
	if err != nil {
		return refuse(stderr, "redis-cli", err.Error())
	}
	bin, err := exec.LookPath("redis-cli")
	if err != nil {
		return refuse(stderr, "redis-cli", "no redis-cli on PATH; install redis")
	}
	argv := append([]string{"-h", host, "-p", port, "--user", c.User, "--no-auth-warning"}, args[i+1:]...)
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Env = seatcred.ChildEnv(os.Environ(), c)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			code = ee.ExitCode()
		}
	}
	fmt.Fprintf(stderr, "REDIS-CLI %s addr=%s cmd=%s exit=%d\n", c, oneline.Field(net.JoinHostPort(host, port)), oneline.Field(args[i+1]), code)
	if code != 0 {
		return 1
	}
	return 0
}

func indexOf(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}
