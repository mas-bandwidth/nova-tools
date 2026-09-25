// Package table parses and renders the nova-sprint table from the single
// consistent snapshot returned by the ns_snapshot Redis Function. One call,
// one server instant: section 6 of #2756.
//
// This file holds the machine, ci, clock and process lines of section 6
// (#3045). They ride the same ns_snapshot instant as the bench and friend
// rows: ReadLines passes the literal `lines` argument and the function answers
// every line from the one server TIME it read, so a machine's desired and
// living sums and its clock's oldest age are one instant, never a pipeline of
// separate reads.
package table

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Machine is one machine line (2.4.5): ceiling, the desired sum and the living
// sum of every bench and friend on the machine, and its load1.
type Machine struct {
	Name    string
	Ceiling int64
	Desired int64
	Living  int64
	Load1   string
}

// CILine is the single ci line (6.4): one count per ci card state and the age
// of the oldest ci card.
type CILine struct {
	Cut       int64
	Running   int64
	Pending   int64
	OK        int64
	Fail      int64
	Flaky     int64
	Missing   int64
	Blocked   int64
	OldestAge int64
}

// ClockLine is one pipeline clock line (section 1): the oldest item age of the
// sprint's clock and whether it breached.
type ClockLine struct {
	Sprint string
	Age    int64
	Breach bool
}

// ProcLine is one process line of section 6: reconciler, router (the
// consumers), backpressure and one harvest worker per bench. State is up,
// down, or ? when a pass exists but is stale.
type ProcLine struct {
	Name  string
	State string
	Age   int64
	Why   string
	Red   bool
}

// Lines is the machine, ci, clock and process section of one snapshot.
type Lines struct {
	Time     int64
	Machines []Machine
	Ci       CILine
	Clocks   []ClockLine
	Procs    []ProcLine
	Errors   []string
}

const (
	machineFields  = 5
	ciFields       = 9
	clockFields    = 3
	procLineFields = 5
)

// ReadLines takes one consistent snapshot of the machine, ci, clock and
// process lines. It makes one FCALL_RO; callers must load the nova_sprint
// function library during deployment.
func ReadLines(ctx context.Context, client *redis.Client) (*Lines, error) {
	return ReadLinesNamed(ctx, client, "")
}

// ReadLinesNamed includes a named sprint, still exactly one FCALL_RO.
func ReadLinesNamed(ctx context.Context, client *redis.Client, sprint string) (*Lines, error) {
	raw, err := client.FCallRo(ctx, "ns_snapshot", []string{}, sprint, "lines").Result()
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("snapshot: unexpected reply %T", raw)
	}
	return ParseLines(list)
}

// ParseLines decodes the flat tagged list returned by ns_snapshot in lines
// mode.
func ParseLines(raw []any) (*Lines, error) {
	lines := &Lines{Ci: CILine{OldestAge: -1}}
	i := 0
	for i < len(raw) {
		tag, err := token(raw, i)
		if err != nil {
			return nil, err
		}
		i++
		switch tag {
		case "time":
			v, err := token(raw, i)
			if err != nil {
				return nil, err
			}
			i++
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("snapshot: bad time %q", v)
			}
			lines.Time = n
		case "error":
			v, err := token(raw, i)
			if err != nil {
				return nil, err
			}
			i++
			lines.Errors = append(lines.Errors, v)
		case "machine":
			m, err := parseMachine(raw, i)
			if err != nil {
				return nil, err
			}
			i += machineFields
			lines.Machines = append(lines.Machines, m)
		case "ci":
			ci, err := parseCi(raw, i)
			if err != nil {
				return nil, err
			}
			i += ciFields
			lines.Ci = ci
		case "clock":
			c, err := parseClock(raw, i)
			if err != nil {
				return nil, err
			}
			i += clockFields
			lines.Clocks = append(lines.Clocks, c)
		case "proc":
			p, err := parseProcLine(raw, i)
			if err != nil {
				return nil, err
			}
			i += procLineFields
			lines.Procs = append(lines.Procs, p)
		default:
			return nil, fmt.Errorf("snapshot: unknown record %q", tag)
		}
	}
	return lines, nil
}

func parseMachine(raw []any, i int) (Machine, error) {
	vals := make([]string, machineFields)
	for k := 0; k < machineFields; k++ {
		v, err := token(raw, i+k)
		if err != nil {
			return Machine{}, err
		}
		vals[k] = v
	}
	m := Machine{Name: vals[0], Load1: vals[4]}
	for k, dst := range []*int64{&m.Ceiling, &m.Desired, &m.Living} {
		n, err := count(vals[1+k])
		if err != nil {
			return Machine{}, fmt.Errorf("snapshot machine %s field %d: %w", m.Name, 1+k, err)
		}
		*dst = n
	}
	return m, nil
}

func parseCi(raw []any, i int) (CILine, error) {
	vals := make([]string, ciFields)
	for k := 0; k < ciFields; k++ {
		v, err := token(raw, i+k)
		if err != nil {
			return CILine{}, err
		}
		vals[k] = v
	}
	ci := CILine{OldestAge: -1}
	for k, dst := range []*int64{&ci.Cut, &ci.Running, &ci.Pending, &ci.OK, &ci.Fail, &ci.Flaky, &ci.Missing, &ci.Blocked} {
		n, err := count(vals[k])
		if err != nil {
			return CILine{}, fmt.Errorf("snapshot ci field %d: %w", k, err)
		}
		*dst = n
	}
	age, err := strconv.ParseInt(vals[8], 10, 64)
	if err != nil {
		return CILine{}, fmt.Errorf("snapshot ci oldest %q: %w", vals[8], err)
	}
	ci.OldestAge = age
	return ci, nil
}

func parseClock(raw []any, i int) (ClockLine, error) {
	vals := make([]string, clockFields)
	for k := 0; k < clockFields; k++ {
		v, err := token(raw, i+k)
		if err != nil {
			return ClockLine{}, err
		}
		vals[k] = v
	}
	age, err := strconv.ParseInt(vals[1], 10, 64)
	if err != nil {
		return ClockLine{}, fmt.Errorf("snapshot clock %s age %q: %w", vals[0], vals[1], err)
	}
	return ClockLine{Sprint: vals[0], Age: age, Breach: vals[2] == "1"}, nil
}

func parseProcLine(raw []any, i int) (ProcLine, error) {
	vals := make([]string, procLineFields)
	for k := 0; k < procLineFields; k++ {
		v, err := token(raw, i+k)
		if err != nil {
			return ProcLine{}, err
		}
		vals[k] = v
	}
	age, err := strconv.ParseInt(vals[2], 10, 64)
	if err != nil {
		return ProcLine{}, fmt.Errorf("snapshot process %s age %q: %w", vals[0], vals[2], err)
	}
	return ProcLine{Name: vals[0], State: vals[1], Age: age, Why: vals[3], Red: vals[4] == "1"}, nil
}

// ageText renders an age in seconds, or ? when unknown (negative).
func ageText(age int64) string {
	if age < 0 {
		return "?"
	}
	return strconv.FormatInt(age, 10) + "s"
}

// Render prints the machine, ci, clock and process lines. A red line is
// prefixed with RED, the same marker the base table uses for its error lines.
func (l *Lines) Render() string {
	var b strings.Builder
	for _, m := range l.Machines {
		b.WriteString("machine:" + m.Name)
		b.WriteString(" | " + strconv.FormatInt(m.Ceiling, 10))
		b.WriteString(" | " + strconv.FormatInt(m.Desired, 10))
		b.WriteString(" | " + strconv.FormatInt(m.Living, 10))
		b.WriteString(" | " + m.Load1)
		b.WriteByte('\n')
	}
	b.WriteString(l.Ci.render())
	for _, c := range l.Clocks {
		if c.Breach {
			b.WriteString("RED ")
		}
		b.WriteString("clock " + c.Sprint + " oldest=" + ageText(c.Age))
		if c.Breach {
			b.WriteString(" breach")
		}
		b.WriteByte('\n')
	}
	for _, p := range l.Procs {
		if p.Red {
			b.WriteString("RED ")
		}
		b.WriteString(procLineText(p))
		b.WriteByte('\n')
	}
	for _, e := range l.Errors {
		b.WriteString("RED " + e)
		b.WriteByte('\n')
	}
	return b.String()
}

func (c CILine) render() string {
	return fmt.Sprintf("ci: cut %d, running %d, PENDING %d, OK %d, FAIL %d, FLAKY %d, MISSING-at-head %d, blocked-no-alternate-bench %d, oldest %s\n",
		c.Cut, c.Running, c.Pending, c.OK, c.Fail, c.Flaky, c.Missing, c.Blocked, ageText(c.OldestAge))
}

func procLineText(p ProcLine) string {
	line := fmt.Sprintf("proc %s %s age=%s", p.Name, p.State, ageText(p.Age))
	if p.Why != "" {
		line += " why=" + p.Why
	}
	return line
}
