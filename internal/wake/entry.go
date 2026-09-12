package wake

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// An entry is something on a forge with a state and a set of hosted checks:
// --entry mas-bandwidth/schema#942. It is named --entry and not --pr because
// the repository is part of the name -- the prototype took --prs 942,951
// against one --repo, which makes a second repository a second invocation and a
// typo in one number a watch on somebody else's work -- and because this tool
// has no model of what the number means beyond "the thing gh will tell me the
// state of".

// EntryBatch is how many gh calls may be outstanding at once. Entries are
// polled in batches with one gh process per entry per poll and no pagination.
const EntryBatch = 8

// Entries is the entry source.
type Entries struct {
	Names   []string // <repo>#<n>, in the order the caller gave them
	Every_  time.Duration
	Timeout time.Duration
	Final   bool // --final-only
}

func (e *Entries) Name() string         { return "entries" }
func (e *Entries) Every() time.Duration { return e.Every_ }

// Poll runs gh once per entry, at most EntryBatch outstanding, and turns each
// answer into a state value. An entry that cannot be read has the state value
// unreadable:<reason>, which is a state value like any other: the first
// sighting is a change and the same reason on the next poll does not re-wake.
func (e *Entries) Poll(ctx context.Context, now time.Time) (Result, error) {
	type answer struct {
		i     int
		value string
		bad   bool
	}
	out := make([]answer, len(e.Names))
	sem := make(chan struct{}, EntryBatch)
	done := make(chan answer, len(e.Names))
	for i, name := range e.Names {
		go func(i int, name string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			value, err := e.one(ctx, name)
			if err != nil {
				done <- answer{i: i, value: Compose("unreadable", err.Error()), bad: true}
				return
			}
			done <- answer{i: i, value: value}
		}(i, name)
	}
	for range e.Names {
		a := <-done
		out[a.i] = a
	}
	res := Result{}
	bad := 0
	for i, a := range out {
		if a.bad {
			bad++
		}
		res.Items = append(res.Items, Item{Kind: KindEntry, Key: "entry:" + e.Names[i], Value: a.value})
	}
	// A source fails when EVERY entry is unreadable in one poll. One unreadable
	// entry beside four readable ones is news, not a broken source.
	if len(e.Names) > 0 && bad == len(e.Names) {
		_, reason := Unreadable(out[0].value)
		return res, fmt.Errorf("every entry is unreadable: %s", reason)
	}
	return res, nil
}

// one is a single gh call and the bucket rule over what it answers.
func (e *Entries) one(ctx context.Context, name string) (string, error) {
	repo, number, err := SplitEntry(name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", number,
		"--repo", repo, "--json", "state,statusCheckRollup")
	raw, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("gh timed out after %s", Dur(e.Timeout))
		}
		var ee *exec.ExitError
		detail := err.Error()
		if ok := asExit(err, &ee); ok && len(ee.Stderr) > 0 {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("gh: %s", oneLineOf(detail))
	}
	var answer struct {
		State             string `json:"state"`
		StatusCheckRollup []struct {
			TypeName   string `json:"__typename"`
			Name       string `json:"name"`
			Context    string `json:"context"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return "", fmt.Errorf("gh answered something this tool cannot parse: %s", oneLineOf(err.Error()))
	}
	var fail, pending, pass int
	var failing []string
	for _, c := range answer.StatusCheckRollup {
		name := c.Name
		if name == "" {
			name = c.Context
		}
		if name == "" {
			// A nameless check is still a count.
			name = "?"
		}
		switch bucket(c.TypeName, c.Status, c.Conclusion, c.State) {
		case "pending":
			pending++
		case "pass":
			pass++
		default:
			fail++
			failing = append(failing, name)
		}
	}
	sort.Strings(failing)
	return Compose(strings.ToUpper(answer.State), strconv.Itoa(fail), strconv.Itoa(pending),
		strconv.Itoa(pass), strings.Join(failing, ",")), nil
}

// bucket is the rule, stated once: a hosted check that has not completed is
// pending; one that completed with success, neutral or skipped is pass;
// anything else is fail. A non-check status context is pending while pending or
// expected, pass on success, fail otherwise.
func bucket(typeName, status, conclusion, state string) string {
	if strings.EqualFold(typeName, "StatusContext") || (status == "" && conclusion == "" && state != "") {
		switch strings.ToUpper(state) {
		case "PENDING", "EXPECTED":
			return "pending"
		case "SUCCESS":
			return "pass"
		default:
			return "fail"
		}
	}
	if !strings.EqualFold(status, "COMPLETED") {
		return "pending"
	}
	switch strings.ToUpper(conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return "pass"
	default:
		return "fail"
	}
}

// IsFinal is --final-only's predicate, a pure function over the state value.
//
// An entry is FINAL when it is no longer OPEN -- MERGED or CLOSED -- or when
// pending=0 and it has at least one check. Green and red are both final,
// because the window's next action differs between them and is needed in both
// cases, and a watcher that woke only on green would sleep through every
// failure. An entry with NO checks at all is never final by the count rule:
// pending=0 pass=0 fail=0 is the state of an entry whose checks have not been
// created yet, and calling that green is the one arithmetic mistake here that
// would merge something red.
func IsFinal(value string) bool {
	p := Decompose(value)
	if len(p) == 2 && p[0] == "unreadable" {
		return false
	}
	p = fields(p, 5)
	if p[0] != "" && !strings.EqualFold(p[0], "OPEN") {
		return true
	}
	fail, _ := strconv.Atoi(p[1])
	pending, _ := strconv.Atoi(p[2])
	pass, _ := strconv.Atoi(p[3])
	return pending == 0 && fail+pass > 0
}

// Unreadable reports whether a value is the unreadable one, and its reason. An
// unreadable value is an ERROR and never a count: it wakes under --final-only
// exactly as without it, because a flag that asks for fewer wakes about
// arithmetic is not a flag that asks to sleep through a source that cannot be
// read.
func Unreadable(value string) (bool, string) {
	p := Decompose(value)
	if len(p) == 2 && p[0] == "unreadable" {
		return true, p[1]
	}
	return false, ""
}

// SplitEntry reads <repo>#<n> and says what the flag WANTS when it cannot.
func SplitEntry(name string) (repo, number string, err error) {
	repo, number, ok := strings.Cut(name, "#")
	if !ok || repo == "" || number == "" || strings.Count(repo, "/") != 1 {
		return "", "", fmt.Errorf("an entry is <owner>/<repo>#<number>, the repository included, as in mas-bandwidth/schema#942")
	}
	if _, err := strconv.Atoi(number); err != nil {
		return "", "", fmt.Errorf("an entry is <owner>/<repo>#<number>, the repository included, as in mas-bandwidth/schema#942")
	}
	return repo, number, nil
}

// oneLineOf folds a program's multi-line complaint into the reason slot of one
// event line. The escape makes it one line; this makes it a READABLE one.
func oneLineOf(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func asExit(err error, out **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*out = ee
	}
	return ok
}
