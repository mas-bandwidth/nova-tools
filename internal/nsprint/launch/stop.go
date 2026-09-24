package launch

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// StopLine is bench-reset's token-free card-stop input.
type StopLine struct {
	Sprint, Label string
	Attempt       int
}

func (l StopLine) Card() string            { return fmt.Sprintf("%s/%s/%d", l.Sprint, l.Label, l.Attempt) }
func (l StopLine) CommandIdentity() string { return WrapperName + " " + l.Card() }

func ParseStopLine(s string) (StopLine, error) {
	f := strings.Split(s, " ")
	if len(f) != 3 {
		return StopLine{}, errors.New("want <sprint> <label> <attempt>")
	}
	if !sprintRE.MatchString(f[0]) {
		return StopLine{}, errors.New("invalid sprint")
	}
	if !labelRE.MatchString(f[1]) {
		return StopLine{}, errors.New("invalid label")
	}
	a, err := strconv.Atoi(f[2])
	if err != nil || a < 1 || strconv.Itoa(a) != f[2] {
		return StopLine{}, errors.New("invalid attempt")
	}
	return StopLine{Sprint: f[0], Label: f[1], Attempt: a}, nil
}

type GroupManager interface {
	Find(context.Context, string) ([]int, error)
	Signal(context.Context, int, string) error
	Alive(context.Context, int) (bool, error)
}
type Waiter func(context.Context, time.Duration) error

type StopSummary struct{ Stopped, Gone, Alive int }

// Stop signals only process groups whose complete command identity is
// `nova-card <S>/<label>/<attempt>`. It is injected in tests; no test signals a
// host process.
func Stop(ctx context.Context, in io.Reader, out io.Writer, grace time.Duration, groups GroupManager, wait Waiter) (StopSummary, error) {
	var sum StopSummary
	if groups == nil {
		return sum, errors.New("card stop: process groups are required")
	}
	if grace <= 0 {
		grace = DefaultStopGrace
	}
	if wait == nil {
		wait = waitContext
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 256), maxLine)
	seen := map[string]bool{}
	var lines []StopLine
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		line, err := ParseStopLine(sc.Text())
		if err != nil {
			return sum, fmt.Errorf("card stop: line %d: %w", len(lines)+1, err)
		}
		if seen[line.Card()] {
			return sum, fmt.Errorf("card stop: duplicate %s", line.Card())
		}
		seen[line.Card()] = true
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		return sum, fmt.Errorf("card stop: stdin: %w", err)
	}
	type found struct {
		line  StopLine
		pgids []int
	}
	foundLines := make([]found, len(lines))
	any := false
	// TERM every exact group before waiting, so a batch pays one grace period,
	// never one grace per card.
	for i, line := range lines {
		pgids, err := groups.Find(ctx, line.CommandIdentity())
		if err != nil {
			return sum, err
		}
		foundLines[i] = found{line, pgids}
		for _, pgid := range pgids {
			any = true
			if err := groups.Signal(ctx, pgid, "TERM"); err != nil {
				return sum, err
			}
		}
	}
	if any {
		if err := wait(ctx, grace); err != nil {
			return sum, err
		}
	}
	for _, f := range foundLines {
		if len(f.pgids) == 0 {
			fmt.Fprintf(out, "GONE %s\n", f.line.Card())
			sum.Gone++
			continue
		}
		live := false
		for _, pgid := range f.pgids {
			yes, err := groups.Alive(ctx, pgid)
			if err != nil {
				return sum, err
			}
			if yes {
				if err := groups.Signal(ctx, pgid, "KILL"); err != nil {
					return sum, err
				}
				yes, err = groups.Alive(ctx, pgid)
				if err != nil {
					return sum, err
				}
				live = live || yes
			}
		}
		if live {
			fmt.Fprintf(out, "ALIVE %s\n", f.line.Card())
			sum.Alive++
		} else {
			fmt.Fprintf(out, "STOPPED %s\n", f.line.Card())
			sum.Stopped++
		}
	}
	return sum, nil
}

const DefaultStopGrace = 5 * time.Second

func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// OSGroupManager reads ps once per identity and uses kill on the negative pgid,
// which targets the wrapper's setsid-created process group.
type OSGroupManager struct{}

func (OSGroupManager) Find(ctx context.Context, identity string) ([]int, error) {
	cmd := exec.CommandContext(ctx, "ps", "-eo", "pgid=,command=")
	body, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("card stop: ps: %w", err)
	}
	var out []int
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			continue
		}
		pg, err := strconv.Atoi(strings.TrimSpace(line[:sp]))
		if err != nil {
			continue
		}
		if strings.TrimSpace(line[sp+1:]) == identity {
			out = append(out, pg)
		}
	}
	return out, sc.Err()
}
func (OSGroupManager) Signal(ctx context.Context, pgid int, sig string) error {
	if pgid <= 0 {
		return errors.New("card stop: invalid process group")
	}
	if err := exec.CommandContext(ctx, "kill", "-"+sig, "-"+strconv.Itoa(pgid)).Run(); err != nil {
		return fmt.Errorf("card stop: %s group %d: %w", sig, pgid, err)
	}
	return nil
}
func (OSGroupManager) Alive(ctx context.Context, pgid int) (bool, error) {
	err := exec.CommandContext(ctx, "kill", "-0", "-"+strconv.Itoa(pgid)).Run()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false, nil
	}
	return false, err
}
