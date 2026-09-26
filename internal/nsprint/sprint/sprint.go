// Package sprint opens, closes and reads a sprint in the nova-sprint store
// (#2939).
//
// A sprint is open when its hash s:<S> has status=open and <S> is a member of
// the sprints set. The claim function (task_claim.lua) and the deal pass read
// the status; the table reads the set. Open is two Redis Functions around the
// pushes (fn/lua/sprint.lua): ns_sprint_begin writes status=opening with the
// source's sha, and ns_sprint_open stamps opened_at once, sets status=open,
// adds the units and places <S> in sprints and sprint:order (ZADD NX, score =
// open time in ms). Both re-make the existing-sprint decision atomically:
// a closed sprint is never reopened, and an open or opening sprint is resumed
// only from the same source. Close is one Redis Function, ns_sprint_close,
// which refuses a name with no s:<S> and a sprint already closed (#3571).
package sprint

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// Status is the one-word state a verb prints.
type Status string

const (
	Open    Status = "open"
	Opening Status = "opening"
	Closed  Status = "closed"
	Absent  Status = "absent"
)

// Function names registered by internal/nsprint/fn/lua/sprint.lua.
const (
	FunctionBegin  = "ns_sprint_begin"
	FunctionOpen   = "ns_sprint_open"
	FunctionStatus = "ns_sprint_status"
	FunctionClose  = "ns_sprint_close"
)

// nameRE is the card identity's sprint shape (card/identity.go).
var nameRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// ValidName reports whether s can name a sprint.
func ValidName(s string) bool { return nameRE.MatchString(s) }

func check(st *store.Store, name string) error {
	if st == nil || st.Client() == nil {
		return errors.New("sprint: store is required")
	}
	if !ValidName(name) {
		return fmt.Errorf("sprint name %q is not [a-z0-9-]{1,40}", name)
	}
	return nil
}

// Existing is the existing-sprint read: s:<S> status and from_sha, and which
// owners are members of friends.
type Existing struct {
	Status  string
	FromSHA string
	Member  map[string]bool
	// Friends is SCARD friends: 0 means no friend is registered on this
	// store yet, which the owner refusal names (#3570).
	Friends int64
	// RegistryRefused is the NOPERM line when the seat's ACL refuses the
	// friends registry read; Member is then empty and Friends is 0, and open
	// refuses naming the ACL rather than calling every owner unregistered.
	RegistryRefused string
}

// ReadExisting is one pipeline, one round trip: HMGET s:<S> status from_sha,
// SCARD friends, and one SISMEMBER friends per owner. SISMEMBER, not
// SMISMEMBER: every seat's ACL (the ns-* actors' command set) grants
// SISMEMBER and SCARD, and none grants SMISMEMBER (#3570). A NOPERM on the
// registry reads is reported in RegistryRefused, never as an error.
func ReadExisting(ctx context.Context, st *store.Store, name string, owners []string) (Existing, error) {
	if err := check(st, name); err != nil {
		return Existing{}, err
	}
	pipe := st.Client().Pipeline()
	hm := pipe.HMGet(ctx, "s:"+name, "status", "from_sha")
	var (
		count *redis.IntCmd
		mem   []*redis.BoolCmd
	)
	if len(owners) > 0 {
		count = pipe.SCard(ctx, "friends")
		mem = make([]*redis.BoolCmd, len(owners))
		for i, o := range owners {
			mem[i] = pipe.SIsMember(ctx, "friends", o)
		}
	}
	_, _ = pipe.Exec(ctx) // each command's error is read below
	if err := hm.Err(); err != nil {
		return Existing{}, fmt.Errorf("sprint open %s: existing-sprint read: %w", name, err)
	}
	vals := hm.Val()
	e := Existing{Member: map[string]bool{}}
	if len(vals) == 2 {
		e.Status, _ = vals[0].(string)
		e.FromSHA, _ = vals[1].(string)
	}
	if count == nil {
		return e, nil
	}
	registry := append([]redis.Cmder{count}, cmders(mem)...)
	for _, c := range registry {
		err := c.Err()
		if err == nil {
			continue
		}
		if refused := nopermLine(err); refused != "" {
			e.RegistryRefused = refused
			e.Member = map[string]bool{}
			return e, nil
		}
		return Existing{}, fmt.Errorf("sprint open %s: friends registry read: %w", name, err)
	}
	e.Friends = count.Val()
	for i, c := range mem {
		e.Member[owners[i]] = c.Val()
	}
	return e, nil
}

func cmders(cmds []*redis.BoolCmd) []redis.Cmder {
	out := make([]redis.Cmder, len(cmds))
	for i, c := range cmds {
		out[i] = c
	}
	return out
}

// nopermLine is the first line of an ACL refusal, or "" for any other error.
func nopermLine(err error) string {
	msg := err.Error()
	if !strings.HasPrefix(msg, "NOPERM") {
		return ""
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return strings.TrimSpace(msg)
}

// Sha8 is the first eight characters of a sha, or none when it is empty.
func Sha8(sha string) string {
	if sha == "" {
		return "none"
	}
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// Refusal is the existing-sprint rule over a read: "" for a first open or a
// resume, otherwise the REFUSED line.
func (e Existing) Refusal(name, fromSHA string) string {
	switch {
	case e.Status == "":
		return ""
	case e.Status == string(Closed):
		return "REFUSED " + name + " closed"
	case e.FromSHA != fromSHA:
		return fmt.Sprintf("REFUSED %s from_sha %s != %s", name, Sha8(e.FromSHA), Sha8(fromSHA))
	}
	return ""
}

// refusalLine turns a {REFUSED, why[, old8]} function reply into the line.
func refusalLine(name, fromSHA string, values []any) string {
	why := ""
	if len(values) > 1 {
		why = fmt.Sprint(values[1])
	}
	if why == "from_sha" && len(values) > 2 {
		return fmt.Sprintf("REFUSED %s from_sha %s != %s", name, fmt.Sprint(values[2]), Sha8(fromSHA))
	}
	return "REFUSED " + name + " " + why
}

func call(ctx context.Context, st *store.Store, fn string, args ...any) ([]any, string, error) {
	reply, err := st.Client().FCall(ctx, fn, nil, args...).Result()
	if err != nil {
		return nil, "", err
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return nil, "", fmt.Errorf("%s: unexpected reply %T", fn, reply)
	}
	word, _ := values[0].(string)
	return values, word, nil
}

// Begin is ns_sprint_begin: a first open writes status=opening with its
// source; a resume writes nothing. refused is the REFUSED line, or "".
func Begin(ctx context.Context, st *store.Store, name, from, fromSHA string, now time.Time) (refused string, err error) {
	if err := check(st, name); err != nil {
		return "", err
	}
	values, word, err := call(ctx, st, FunctionBegin, name, from, fromSHA, now.UnixMilli())
	if err != nil {
		return "", fmt.Errorf("sprint open %s: begin: %w", name, err)
	}
	switch word {
	case "BEGUN", "RESUME":
		return "", nil
	case "REFUSED":
		return refusalLine(name, fromSHA, values), nil
	}
	return "", fmt.Errorf("sprint open %s: begin: unexpected status %q", name, word)
}

// Finish is ns_sprint_open: it re-checks closed and from_sha, stamps
// opened_at once (HSETNX), sets status=open, adds units to s:<S>:units and
// places <S> in sprints and sprint:order. refused is the REFUSED line, or "".
func Finish(ctx context.Context, st *store.Store, name, from, fromSHA string, now time.Time, units []string) (refused string, err error) {
	if err := check(st, name); err != nil {
		return "", err
	}
	args := []any{name, from, fromSHA, now.UnixMilli()}
	for _, u := range units {
		args = append(args, u)
	}
	values, word, err := call(ctx, st, FunctionOpen, args...)
	if err != nil {
		return "", fmt.Errorf("sprint open %s: %w", name, err)
	}
	switch word {
	case "OPENED", "RESUMED":
		return "", nil
	case "REFUSED":
		return refusalLine(name, fromSHA, values), nil
	}
	return "", fmt.Errorf("sprint open %s: unexpected status %q", name, word)
}

// SetClosed is ns_sprint_close, one function call: an open or opening
// sprint gets status=closed, closed_at stamped once and <S> removed from
// sprints. A name with no s:<S> status and a sprint past open (closed or
// folded) write nothing and come back as refused, the REFUSED line naming
// the remedy (#3571); the verb exits 1 on it. sprint:order keeps its entry: every reader
// of the order also checks status=open. There is no reopen.
//
// The same call retires every card of the sprint that is not done (#3925):
// each moves to done/fail through the one card move and its bench leases go,
// so no closed sprint keeps a card in a table set. retired is how many moved.
func SetClosed(ctx context.Context, st *store.Store, name string, now time.Time) (s Status, retired int, refused string, err error) {
	if err := check(st, name); err != nil {
		return "", 0, "", err
	}
	values, word, err := call(ctx, st, FunctionClose, name, now.UnixMilli())
	if err != nil {
		return "", 0, "", fmt.Errorf("sprint close %s: %w", name, err)
	}
	switch word {
	case "CLOSED":
		if len(values) > 1 {
			retired, _ = strconv.Atoi(fmt.Sprint(values[1]))
		}
		return Closed, retired, "", nil
	case "ABSENT":
		return Absent, 0, "REFUSED " + name + " no such sprint (no s:" + name + " status); nothing closed; remedy: sprint status lists the open sprints", nil
	case "ALREADY":
		was, at := string(Closed), "?"
		if len(values) > 2 {
			was, at = fmt.Sprint(values[1]), fmt.Sprint(values[2])
		}
		if at == "" {
			at = "?"
		}
		return Status(was), 0, "REFUSED " + name + " already " + was + " (closed_at " + at + "); nothing written; there is no reopen", nil
	}
	return "", 0, "", fmt.Errorf("sprint close %s: unexpected status %q", name, word)
}

// Line is the one line close prints.
func Line(name string, s Status) string { return name + " status=" + string(s) }

// StatusLines is `sprint status`: the one count (ws.Counts, the numbers the
// table and ws counts print), one line, `<S> <status> <landed>/<total> done
// <z>%, left <l>, eta <HH:MM> ET`. An empty name is the open sprint (the last
// of sprint:order not closed, the table's rule); with none open it prints
// nothing. The ws index holds one sprint's streams at a time, so the counts
// are the index's whatever sprint is named; the name picks the status and
// its status word; the eta is measured from now (--now).
func StatusLines(ctx context.Context, st *store.Store, name string, now time.Time) ([]string, error) {
	if st == nil || st.Client() == nil {
		return nil, errors.New("sprint: store is required")
	}
	if name != "" && !ValidName(name) {
		return nil, fmt.Errorf("sprint name %q is not [a-z0-9-]{1,40}", name)
	}
	c, err := (&ws.CountsReader{Sprint: name, WithStatus: true}).Read(ctx, st.Client(), now)
	if err != nil {
		return nil, fmt.Errorf("sprint status: %w", err)
	}
	return StatusLine(c), nil
}

// StatusLine is the status line of one read: none when no sprint is named or
// open.
func StatusLine(c ws.SprintCounts) []string {
	if c.Sprint == "" {
		return nil
	}
	status := c.Status
	if status == "" {
		status = string(Absent)
	}
	return []string{c.Sprint + " " + status + " " + c.Header()}
}
