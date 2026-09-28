// Package launch is the bench side of a deal pass: `nova-sprint card launch
// --stdin` (#2756 4.3, #2931).
//
// The dealer opens one ssh session per bench and writes that bench's whole
// batch on its stdin, one line per card: <sprint> <label> <attempt> <token>.
// For each line the launcher starts the card wrapper detached, in its own
// session (POSIX setsid), with the command identity
// `nova-card <sprint>/<label>/<attempt>`, and then exits. It never holds the
// ssh session for a card's run: the wrapper's stdout and stderr are
// /dev/null, so the session closes as soon as the launcher returns, and a
// hang-up or kill of the session's process group does not reach a wrapper.
//
// The token is the wrapper's first line on stdin, never an argument: argv is
// what `ps` shows to every user on the bench, and receipts carry only the
// token's sha. The wrapper reads the line, checks it names its own identity,
// and calls `card launched` itself.
package launch

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// WrapperName is argv[0] of every card wrapper: the name `pgrep -f nova-card`
// and the reconciler's live-identity census match.
const WrapperName = "nova-card"

// LaunchAckFDEnv names the inherited file descriptor nova-card uses to
// acknowledge that Redis accepted `card launched`, or to report a refusal.
// The token never crosses this descriptor.
const LaunchAckFDEnv = "NOVA_CARD_LAUNCH_ACK_FD"

// LaunchDeadlineEnv is the batch's absolute Unix-millisecond deadline. The
// child and Redis transition both refuse to launch after it.
const LaunchDeadlineEnv = "NOVA_CARD_LAUNCH_DEADLINE_MS"

// DefaultBudget is how long one batch may take to start: the verb returns
// within it (#2931). A line reached at or after the budget is REFUSED timeout and
// its card is not started; the reconciler requeues a dealt card that never
// acked launched (#2756 3.2).
const DefaultBudget = 5 * time.Second

// maxLine bounds one stdin line; a canonical line is under 170 bytes.
const maxLine = 4096

var (
	sprintRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	labelRE  = regexp.MustCompile(`^[a-z0-9-]{1,80}$`)
	tokenRE  = regexp.MustCompile(`^([1-9][0-9]*)\.[0-9a-f]{32}$`)
)

// Line is one card of the batch: which attempt to start and its fenced token.
type Line struct {
	Sprint  string
	Label   string
	Attempt int
	Token   string
}

// Card is <sprint>/<label>/<attempt>, the card part of the command identity.
func (l Line) Card() string { return fmt.Sprintf("%s/%s/%d", l.Sprint, l.Label, l.Attempt) }

// CommandIdentity is what `ps -o command=` shows for this card's wrapper.
func (l Line) CommandIdentity() string { return WrapperName + " " + l.Card() }

// String is the canonical stdin line, token included; never print it.
func (l Line) String() string {
	return fmt.Sprintf("%s %s %d %s", l.Sprint, l.Label, l.Attempt, l.Token)
}

// ParseLine accepts only the canonical form String produces: sprint and
// label as the card identity has them, an attempt of 1 or more, and a token
// <attempt>.<128 bits hex> whose attempt is the line's.
func ParseLine(s string) (Line, error) {
	f := strings.Split(s, " ")
	if len(f) != 4 {
		return Line{}, errors.New("want <sprint> <label> <attempt> <token>")
	}
	if !sprintRE.MatchString(f[0]) {
		return Line{}, errors.New("sprint is not [a-z0-9-]{1,40}")
	}
	if !labelRE.MatchString(f[1]) {
		return Line{}, errors.New("label is not [a-z0-9-]{1,80}")
	}
	attempt, err := strconv.Atoi(f[2])
	if err != nil || attempt < 1 || strconv.Itoa(attempt) != f[2] {
		return Line{}, errors.New("attempt is not a number from 1")
	}
	m := tokenRE.FindStringSubmatch(f[3])
	if m == nil {
		return Line{}, errors.New("token is not <attempt>.<32 hex>")
	}
	if m[1] != f[2] {
		return Line{}, errors.New("token is for another attempt")
	}
	return Line{Sprint: f[0], Label: f[1], Attempt: attempt, Token: f[3]}, nil
}

// Config is the bench's launcher configuration.
type Config struct {
	// Wrapper is the absolute path of the card wrapper program. It is
	// started with argv[0] WrapperName, so its command identity is the
	// card's whatever the file is called.
	Wrapper string
	// Budget bounds the batch; zero means DefaultBudget.
	Budget time.Duration
	// Now is the clock the budget is read on; nil means time.Now.
	Now func() time.Time
	// Err, when set, gets every REFUSED line as well as out (#3700): the
	// dealer keeps stdout, and a session log on the bench keeps stderr.
	Err io.Writer
	// start is the process seam for deterministic deadline tests.
	start func(string, Line, time.Time) (int, string, error)
}

// Result counts the batch: wrappers started and lines refused.
type Result struct {
	Started int
	Refused int
	// Overran is true when the batch's total wall time, measured after the
	// last line's startDetached returned, is past Budget. The per-line
	// check only bounds the time reached BEFORE a given line's start; a
	// slow final start can still push the whole batch past the budget
	// without refusing anything. A caller must not treat Overran as
	// success even when Refused is 0 (#2931 HOLD 7).
	Overran bool
}

// Launch reads the batch from in and starts one detached wrapper per line
// as the line arrives. It writes one line per card to out:
//
//	LAUNCHED <sprint>/<label>/<attempt> pid=<pid>
//	REFUSED line=<n> <why>
//
// (each REFUSED line to cfg.Err as well, when set) and then LAUNCH
// started=<n> refused=<m> ms=<batch time> over=<bool>. A
// malformed line, a second line for an attempt already in this batch, a
// wrapper that would not start, or a line reached after the budget is
// refused; the rest of the batch still launches. over=true means the
// measured wall time, taken after the last line's own start returned, is
// past the budget even though every line that reached it was individually
// on time (Result.Overran); a caller must not read Refused==0 as success
// when over=true. The error is for a batch that could not run at all: no
// wrapper, or stdin failed.
func Launch(in io.Reader, out io.Writer, cfg Config) (Result, error) {
	var res Result
	if err := checkWrapper(cfg.Wrapper); err != nil {
		return res, err
	}
	now, budget := cfg.Now, cfg.Budget
	if now == nil {
		now = time.Now
	}
	if budget <= 0 {
		budget = DefaultBudget
	}
	began := now()
	deadline := began.Add(budget)
	start := cfg.start
	if start == nil {
		start = startDetached
	}
	refused := func(format string, args ...any) {
		res.Refused++
		line := fmt.Sprintf(format, args...)
		fmt.Fprint(out, line)
		if cfg.Err != nil {
			fmt.Fprint(cfg.Err, line)
		}
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 512), maxLine)
	n := 0
	for sc.Scan() {
		n++
		text := sc.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		l, err := ParseLine(text)
		if err != nil {
			// The line may hold a token; its content is never echoed.
			refused("REFUSED line=%d malformed: %s\n", n, err)
			continue
		}
		if seen[l.Card()] {
			refused("REFUSED line=%d duplicate %s: one attempt is launched once\n", n, l.Card())
			continue
		}
		seen[l.Card()] = true
		current := now()
		spent := current.Sub(began)
		if !current.Before(deadline) {
			refused("REFUSED line=%d timeout %s: batch at %dms is at or past the %dms launch budget\n",
				n, l.Card(), spent.Milliseconds(), budget.Milliseconds())
			continue
		}
		pid, ack, err := start(cfg.Wrapper, l, deadline)
		if err != nil {
			refused("REFUSED line=%d start %s: %s\n", n, l.Card(), oneline.Err(err))
			continue
		}
		if ack != "LAUNCHED" {
			refused("REFUSED line=%d wrapper %s: %s\n", n, l.Card(), oneline.Escape(ack))
			continue
		}
		res.Started++
		fmt.Fprintf(out, "LAUNCHED %s pid=%d\n", l.Card(), pid)
	}
	// Measured after the loop, so it includes the last line's own
	// startDetached: a per-line check alone cannot catch a slow final
	// start that pushes the whole batch past budget.
	spent := now().Sub(began)
	res.Overran = spent > budget
	fmt.Fprintf(out, "LAUNCH started=%d refused=%d ms=%d over=%t\n", res.Started, res.Refused, spent.Milliseconds(), res.Overran)
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("card launch: stdin after line %d: %w", n, err)
	}
	return res, nil
}

func checkWrapper(path string) error {
	if path == "" {
		return errors.New("card launch: wrapper MISSING: no path configured")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("card launch: wrapper %s MISSING: %s", oneline.Escape(path), oneline.Err(err))
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("card launch: wrapper %s is not an executable file", oneline.Escape(path))
	}
	return nil
}
