// nova-sprint table reads one consistent Redis snapshot per render and prints
// it; it writes no file (#3326). A restarted unit re-renders from Redis on its
// next tick, so there is no last table to keep.
//
// A launchd kickstart -k SIGTERMs the unit's process group. The refresh
// the unit starts is put in its own session (POSIX setsid) so that signal
// does not kill it.
//
// Exit 0 ran, 2 could not run.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/sprinttable"
)

var version string

const usage = `nova-sprint: the sprint table, read from Redis (see docs/CLI.md)

usage:
  nova-sprint version
  nova-sprint help
  nova-sprint table --redis <addr> [--sprint <name>] [--once | --loop] [--out <file>]
  nova-sprint table --check --redis <addr>
  nova-sprint table --layout live [--redis <addr>] [--sprint <name>] [--friends <a,b,...>] [--once | --loop [<seconds>]] [--out <file>] [--lock <key>]
  nova-sprint table clear --checkpoint <file> [--redis <addr>] [--friends <a,b,...>] [--by <name>]
  nova-sprint table --compare <file> --redis <addr> --sprint <name> --friends <a,b,...> [--xy-file <file>]
  nova-sprint refresh [--dir <state dir>] -- <command> [arg...]
  nova-sprint refresh show <session dir>
  nova-sprint <verb> [<subverb>] -h
  nova-sprint redis-cli [--redis <addr>] -- <redis command...>

Every verb takes --seat <name> (or NOVA_SPRINT_SEAT, then NOVA_SEAT): the
seat's row in $XDG_CONFIG_HOME/nova-sprint/seats.tsv (name, redis addr, redis
user, secret env, store, key) names its Redis and login, and the password is
read from the seat's file in the nova-secrets store, through the library
nova-secrets exec runs on, with no wrapper (see docs/CLI.md).
  nova-sprint --seat coordinator redis <redis command...>
runs one raw command as the seat and prints the reply, or the refusal.

table reads one consistent FCALL_RO snapshot per render and prints it to
stdout; --loop renders once per second. With --out <file> each tick publishes
by writing <file>.tmp.<pid> beside it and renaming it, so a reader sees one
whole table. Without --out it writes no file: there is no --fixture or
--refresh pending, and a restart re-renders from Redis.
Load the function library first with
nova-sprint fn load --redis <addr> (fn check exits 1 while it is missing or stale).
Control sprints are hidden unless named with --sprint.
--layout live is Glenn's sprint table (#2674): the streams, workers and
benches of the copy model, rendered from Redis once a second.
--compare waits for the file's next publish, renders from Redis, and prints
MATCH (exit 0) or a unified diff (exit 1).
--check reads an existing throwaway fixture store and compares exact output.

refresh runs the command after -- in its own session (POSIX setsid) and
returns without waiting, so a unit restart does not kill it. The loop
plist sets AbandonProcessGroup (fleet/templates/nova-loop.plist.j2), which
stops launchd from signalling the unit's process group; setsid is the
refresh leaving that group itself. Exit 0 is the launch receipt, REFRESH
SESSION pid=<n> log=<file> result=<file>, under a session directory in
--dir (else $XDG_STATE_HOME/nova-sprint/refresh, else
~/.local/state/nova-sprint/refresh): the command's stdout and stderr go to
log, and when it ends the supervisor that outlives this process writes
result (its exit status and the log's tail). refresh show <session dir>
prints the result (exit 0), or RUNNING with the log so far (exit 3).

-h on any verb or subverb prints its usage line and every flag it takes on
stdout, and exits 2.

exit codes: 0 ran, 2 could not run, 6 the store did not answer or its ACL
refused the seat's user (a verb that documents its own store code, fleet's 5
and task push's 7, keeps it).

example:
  nova-sprint table --redis 127.0.0.1:6379 --once
  nova-sprint table --redis 127.0.0.1:6379 --sprint control-a --once
  nova-sprint table --check --redis 127.0.0.1:6379
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) (code int) {
	// -h on any verb or subverb: its usage line and flags on stdout, exit 2 (#3254).
	defer verbflag.Recover(stdout, "nova-sprint", &code)
	quietRedisOnce.Do(func() { redis.SetLogger(quietRedis{}) })
	// --seat <name> (or NOVA_SPRINT_SEAT, then NOVA_SEAT) anywhere before a
	// "--": every verb reads its Redis login from that seat through
	// nova-secrets' library (#4052), and its address and user from the seat's
	// row in seats.tsv (#4330, seat.go).
	args, err := selectSeat(seatcred.Process(), args, os.Getenv, os.Setenv)
	if err != nil {
		return refuse(stderr, "", err.Error())
	}
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; table renders, refresh detaches")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 {
			return refuse(stderr, "help", "help takes no arguments")
		}
		fmt.Fprint(stdout, usageWithRegisteredVerbs())
		return 0
	case "version", "--version":
		if len(args) > 1 {
			return refuse(stderr, "version", "version takes no arguments")
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-sprint", version))
		return 0
	case "table":
		return cmdTable(args[1:], stdout, stderr)
	case "refresh":
		return cmdRefresh(args[1:], stdout, stderr)
	default:
		if code, ok := runRegistered(args[0], args[1:], stdout, stderr); ok {
			return code
		}
		return refuse(stderr, "", fmt.Sprintf("unknown verb %s; table renders, refresh detaches", args[0]))
	}
}

// quietRedis drops go-redis's own pool lines ("pool.go:762: redis: connection
// pool: failed to dial after 5 attempts ..."), which the library printed to
// stderr ahead of the verb's one refusal line on every store it could not
// reach. The verb's line names the store and the remedy; the library's does
// not. SetLogger writes a package variable, so it is written once per
// process (nova-merge #1609, doctor did the same for itself).
type quietRedis struct{}

func (quietRedis) Printf(context.Context, string, ...interface{}) {}

var quietRedisOnce sync.Once

// exitStoreDown is the one exit code for a store that did not answer or
// whose ACL refused the seat's user: the code land, consume, reconcile and
// sprint open already used ("6 no Redis"), now every refuse caller's.
const exitStoreDown = 6

// noPermUser reads the ACL user out of Redis's own NOPERM line ("NOPERM User
// audit has no permissions to run the 'fcall' command").
var noPermUser = regexp.MustCompile(`NOPERM User (\S+) `)

// causeAddr is the first host:port a cause names ("dial tcp 10.0.0.5:6379:
// connect: connection refused", "redis at 127.0.0.1:6379: ...").
var causeAddr = regexp.MustCompile(`(?:\d{1,3}(?:\.\d{1,3}){3}|localhost|[A-Za-z][\w.-]*\.[A-Za-z]\w*):\d{2,5}\b`)

// refuse is the one refusal line every verb prints on stderr: the verb, the
// cause, and the next verb. The cause is read for the store's own errors
// (storeRefusal) so a missing function library, a store that did not answer
// and a seat the ACL refuses are each answered with the same remedy and the
// same exit code on every verb; anything else ends with the help door, exit 2.
func refuse(stderr io.Writer, verb, what string) int {
	where := ""
	if verb != "" {
		where = " " + verb
	}
	line, code := storeRefusal(what)
	fmt.Fprintf(stderr, "nova-sprint%s: %s\n", where, oneline.Escape(line))
	return code
}

// storeRefusal reads a refusal's cause for the store's own errors and gives
// each class one remedy and one exit code, whichever verb hit it: the
// nova_sprint function library not loaded on the store ("ERR Function not
// found") is `nova-sprint fn load --redis <addr>`, exit 2; a store that did
// not answer (store.UnreachableText) is `--redis` / `nova-sprint doctor`,
// exit 6; the seat's ACL user denied (NOPERM) names that user and
// `nova-sprint acl check`, exit 6. The address is the one this process last
// opened (store.LastOpened), since Redis's error names none; with no open
// yet the remedy spells the flag.
func storeRefusal(what string) (string, int) {
	// The cause's own address wins (a dial error carries it); a Redis reply
	// does not, so the process's last open is the store it was talking to.
	addr := store.LastOpened()
	if m := causeAddr.FindString(what); m != "" {
		addr = m
	}
	flag := "--redis <addr>"
	at := "the store"
	if addr != "" {
		flag, at = "--redis "+addr, addr
	}
	switch {
	case strings.Contains(what, "Function not found"):
		return what + "; the nova_sprint function library is not loaded on " + at +
			"; run: nova-sprint fn load " + flag + " (nova-sprint doctor " + flag + " shows the store)", 2
	case strings.Contains(what, "NOPERM"):
		user := "the seat's ACL user"
		if m := noPermUser.FindStringSubmatch(what); m != nil {
			user = "ACL user " + m[1]
		} else if u := os.Getenv(redisauth.UserEnv); u != "" {
			user = "ACL user " + u + " (" + redisauth.UserEnv + ")"
		}
		return what + "; " + at + " denies " + user + " (the seat's row in seats.tsv, else " + redisauth.UserEnv +
			"); run: nova-sprint acl check " + flag + " (the play writes the rows), or nova-sprint doctor " + flag, exitStoreDown
	case store.NoAnswerText(what):
		return what + "; " + at + " did not answer; check " + flag + " (or NOVA_SPRINT_REDIS), then run: nova-sprint doctor " + flag, exitStoreDown
	}
	return what + "; run: nova-sprint help", 2
}

// refreshReexecEnv marks a re-execution of this binary as refresh's
// supervisor: main runs run either way, and a test binary's TestMain runs
// run under it (main_test.go), so the same path is proved in the test tier.
const refreshReexecEnv = "NOVA_SPRINT_REEXEC"

// refreshSelf is the binary the supervisor is; a test does not replace it.
var refreshSelf = os.Executable

// refreshTailBytes is how much of the log the result quotes.
const refreshTailBytes = 4096

// cmdRefresh is refresh [--dir <d>] -- <command...>: a launch receipt (exit
// 0 means accepted), with the asynchronous work's durable, discoverable
// result. The command is started by a supervisor, this binary re-executed
// as `refresh --supervise <session> -- <command...>` in its own session, so
// neither this process's return nor a unit restart loses the command's
// outcome: the supervisor holds the log open, waits, and writes result.
// refresh show <session> prints them.
func cmdRefresh(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "show" {
		return refreshShow(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "--supervise" {
		return refreshSupervise(args[1:], stderr)
	}
	dir := ""
	if len(args) >= 2 && args[0] == "--dir" {
		dir, args = args[1], args[2:]
	}
	if len(args) == 0 || args[0] != "--" {
		return refuse(stderr, "refresh", "needs -- and the command to run in its own session, for example: nova-sprint refresh -- /usr/bin/true")
	}
	argv := args[1:]
	if len(argv) == 0 {
		return refuse(stderr, "refresh", "needs a command after --; that command is what survives the unit restart")
	}
	session, err := refreshSession(dir)
	if err != nil {
		return refuse(stderr, "refresh", err.Error())
	}
	self, err := refreshSelf()
	if err != nil {
		return refuse(stderr, "refresh", "cannot find this executable for the supervisor: "+err.Error())
	}
	sup := append([]string{self, "refresh", "--supervise", session, "--"}, argv...)
	log, result := filepath.Join(session, "log"), filepath.Join(session, "result")
	pid, err := startOwnSessionLogEnv(sup, log, append(os.Environ(), refreshReexecEnv+"=1"))
	if err != nil {
		return refuse(stderr, "refresh", err.Error())
	}
	fmt.Fprintf(stdout, "REFRESH SESSION pid=%d log=%s result=%s\n", pid, log, result)
	return 0
}

// refreshSession makes one session directory under dir (else the state
// dir): <dir>/<utc stamp>-<random>, so two refreshes in the same second do
// not share a log.
func refreshSession(dir string) (string, error) {
	if dir == "" {
		dir = os.Getenv("XDG_STATE_HOME")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("no --dir and no home for the state directory: %v", err)
			}
			dir = filepath.Join(home, ".local", "state")
		}
		dir = filepath.Join(dir, "nova-sprint", "refresh")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("refresh directory %s: %v", dir, err)
	}
	session, err := os.MkdirTemp(dir, time.Now().UTC().Format("20060102-150405")+"-")
	if err != nil {
		return "", fmt.Errorf("refresh session under %s: %v", dir, err)
	}
	return session, nil
}

// refreshSupervise is `refresh --supervise <session> -- <command...>`, the
// process refresh starts in its own session: it runs the command with its
// stdout and stderr appended to <session>/log, waits, and writes
// <session>/result (the exit status and the log's tail) by rename, so a
// reader sees a whole result or none. A result it cannot write is named on
// its own stderr (the log, when refresh started it) with the exit status
// it could not record, exit 1; a command it cannot start is a result with
// exit=-1 and the error.
func refreshSupervise(args []string, stderr io.Writer) int {
	if len(args) < 3 || args[1] != "--" {
		fmt.Fprintln(stderr, "nova-sprint refresh --supervise: wants <session> -- <command...>")
		return 2
	}
	session, argv := args[0], args[2:]
	log, result := filepath.Join(session, "log"), filepath.Join(session, "result")
	f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "REFRESH SUPERVISE REFUSED session=%s err=%v\n", session, err)
		return 2
	}
	defer func() { _ = f.Close() }()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = f, f
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, refreshReexecEnv+"=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	started := time.Now()
	exit, wait := -1, ""
	if err := cmd.Start(); err != nil {
		wait = "start: " + err.Error()
	} else {
		if err := cmd.Wait(); err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				wait = "wait: " + err.Error()
			}
		}
		exit = cmd.ProcessState.ExitCode()
	}
	ended := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "REFRESH RESULT exit=%d cmd=%s started=%s ended=%s log=%s", exit, oneline.Field(argv[0]),
		started.UTC().Format(time.RFC3339), ended.UTC().Format(time.RFC3339), log)
	if wait != "" {
		fmt.Fprintf(&b, " wait=%s", oneline.Field(wait))
	}
	b.WriteString("\n")
	tail := refreshTail(log)
	fmt.Fprintf(&b, "--- log tail (%d bytes) ---\n%s", len(tail), tail)
	if len(tail) > 0 && !strings.HasSuffix(tail, "\n") {
		b.WriteString("\n")
	}
	tmp := result + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err == nil {
		err = os.Rename(tmp, result)
		if err == nil {
			return 0
		}
		_ = os.Remove(tmp)
		fmt.Fprintf(stderr, "REFRESH RESULT-WRITE-FAILED result=%s exit=%d err=%v\n", result, exit, err)
		return 1
	} else {
		fmt.Fprintf(stderr, "REFRESH RESULT-WRITE-FAILED result=%s exit=%d err=%v\n", result, exit, err)
		return 1
	}
}

// refreshTail is the last refreshTailBytes of the log, or "".
func refreshTail(log string) string {
	b, err := os.ReadFile(log)
	if err != nil {
		return ""
	}
	if len(b) > refreshTailBytes {
		b = b[len(b)-refreshTailBytes:]
	}
	return string(b)
}

// refreshShow is `refresh show <session>`: the result when the command has
// ended (exit 0), RUNNING with the log so far when it has not (exit 3), and
// a refusal for a directory that is no session.
func refreshShow(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return refuse(stderr, "refresh show", "wants one session directory, the one REFRESH SESSION named (log=<session>/log)")
	}
	session := args[0]
	if b, err := os.ReadFile(filepath.Join(session, "result")); err == nil {
		_, _ = stdout.Write(b)
		return 0
	}
	log := filepath.Join(session, "log")
	if _, err := os.Stat(log); err != nil {
		return refuse(stderr, "refresh show", "no refresh session at "+session+": neither result nor log ("+err.Error()+")")
	}
	tail := refreshTail(log)
	fmt.Fprintf(stdout, "REFRESH RUNNING session=%s log=%s: no result yet, the command has not ended\n--- log tail (%d bytes) ---\n%s", session, log, len(tail), tail)
	if len(tail) > 0 && !strings.HasSuffix(tail, "\n") {
		fmt.Fprintln(stdout)
	}
	return 3
}

// startOwnSession is refresh's start: argv in its own session (POSIX
// setsid), not waited for, so neither a unit restart nor the end of the
// session that ran the verb kills it.
func startOwnSession(argv []string) (int, error) { return startOwnSessionLog(argv, "") }

// startOwnSessionLog is startOwnSession with the started process's stdout
// and stderr appended to log (its directory made) when log is not "": friend
// pull, card work and task take start a friend's beat loop through it, so a
// loop that backs off says why somewhere (seat-keeps-beat).
func startOwnSessionLog(argv []string, log string) (int, error) {
	return startOwnSessionLogEnv(argv, log, nil)
}

// startOwnSessionLogEnv is startOwnSessionLog with the started process's
// environment (nil inherits this one's).
func startOwnSessionLogEnv(argv []string, log string, env []string) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	if err := sprinttable.ApplyOwnSession(cmd); err != nil {
		return 0, err
	}
	if log != "" {
		if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
			return 0, fmt.Errorf("log %s: %v", log, err)
		}
		f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return 0, fmt.Errorf("log %s: %v", log, err)
		}
		defer func() { _ = f.Close() }() // the child holds its own descriptor
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("cannot start %s: %v", argv[0], err)
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}
