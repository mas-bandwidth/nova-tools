package sprintline

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseOpenFile reads an --open file. A non-empty line is a TASK line or a
// comment. Anything else is a refusal: a status fraction in this file would
// otherwise be skipped, and skipping is how a hand number sneaks back in.
func ParseOpenFile(text string) ([]Task, error) {
	return parseTasks(text, false)
}

// ParseStatus reads `nova-pulse sprint status --verbose` stdout. The producer
// prints C/O/W rows, not TASK lines:
//
//	Open <id> <ref> owner=<owner> route=<route> est=<~duration> kind=<kind> depends=<id,id>
//
// Working and Closed use the same shape. est is `~45m`, `~2h`, or `~1.5h`.
// kind is what SUGGEST charges. depends is a comma list of task ids, or `-`
// when the task waits on nothing. The fraction, the C= O= W= line, the wall,
// the lanes and the splittable note are not tasks. A TASK line is still
// accepted. Zero rows is not an error here; the caller refuses that, because
// a status with no open task is not an empty sprint. A row with no kind= or
// no depends= is a refusal: charging the printed estimate is how the
// calibrated wall was skipped.
func ParseStatus(text string) ([]Task, error) {
	return parseTasks(text, true)
}

func parseTasks(text string, allowOther bool) ([]Task, error) {
	var out []Task
	seen := map[string]bool{}
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var (
			t   Task
			err error
		)
		switch {
		case strings.HasPrefix(line, "TASK "):
			t, err = taskFrom(strings.Fields(line)[1:])
		case allowOther && isStatusRow(line):
			t, err = taskFromStatusRow(line)
		case allowOther:
			continue
		default:
			return nil, fmt.Errorf("line %d is not a TASK line (%q); an open file is id, kind, owner, est, depends, paths", n+1, line)
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("line %d: task %s is listed twice", n+1, t.ID)
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	return out, nil
}

func taskFrom(fields []string) (Task, error) {
	kv := map[string]string{}
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return Task{}, fmt.Errorf("field %q is not key=value", f)
		}
		if _, dup := kv[k]; dup {
			return Task{}, fmt.Errorf("field %s is repeated", k)
		}
		switch k {
		case "id", "kind", "owner", "state", "est", "depends", "paths":
			kv[k] = v
		default:
			return Task{}, fmt.Errorf("unknown field %s; a TASK line is id, kind, owner, state, est, depends, paths", k)
		}
	}
	for _, req := range []string{"id", "kind", "est"} {
		if _, ok := kv[req]; !ok {
			return Task{}, fmt.Errorf("TASK wants %s=; refusing to guess", req)
		}
	}
	est, err := strconv.Atoi(kv["est"])
	if err != nil || est < 0 {
		return Task{}, fmt.Errorf("est=%s is not a non-negative integer", kv["est"])
	}
	state := kv["state"]
	if state == "" {
		state = "open"
	}
	switch state {
	case "open", "working", "closed":
	default:
		return Task{}, fmt.Errorf("state=%s is not open, working, or closed", state)
	}
	if kv["id"] == "" || kv["kind"] == "" {
		return Task{}, fmt.Errorf("TASK wants a non-empty id and kind")
	}
	return Task{
		ID:         kv["id"],
		Kind:       kv["kind"],
		Owner:      kv["owner"],
		State:      state,
		EstMinutes: est,
		DependsOn:  splitList(kv["depends"]),
		Paths:      splitList(kv["paths"]),
	}, nil
}

// isStatusRow reports a C/O/W row from sprint status --verbose. The producer
// title-cases the state: Open, Working, Closed.
func isStatusRow(line string) bool {
	word, _, _ := strings.Cut(line, " ")
	switch word {
	case "Open", "Working", "Closed", "open", "working", "closed":
		return true
	default:
		return false
	}
}

func statusState(word string) (string, bool) {
	switch word {
	case "Open", "open":
		return "open", true
	case "Working", "working":
		return "working", true
	case "Closed", "closed":
		return "closed", true
	default:
		return "", false
	}
}

// taskFromStatusRow reads one producer row. The ref between the id and the
// key=value fields is not kept. owner=- and route=- are empty, which is how
// the producer prints a blank. est is the rendered duration, not an integer.
func taskFromStatusRow(line string) (Task, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return Task{}, fmt.Errorf("status row %q wants a state and an id", line)
	}
	state, ok := statusState(fields[0])
	if !ok {
		return Task{}, fmt.Errorf("status row %q does not start with Open, Working, or Closed", line)
	}
	id := fields[1]
	if id == "" || strings.Contains(id, "=") {
		return Task{}, fmt.Errorf("status row %q wants an id", line)
	}
	kv := map[string]string{}
	sawKV := false
	for _, f := range fields[2:] {
		k, v, isKV := strings.Cut(f, "=")
		if !isKV {
			if sawKV {
				return Task{}, fmt.Errorf("status row %q has %q after its fields", line, f)
			}
			continue
		}
		if k == "" {
			return Task{}, fmt.Errorf("field %q is not key=value", f)
		}
		if _, dup := kv[k]; dup {
			return Task{}, fmt.Errorf("field %s is repeated", k)
		}
		switch k {
		case "owner", "route", "est", "kind", "depends", "paths":
			kv[k] = v
		default:
			return Task{}, fmt.Errorf("unknown field %s on a status row; a row is state, id, ref, owner, route, est, kind, depends", k)
		}
		sawKV = true
	}
	if _, ok := kv["est"]; !ok {
		return Task{}, fmt.Errorf("status row wants est=, the estimate the sprint printed")
	}
	kind, ok := kv["kind"]
	if !ok {
		return Task{}, fmt.Errorf("status row wants kind=, the task kind SUGGEST replaces")
	}
	kind = undash(kind)
	if kind == "" {
		return Task{}, fmt.Errorf("status row wants a kind; kind=- is not a kind the calibration can charge")
	}
	depField, ok := kv["depends"]
	if !ok {
		return Task{}, fmt.Errorf("status row wants depends=, the ids it waits on, or depends=-")
	}
	est, err := parseSprintMinutes(kv["est"])
	if err != nil {
		return Task{}, err
	}
	return Task{
		ID:         id,
		Kind:       kind,
		Owner:      undash(kv["owner"]),
		Route:      undash(kv["route"]),
		State:      state,
		EstMinutes: est,
		DependsOn:  splitList(undash(depField)),
		Paths:      splitList(kv["paths"]),
	}, nil
}

// parseSprintMinutes reads est as the producer prints it (`~45m`, `~2h`,
// `~1.5h`) or as a bare non-negative integer of minutes. One decimal hour is
// six minutes per tenth, which is what `~1.5h` means and what it round-trips
// to. Anything finer is not what the sprint line prints, and is refused.
func parseSprintMinutes(s string) (int, error) {
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("est=%s is not a non-negative integer", s)
		}
		return n, nil
	}
	body := strings.TrimPrefix(s, "~")
	if body == s || body == "" {
		return 0, fmt.Errorf("est=%s is not ~Nh, ~Nm, or an integer number of minutes", s)
	}
	if strings.HasSuffix(body, "m") {
		n, err := strconv.Atoi(strings.TrimSuffix(body, "m"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("est=%s is not a whole number of minutes", s)
		}
		return n, nil
	}
	if !strings.HasSuffix(body, "h") {
		return 0, fmt.Errorf("est=%s is not ~Nh, ~Nm, or an integer number of minutes", s)
	}
	h := strings.TrimSuffix(body, "h")
	whole, frac, dotted := strings.Cut(h, ".")
	w, err := strconv.Atoi(whole)
	if err != nil || w < 0 || whole == "" || whole == "-" {
		return 0, fmt.Errorf("est=%s is not a number of hours", s)
	}
	if !dotted {
		return w * 60, nil
	}
	if len(frac) != 1 || frac[0] < '0' || frac[0] > '9' {
		return 0, fmt.Errorf("est=%s wants one decimal hour, the way the sprint line prints it", s)
	}
	return w*60 + int(frac[0]-'0')*6, nil
}

func undash(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
