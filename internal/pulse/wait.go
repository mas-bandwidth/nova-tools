package pulse

// `nova-pulse wait` is the one waiter (nova-tools #2546). Glenn 2026-09-22: "the real
// solution is to do it in go as a tool verb". Tonight's coordinator typed a dozen one-off
// waiters into a zsh tool shell -- until a process is gone, until a file has a line, until
// a receipt exists, until a PR has a check state -- four of them broke on zsh word
// splitting, globs or $(...) quoting, and one silently never fired. bin/wait-for was the
// interim in bash; this is the verb that retires it.
//
// The shape is one loop and six conditions:
//
//	nova-pulse wait --until <cond> [args...] [--every d] [--timeout d] [-- cmd...]
//
// Exit 0 when the condition held, 2 on timeout, and ONE receipt line either way -- held to
// stdout, timeout to stderr -- because a waiter that returns without saying how long it
// waited leaves the caller guessing whether anything happened at all.
//
// A `-- cmd...` runs after the condition holds and its exit status becomes the verb's, the
// way bin/wait-for's did: `wait --until file-exists X -- nova-pulse harvest ...` is one
// line in a lane script, and a harvest that failed must not read as a wait that succeeded.
//
// Every outside fact is a seam -- the process table, the forge, the store, the bus root,
// the clock and the sleep -- so the tests below run the whole loop without a network, a
// spawned pipeline or a second of wall clock, and one integration test per reader proves
// the seam's production side against a real machine.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The wait verb's defaults, the same two bin/wait-for carried.
const (
	DefaultWaitEvery   = 20 * time.Second
	DefaultWaitTimeout = 30 * time.Minute
)

// MinPollInterval is the floor under --every. A poll faster than this is a spin: it reads
// the process table or the forge hundreds of times a second and tells the caller nothing
// the next tick would not.
const MinPollInterval = 10 * time.Millisecond

// waitEvidenceCap bounds what a receipt quotes back from a matched line, a process argv or
// a stored value. A receipt is one line; a matched line from a 4 MB log is not.
const waitEvidenceCap = 160

// PRState is what the forge says about one pull request, folded to the two words a wait
// asks about: the PR's own state and its check rollup.
type PRState struct {
	// State is OPEN, MERGED or CLOSED, as the forge spells it.
	State string
	// Checks is green, red, pending or none: green when every check concluded well and
	// none is still running, red on any failure, pending while any is queued or running,
	// and none when the head carries no checks at all.
	Checks string
}

// WaitInput is one invocation of the verb.
type WaitInput struct {
	// Until is the condition and its arguments: {"process-gone", "harvest-priority"}.
	Until []string
	// Every is the poll interval and Timeout is the bound on the whole wait.
	Every   time.Duration
	Timeout time.Duration
	// Cmd is what runs after the condition holds, from `-- cmd...`. Empty is no command.
	Cmd []string
	// Bus is the bus clone the `bus-note` condition reads. Required by that condition and
	// by no other.
	Bus string
	// Store is the fleet Redis the `redis-key` condition reads. Required by that condition
	// and by no other.
	Store StoreOptions

	Stdout io.Writer
	Stderr io.Writer

	// The seams. Each has a production default installed by the CLI; a test hands in its
	// own and opens nothing.
	Now   func() time.Time
	Sleep func(time.Duration)
	Self  int
	Procs func() ([]WaitProc, error)
	PR    func(repo string, number int) (PRState, error)
	Key   func(ctx context.Context, key string) (value string, exists bool, err error)
	Exec  func(cmd []string) int
}

// ParsePollInterval reads --every and --interval: a bare whole number is seconds, the way
// bin/wait-for's --every 20 read, and anything else is a Go duration (500ms, 90s, 2m). It
// is separate from parseTimeout because a poll may legitimately be sub-second and a
// timeout may not.
func ParsePollInterval(s string) (time.Duration, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, fmt.Errorf("it wants an interval, as in 20, 1s or 500ms")
	}
	if n, err := strconv.Atoi(t); err == nil {
		if n < 1 {
			return 0, fmt.Errorf("an interval of %d is not an interval; it wants one second or more, or a duration like 500ms", n)
		}
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(t)
	if err != nil {
		return 0, fmt.Errorf("%s is neither a whole number of seconds nor a duration like 500ms, 90s or 2m", oneline.Field(t))
	}
	if d < MinPollInterval {
		return 0, fmt.Errorf("%s is under %s; a poll that fast is a spin", oneline.Field(t), MinPollInterval)
	}
	return d, nil
}

// waitCond is one condition: its name, the arguments the receipt prints back, and the
// question the loop asks every tick. Held returns the evidence it found -- the matched
// line, the live process, the answering note -- so the receipt says what happened rather
// than only that something did.
type waitCond struct {
	name     string
	args     []string
	held     func() (evidence string, held bool, err error)
	blocking bool // true when Held itself waits up to the poll interval rather than returning at once
}

// WaitConditions is the vocabulary, in the order the help prints it. A condition outside
// this list is a refusal naming the six, never a wait that can never hold: bin/wait-for's
// unknown mode exited 2 and so does this.
var WaitConditions = []string{"process-gone", "file-has", "file-exists", "pr-check", "redis-key", "bus-note"}

// Wait polls until the condition holds, then runs the command if there is one.
func Wait(in WaitInput) int {
	stdout, stderr := in.Stdout, in.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	sleep := in.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	every := in.Every
	if every <= 0 {
		every = DefaultWaitEvery
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}

	cond, err := buildWaitCond(in)
	if err != nil {
		fmt.Fprintf(stderr, "nova-pulse wait: %s\n", oneline.Err(err))
		return 2
	}

	start := now()
	polls := 0
	lastErr := ""
	for {
		polls++
		evidence, held, err := cond.held()
		if err != nil {
			// A reader that failed is NOT a condition that held. It is carried to the
			// timeout receipt so the caller learns the wait spent thirty minutes failing
			// to reach the forge rather than thirty minutes on a PR that stayed red.
			lastErr = oneline.Cap(oneline.Escape(err.Error()), waitEvidenceCap)
		} else if held {
			spent := now().Sub(start)
			fmt.Fprintf(stdout, "WAIT HELD until=%s args=%s spent=%s polls=%d%s\n",
				oneline.Field(cond.name), oneline.Quote(strings.Join(cond.args, " ")),
				roundWaitDur(spent), polls, evidenceField(evidence))
			if len(in.Cmd) == 0 {
				return 0
			}
			run := in.Exec
			if run == nil {
				run = runWaitCommand
			}
			return run(in.Cmd)
		}
		spent := now().Sub(start)
		if spent >= timeout {
			fmt.Fprintf(stderr, "WAIT TIMEOUT until=%s args=%s timeout=%s spent=%s polls=%d%s\n",
				oneline.Field(cond.name), oneline.Quote(strings.Join(cond.args, " ")),
				roundWaitDur(timeout), roundWaitDur(spent), polls, waitLastField(lastErr, evidence))
			return 2
		}
		// A blocking condition already spent the interval inside Held; sleeping again
		// would double the latency of every tick.
		if !cond.blocking {
			if remaining := timeout - spent; remaining < every {
				sleep(remaining)
			} else {
				sleep(every)
			}
		}
	}
}

// evidenceField prints the evidence a held condition found, or nothing when it found none
// worth quoting (a process that is simply absent has no line to show).
func evidenceField(evidence string) string {
	if strings.TrimSpace(evidence) == "" {
		return ""
	}
	return " evidence=" + oneline.Quote(oneline.Cap(oneline.Escape(evidence), waitEvidenceCap))
}

// waitLastField is the timeout receipt's tail: the reader's last error if it had one, else the
// last thing it saw that was not enough.
func waitLastField(lastErr, evidence string) string {
	if lastErr != "" {
		return " last=" + oneline.Quote(lastErr)
	}
	if strings.TrimSpace(evidence) == "" {
		return ""
	}
	return " last=" + oneline.Quote(oneline.Cap(oneline.Escape(evidence), waitEvidenceCap))
}

// roundWaitDur prints a duration a person reads: whole seconds once past a second, and
// milliseconds below it, so a test's 300ms wait does not print as 0s.
func roundWaitDur(d time.Duration) string {
	if d >= time.Second {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Millisecond).String()
}

// runWaitCommand is the production `-- cmd...` runner: the command inherits this process's
// standard streams and its exit status becomes the verb's. It is never a shell -- the
// argv is passed through as given, which is the whole reason this verb exists instead of
// another line of zsh.
func runWaitCommand(cmd []string) int {
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "nova-pulse wait: %s: %s\n", oneline.Field(cmd[0]), oneline.Err(err))
		return 2
	}
	return 0
}

// buildWaitCond validates --until and returns the condition. Every arity and every
// required companion flag is checked HERE, before the first poll: a wait that will refuse
// at minute thirty because --bus was missing is a wait that wasted thirty minutes.
func buildWaitCond(in WaitInput) (*waitCond, error) {
	if len(in.Until) == 0 {
		return nil, fmt.Errorf("--until is required; it wants one of: %s", strings.Join(WaitConditions, ", "))
	}
	name, args := in.Until[0], in.Until[1:]
	switch name {
	case "process-gone":
		if len(args) != 1 {
			return nil, arity(name, "<pattern>", args)
		}
		return waitProcessGone(in, args[0]), nil
	case "file-has":
		if len(args) != 2 {
			return nil, arity(name, "<path> <regex>", args)
		}
		re, err := regexp.Compile(args[1])
		if err != nil {
			return nil, fmt.Errorf("--until file-has %s: the regex does not compile: %s",
				oneline.Field(args[0]), oneline.Err(err))
		}
		return waitFileHas(args[0], args[1], re), nil
	case "file-exists":
		if len(args) != 1 {
			return nil, arity(name, "<path>", args)
		}
		return waitFileExists(args[0]), nil
	case "pr-check":
		if len(args) != 3 {
			return nil, arity(name, "<repo> <n> <state>", args)
		}
		number, err := strconv.Atoi(args[1])
		if err != nil || number < 1 {
			return nil, fmt.Errorf("--until pr-check %s: %s is not a pull request number",
				oneline.Field(args[0]), oneline.Field(args[1]))
		}
		want := strings.ToLower(strings.TrimSpace(args[2]))
		if !validPRWant(want) {
			return nil, fmt.Errorf("--until pr-check: %s is not a state; it wants one of: %s",
				oneline.Field(args[2]), strings.Join(PRWants, ", "))
		}
		return waitPRCheck(in, args[0], number, want), nil
	case "redis-key":
		if len(args) != 2 {
			return nil, arity(name, "<key> <value>", args)
		}
		if in.Key == nil && strings.TrimSpace(in.Store.Addr) == "" {
			return nil, fmt.Errorf("--until redis-key wants --store <host:port>; the fleet store is where the sprint's coordination state lives and there is no default to guess")
		}
		return waitRedisKey(in, args[0], args[1]), nil
	case "bus-note":
		if len(args) != 1 {
			return nil, arity(name, "<id>", args)
		}
		if strings.TrimSpace(in.Bus) == "" {
			return nil, fmt.Errorf("--until bus-note wants --bus <clone>; the bus root is always a flag")
		}
		return waitBusNote(in.Bus, args[0]), nil
	}
	return nil, fmt.Errorf("%s is not a condition; it wants one of: %s",
		oneline.Field(name), strings.Join(WaitConditions, ", "))
}

func arity(name, wants string, got []string) error {
	return fmt.Errorf("--until %s wants %s, got %d argument(s): %s",
		name, wants, len(got), oneline.Quote(strings.Join(got, " ")))
}

// PRWants is what `pr-check` accepts for its third argument: the four check rollups and the
// two terminal PR states. `green` and `red` are the words the fleet already says.
var PRWants = []string{"green", "red", "pending", "none", "merged", "closed"}

func validPRWant(want string) bool {
	for _, w := range PRWants {
		if w == want {
			return true
		}
	}
	return false
}

// waitProcessGone holds when no process the pattern names is running. See waitproc.go for
// the two rules that keep a waiter from matching itself.
func waitProcessGone(in WaitInput, pattern string) *waitCond {
	read := in.Procs
	if read == nil {
		read = ReadWaitProcs
	}
	self := in.Self
	if self == 0 {
		self = os.Getpid()
	}
	return &waitCond{
		name: "process-gone",
		args: []string{pattern},
		held: func() (string, bool, error) {
			procs, err := read()
			if err != nil {
				return "", false, err
			}
			live := WaitLiveMatches(procs, pattern, self)
			if len(live) == 0 {
				return "", true, nil
			}
			return fmt.Sprintf("%d live, first pid=%d %s",
				len(live), live[0].Pid, strings.Join(live[0].Args, " ")), false, nil
		},
	}
}

// waitFileHas holds when the file exists and a line of it matches the regex. The matched
// text goes on the receipt: `wait --until file-has ./harvest.log 'LANDED #[0-9]+'` that
// says only "held" leaves the caller to go and read the log again.
func waitFileHas(path, source string, re *regexp.Regexp) *waitCond {
	return &waitCond{
		name: "file-has",
		args: []string{path, source},
		held: func() (string, bool, error) {
			raw, err := os.ReadFile(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return "", false, nil // not yet written is not an error
				}
				return "", false, err
			}
			if m := re.FindIndex(raw); m != nil {
				return matchedLine(raw, m[0]), true, nil
			}
			return "", false, nil
		},
	}
}

// matchedLine is the whole line the match fell on, so the evidence reads like the log.
func matchedLine(raw []byte, at int) string {
	start := 0
	if i := strings.LastIndexByte(string(raw[:at]), '\n'); i >= 0 {
		start = i + 1
	}
	rest := string(raw[start:])
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimRight(rest, "\r")
}

// waitFileExists holds as soon as the path is there.
//
// IT HOLDS ON AN EMPTY FILE, and bin/wait-for's `file` mode did not: that mode wanted a
// NON-EMPTY path, because the thing it waited on was a RESULT.md a card creates and then
// writes. A caller porting `wait-for file X` wants `--until file-has X .` -- one character
// of regex, any byte -- and this sentence is here because the difference is one silent
// early return in a lane script.
func waitFileExists(path string) *waitCond {
	return &waitCond{
		name: "file-exists",
		args: []string{path},
		held: func() (string, bool, error) {
			st, err := os.Stat(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return "", false, nil
				}
				return "", false, err
			}
			return fmt.Sprintf("%d bytes", st.Size()), true, nil
		},
	}
}

// waitPRCheck holds when the forge says the pull request is in the asked-for state.
func waitPRCheck(in WaitInput, repo string, number int, want string) *waitCond {
	read := in.PR
	if read == nil {
		read = GHPRState
	}
	return &waitCond{
		name: "pr-check",
		args: []string{repo, strconv.Itoa(number), want},
		held: func() (string, bool, error) {
			st, err := read(repo, number)
			if err != nil {
				return "", false, err
			}
			seen := fmt.Sprintf("state=%s checks=%s", st.State, st.Checks)
			switch want {
			case "merged", "closed":
				return seen, strings.EqualFold(st.State, want), nil
			default:
				return seen, strings.EqualFold(st.Checks, want), nil
			}
		},
	}
}

// waitRedisKey holds when the key carries the value. `*` as the value asks only that the
// key exist, which is the shape a lease or a stamp wants.
//
// IT POLLS, and the issue asked whether it could block. Redis has no blocking read for a
// string key: BLPOP and XREAD BLOCK are the list and stream forms, and a blocking read of
// `GET` would need keyspace notifications turned on for the instance, which this store does
// not have. The poll is the honest implementation for a string key, and the blocking read
// belongs to the event stream (#2563) where the store really does allow one. The `blocking`
// field on waitCond is here for that reader when it lands.
func waitRedisKey(in WaitInput, key, value string) *waitCond {
	read := in.Key
	return &waitCond{
		name: "redis-key",
		args: []string{key, value},
		held: func() (string, bool, error) {
			if read == nil {
				r, err := storeKeyReader(in.Store)
				if err != nil {
					return "", false, err
				}
				read = r
			}
			ctx := context.Background()
			got, ok, err := read(ctx, key)
			if err != nil {
				return "", false, err
			}
			if !ok {
				return "absent", false, nil
			}
			if value == "*" {
				return got, true, nil
			}
			return got, got == value, nil
		},
	}
}

// storeKeyReader dials the fleet store once, on the first poll rather than at build time,
// so a wait whose store is briefly down retries instead of refusing outright. The client
// is kept for the life of the wait; the password never leaves DialStore.
func storeKeyReader(o StoreOptions) (func(context.Context, string) (string, bool, error), error) {
	rdb, err := DialStore(context.Background(), o)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, key string) (string, bool, error) {
		v, err := rdb.Get(ctx, key).Result()
		if err != nil {
			// A key that is not there yet is the ordinary case of a wait, and a key of
			// the wrong type is a caller naming a hash where a string was meant -- both
			// are "not this value", not a reader that broke.
			if strings.Contains(err.Error(), "redis: nil") {
				return "", false, nil
			}
			if strings.Contains(err.Error(), "WRONGTYPE") {
				if n, e2 := rdb.Exists(ctx, key).Result(); e2 == nil && n > 0 {
					return "<not a string>", true, nil
				}
			}
			return "", false, err
		}
		return v, true, nil
	}, nil
}

// waitBusNote holds when somebody OTHER than the note's own sender has answered it.
//
// The bus's answered rule is per reader: a note is answered, for one reader, when a file in
// THAT READER'S lane carries its id on a Re line, or that reader's RECEIPTS records it. A
// note is therefore always "answered" in its own sender's lane and never interesting there,
// so this asks every other lane on the bus.
//
// HEARD IS NOT ANSWERED. A receipt in another lane says the note was read; it does not say
// it was answered, and this keeps waiting. The timeout receipt names the receipt it saw, so
// a wait that expired on a note somebody merely acknowledged says exactly that instead of
// looking like a note nobody ever opened.
func waitBusNote(root, id string) *waitCond {
	return &waitCond{
		name: "bus-note",
		args: []string{id},
		held: func() (string, bool, error) {
			cfg, err := bus.LoadConfig(root)
			if err != nil {
				return "", false, err
			}
			tab, err := bus.ReadBus(root, cfg)
			if err != nil {
				return "", false, err
			}
			n, ok := tab.Resolve(id)
			if !ok {
				return "no such note yet", false, nil
			}
			heard := ""
			for _, lane := range busLanes(root) {
				if lane == n.Lane {
					continue
				}
				by, kind := tab.AnswerFor(n, lane)
				switch kind {
				case bus.AnsweredByReply:
					return "answered by " + by, true, nil
				case bus.HeardByReceipt:
					if heard == "" {
						heard = "heard by " + by
					}
				}
			}
			return heard, false, nil
		},
	}
}

// busLanes lists the lane directories on the bus. ReadBus keeps the same list privately;
// this reads the root the same way so a lane with no roster entry is still asked.
func busLanes(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var lanes []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "from-") {
			lanes = append(lanes, e.Name())
		}
	}
	return lanes
}
