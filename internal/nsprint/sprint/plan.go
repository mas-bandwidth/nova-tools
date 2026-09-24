// Sprint plan (#2380 rev 4): one plan file, validated in full, applied in one
// atomic step by an authorised seat, and readable by any seat.
//
// Apply parses the file and validates every row against the store in two
// pipelined round trips (the registries, the plan's ceilings and the plan
// consumers' desired hashes; then every other consumer's desired hash and the
// ceilings of the machines a plan consumer leaves). Any finding prints one
// PLAN-REFUSED line <n>: <reason> per finding and writes nothing. A valid plan
// is one FCALL ns_sprint_plan (fn/lua/capacity.lua), which checks authority
// first, then every key, then writes; Go makes no authority check of its own.
//
// Show reads the plan record, the policy and every row's desired hash with
// plain reads (no FCALL), so a bench and a viewer seat can run it, and prints
// one DRIFT line for every row or policy key where the store differs from the
// plan.
package sprint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

const (
	// PlanHeader is line 1 of every v1 plan, exactly.
	PlanHeader = "#nova-sprint-plan v1"
	// PlanMaxBytes bounds a plan file.
	PlanMaxBytes = 64 << 10
	// PlanMaxSlots bounds one row's slots.
	PlanMaxSlots = 256
	// FunctionPlan is the Redis Function that applies a plan.
	FunctionPlan = "ns_sprint_plan"
	// AuthzKey names the right to apply a plan: a seat whose ACL may HSET it
	// may apply. It is never written.
	AuthzKey = "authz:sprint-plan"
)

// PolicyKeys are the four policy keys of schema v1, in show order. Each
// appears exactly once in a plan.
var PolicyKeys = []string{"backpressure_missing", "ci_reruns", "readers", "absent_after"}

// PlanRow is one bench or friend row.
type PlanRow struct {
	Line    int
	Kind    string
	Name    string
	Machine string
	Slots   int
}

// Plan is one parsed plan file.
type Plan struct {
	Body   []byte
	SHA    string // sha256 of Body, hex
	Policy map[string]string
	Rows   []PlanRow
	// policyLine is the line of each policy key, for findings.
	policyLine map[string]int
}

// Finding is one reason a plan is refused.
type Finding struct {
	Line   int
	Reason string
}

func (f Finding) String() string { return fmt.Sprintf("PLAN-REFUSED line %d: %s", f.Line, f.Reason) }

func planSHA(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// ParsePlan reads schema v1. It needs no store; every finding carries the line
// it is about, and a plan with any finding is never applied.
func ParsePlan(body []byte) (*Plan, []Finding) {
	p := &Plan{Body: body, SHA: planSHA(body), Policy: map[string]string{}, policyLine: map[string]int{}}
	if len(body) > PlanMaxBytes {
		return p, []Finding{{1, fmt.Sprintf("plan is %d bytes; the limit is %d", len(body), PlanMaxBytes)}}
	}
	text := strings.TrimSuffix(string(body), "\n")
	lines := strings.Split(text, "\n")
	if lines[0] != PlanHeader {
		return p, []Finding{{1, fmt.Sprintf("header %q is not %q", lines[0], PlanHeader)}}
	}
	var findings []Finding
	seen := map[string]int{}
	for i, line := range lines[1:] {
		n := i + 2
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		switch cols[0] {
		case "policy":
			if len(cols) != 3 {
				findings = append(findings, Finding{n, fmt.Sprintf("policy wants 3 tab-separated columns, got %d", len(cols))})
				continue
			}
			key, value := cols[1], cols[2]
			if first, ok := p.policyLine[key]; ok {
				findings = append(findings, Finding{n, fmt.Sprintf("policy %s repeats line %d", key, first)})
				continue
			}
			if reason := checkPolicy(key, value); reason != "" {
				findings = append(findings, Finding{n, reason})
				continue
			}
			p.Policy[key], p.policyLine[key] = value, n
		case capacity.KindBench, capacity.KindFriend:
			if len(cols) != 4 {
				findings = append(findings, Finding{n, fmt.Sprintf("%s wants 4 tab-separated columns, got %d", cols[0], len(cols))})
				continue
			}
			kind, name, machine := cols[0], cols[1], cols[2]
			if name == "" || machine == "" || strings.ContainsAny(name+machine, " :") {
				findings = append(findings, Finding{n, fmt.Sprintf("%s name %q and machine %q must be non-empty words", kind, name, machine)})
				continue
			}
			slots, err := strconv.Atoi(cols[3])
			if err != nil || slots < 0 || slots > PlanMaxSlots {
				findings = append(findings, Finding{n, fmt.Sprintf("%s %s slots %q is not an integer 0..%d", kind, name, cols[3], PlanMaxSlots)})
				continue
			}
			id := kind + " " + name
			if first, ok := seen[id]; ok {
				findings = append(findings, Finding{n, fmt.Sprintf("%s repeats line %d", id, first)})
				continue
			}
			seen[id] = n
			p.Rows = append(p.Rows, PlanRow{Line: n, Kind: kind, Name: name, Machine: machine, Slots: slots})
		default:
			findings = append(findings, Finding{n, fmt.Sprintf("unknown row kind %q; want policy, bench or friend", cols[0])})
		}
	}
	for _, key := range PolicyKeys {
		if _, ok := p.policyLine[key]; !ok && !refusedKey(findings, key) {
			findings = append(findings, Finding{len(lines), fmt.Sprintf("policy %s is missing; each of %s appears once", key, strings.Join(PolicyKeys, ", "))})
		}
	}
	return p, findings
}

// refusedKey reports whether a finding already names a present but invalid
// policy key, so a bad value is not also reported as missing.
func refusedKey(findings []Finding, key string) bool {
	for _, f := range findings {
		if strings.HasPrefix(f.Reason, "policy "+key+" ") {
			return true
		}
	}
	return false
}

func checkPolicy(key, value string) string {
	switch key {
	case "backpressure_missing":
		if value != "open" && value != "closed" {
			return fmt.Sprintf("policy %s %s is not open or closed", key, value)
		}
	case "ci_reruns":
		if n, err := strconv.Atoi(value); err != nil || n < 0 || n > 3 {
			return fmt.Sprintf("policy %s %s is not an integer 0..3", key, value)
		}
	case "readers":
		if value != "1" && value != "2" {
			return fmt.Sprintf("policy %s %s is not 1 or 2", key, value)
		}
	case "absent_after":
		if d, err := time.ParseDuration(value); err != nil || d < time.Minute || d > 24*time.Hour {
			return fmt.Sprintf("policy %s %s is not a duration from 1m to 24h", key, value)
		}
	default:
		return "unknown policy key " + key
	}
	return ""
}

type consumerKey struct{ kind, name string }

type storedDesired struct {
	slots   int
	machine string
}

func readDesired(values []any) (storedDesired, error) {
	var d storedDesired
	if len(values) > 0 {
		if text, ok := values[0].(string); ok && text != "" {
			n, err := strconv.Atoi(text)
			if err != nil {
				return d, fmt.Errorf("slots %q", text)
			}
			d.slots = n
		}
	}
	if len(values) > 1 {
		if text, ok := values[1].(string); ok {
			d.machine = text
		}
	}
	return d, nil
}

// ValidatePlan checks a parsed plan against the store in two pipelined round
// trips: names registered, every plan machine has a ceiling, and on each
// machine the plan slots plus the stored slots of consumers outside the plan
// fit the ceiling, every plan row substituted at once (rev 4). It never
// writes.
func ValidatePlan(ctx context.Context, st *store.Store, p *Plan) ([]Finding, error) {
	c := st.Client()
	machines := map[string]bool{}
	for _, r := range p.Rows {
		machines[r.Machine] = true
	}

	// Round trip 1: registries, the plan machines' ceilings, the plan rows'
	// desired hashes.
	pipe := c.Pipeline()
	friends := pipe.SMembers(ctx, "friends")
	benches := pipe.SMembers(ctx, "benches")
	ceilings := map[string]*redis.StringCmd{}
	for m := range machines {
		ceilings[m] = pipe.HGet(ctx, capacity.MachineCeilingKey(m), "slots")
	}
	rowCmds := make([]*redis.SliceCmd, len(p.Rows))
	for i, r := range p.Rows {
		rowCmds[i] = pipe.HMGet(ctx, capacity.DesiredKey(r.Kind, r.Name), "slots", "machine")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("plan validate: %w", err)
	}
	registered := map[consumerKey]bool{}
	for _, n := range friends.Val() {
		registered[consumerKey{capacity.KindFriend, n}] = true
	}
	for _, n := range benches.Val() {
		registered[consumerKey{capacity.KindBench, n}] = true
	}
	inPlan := map[consumerKey]bool{}
	left := map[string]bool{} // stored machines of plan consumers the plan does not name
	for i, r := range p.Rows {
		inPlan[consumerKey{r.Kind, r.Name}] = true
		d, err := readDesired(rowCmds[i].Val())
		if err != nil {
			return nil, fmt.Errorf("plan validate %s %s: %w", r.Kind, r.Name, err)
		}
		if d.machine != "" && !machines[d.machine] {
			left[d.machine] = true
		}
	}

	// Round trip 2: every consumer outside the plan, and the ceilings of the
	// machines plan consumers leave.
	var outsiders []consumerKey
	for k := range registered {
		if !inPlan[k] {
			outsiders = append(outsiders, k)
		}
	}
	sort.Slice(outsiders, func(i, j int) bool {
		if outsiders[i].kind != outsiders[j].kind {
			return outsiders[i].kind < outsiders[j].kind
		}
		return outsiders[i].name < outsiders[j].name
	})
	pipe = c.Pipeline()
	outCmds := make([]*redis.SliceCmd, len(outsiders))
	for i, k := range outsiders {
		outCmds[i] = pipe.HMGet(ctx, capacity.DesiredKey(k.kind, k.name), "slots", "machine")
	}
	for m := range left {
		ceilings[m] = pipe.HGet(ctx, capacity.MachineCeilingKey(m), "slots")
	}
	if len(outsiders) > 0 || len(left) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("plan validate: %w", err)
		}
	}
	outside := map[string]int{}
	for i, k := range outsiders {
		d, err := readDesired(outCmds[i].Val())
		if err != nil {
			return nil, fmt.Errorf("plan validate %s %s: %w", k.kind, k.name, err)
		}
		if d.machine != "" {
			outside[d.machine] += d.slots
		}
	}

	var findings []Finding
	for _, r := range p.Rows {
		if !registered[consumerKey{r.Kind, r.Name}] {
			set := "benches"
			if r.Kind == capacity.KindFriend {
				set = "friends"
			}
			findings = append(findings, Finding{r.Line, fmt.Sprintf("%s %s is not in %s (a plan never registers a name)", r.Kind, r.Name, set)})
		}
	}
	names := make([]string, 0, len(ceilings))
	for m := range ceilings {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		ceiling, err := ceilings[m].Int()
		if err != nil {
			if machines[m] {
				findings = append(findings, Finding{lastRowOn(p, m), fmt.Sprintf("machine %s has no ceiling (machine:%s:ceiling slots)", m, m)})
			}
			continue
		}
		sum := outside[m]
		for _, r := range p.Rows {
			if r.Machine == m {
				sum += r.Slots
			}
		}
		if sum > ceiling {
			line := lastRowOn(p, m)
			if line == 0 {
				line = 1
			}
			findings = append(findings, Finding{line, (&capacity.CeilingError{Machine: m, Sum: sum, Ceiling: ceiling}).Error()})
		}
	}
	return findings, nil
}

func lastRowOn(p *Plan, m string) int {
	line := 0
	for _, r := range p.Rows {
		if r.Machine == m {
			line = r.Line
		}
	}
	return line
}

// RoutesSHA identifies the embedded route table the binary enforces: the
// sha256 of the table as the route package renders it after parsing the
// embedded routes.yaml. The route package does not export the file bytes, and
// this build stays inside the #2380 paths; any change to a parsed field of
// routes.yaml changes it.
func RoutesSHA() (string, error) {
	t, err := route.Load()
	if err != nil {
		return "", fmt.Errorf("routes: %w", err)
	}
	sum := sha256.Sum256([]byte(t.Source + "\n" + t.Rule + "\n" + t.Render()))
	return hex.EncodeToString(sum[:]), nil
}

// Applier applies plans for one Redis seat. User is only the record written
// as applied_by and the name in an authority refusal; the gate is the ACL of
// the connection, checked inside the function.
type Applier struct {
	Store *store.Store
	User  string
	// afterValidate runs between Go validation and the FCALL (tests only).
	afterValidate func(context.Context)
}

// Apply validates and applies one plan and returns the exit code: 0 for
// PLAN-APPLIED or PLAN-UNCHANGED, 1 for any refusal, 2 when the store cannot
// be read. Results go to out and refusals to errOut.
func (a Applier) Apply(ctx context.Context, name string, body []byte, out, errOut io.Writer) int {
	if err := check(a.Store, name); err != nil {
		fmt.Fprintf(errOut, "PLAN-ERROR %v\n", err)
		return 2
	}
	p, findings := ParsePlan(body)
	if len(findings) == 0 {
		more, err := ValidatePlan(ctx, a.Store, p)
		if err != nil {
			fmt.Fprintf(errOut, "PLAN-ERROR %v\n", err)
			return 2
		}
		findings = more
	}
	if len(findings) > 0 {
		sort.SliceStable(findings, func(i, j int) bool { return findings[i].Line < findings[j].Line })
		for _, f := range findings {
			fmt.Fprintln(errOut, f.String())
		}
		return 1
	}
	routes, err := RoutesSHA()
	if err != nil {
		fmt.Fprintf(errOut, "PLAN-ERROR %v\n", err)
		return 2
	}
	if a.afterValidate != nil {
		a.afterValidate(ctx)
	}
	user := a.User
	if user == "" {
		user = "default"
	}
	args := []any{name, p.SHA, string(p.Body), user, routes}
	for _, key := range PolicyKeys {
		args = append(args, p.Policy[key])
	}
	args = append(args, strconv.Itoa(len(p.Rows)))
	for _, r := range p.Rows {
		args = append(args, r.Kind, r.Name, r.Machine, strconv.Itoa(r.Slots))
	}
	reply, err := a.Store.Client().FCall(ctx, FunctionPlan, nil, args...).StringSlice()
	if err != nil {
		text := err.Error()
		switch {
		case strings.HasPrefix(text, "NOAUTHZ"):
			fmt.Fprintf(errOut, "PLAN-REFUSED authority: redis user %s may not apply a sprint plan (needs HSET on %s)\n", user, AuthzKey)
			return 1
		case strings.HasPrefix(text, "NOPERM"):
			fmt.Fprintf(errOut, "PLAN-REFUSED authority: redis user %s may not apply a sprint plan (needs HSET on %s): %s\n", user, AuthzKey, text)
			return 1
		}
		fmt.Fprintf(errOut, "PLAN-ERROR %s: %v\n", FunctionPlan, err)
		return 2
	}
	if len(reply) == 0 {
		fmt.Fprintf(errOut, "PLAN-ERROR %s: empty reply\n", FunctionPlan)
		return 2
	}
	switch reply[0] {
	case "APPLIED":
		fmt.Fprintf(out, "PLAN-APPLIED %s rows=%d sha=%s\n", name, len(p.Rows), short(p.SHA))
		return 0
	case "UNCHANGED":
		fmt.Fprintf(out, "PLAN-UNCHANGED %s sha=%s\n", name, short(p.SHA))
		return 0
	case "CEILING":
		if len(reply) == 4 {
			fmt.Fprintf(errOut, "PLAN-REFUSED check: CEILING %s %s/%s\n", reply[1], reply[2], reply[3])
			return 1
		}
	}
	fmt.Fprintf(errOut, "PLAN-REFUSED check: %s\n", strings.Join(reply, " "))
	return 1
}

// Show prints the applied plan against the store and returns 0 when nothing
// drifted, 1 on any DRIFT line or when the sprint has no plan, and 2 when the
// store cannot be read. It uses plain reads only, in two pipelined round
// trips, so any seat that may read s:*, bench:*, friend:*, lease:* and proc:*
// can run it.
func Show(ctx context.Context, st *store.Store, name string, out, errOut io.Writer) int {
	if err := check(st, name); err != nil {
		fmt.Fprintf(errOut, "PLAN-ERROR %v\n", err)
		return 2
	}
	c := st.Client()
	pipe := c.Pipeline()
	planCmd := pipe.HGetAll(ctx, "s:"+name+":plan")
	policyCmd := pipe.HGetAll(ctx, "s:"+name+":policy")
	lease := pipe.HGetAll(ctx, "lease:reconciler")
	proc := pipe.HGetAll(ctx, "proc:reconciler")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		fmt.Fprintf(errOut, "PLAN-ERROR show %s: %v\n", name, err)
		return 2
	}
	record := planCmd.Val()
	if len(record) == 0 {
		fmt.Fprintf(out, "PLAN %s none\n", name)
		return 1
	}
	p, findings := ParsePlan([]byte(record["body"]))
	if len(findings) > 0 {
		fmt.Fprintf(errOut, "PLAN-ERROR show %s: stored body does not parse: %s\n", name, findings[0])
		return 2
	}

	pipe = c.Pipeline()
	rowCmds := make([]*redis.SliceCmd, len(p.Rows))
	for i, r := range p.Rows {
		rowCmds[i] = pipe.HMGet(ctx, capacity.DesiredKey(r.Kind, r.Name), "slots", "machine")
	}
	if len(p.Rows) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			fmt.Fprintf(errOut, "PLAN-ERROR show %s: %v\n", name, err)
			return 2
		}
	}

	var b bytes.Buffer
	appliedAt := record["applied_at"]
	if ms, err := strconv.ParseInt(appliedAt, 10, 64); err == nil {
		appliedAt = time.UnixMilli(ms).In(time.Local).Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "PLAN %s sha=%s rows=%s applied_at=%s applied_by=%s routes_sha=%s\n",
		name, short(record["sha"]), record["rows"], appliedAt, record["applied_by"], short(record["routes_sha"]))
	var drift []string
	policy := policyCmd.Val()
	for _, key := range PolicyKeys {
		want, got := p.Policy[key], orDash(policy[key])
		fmt.Fprintf(&b, "policy %s plan=%s store=%s\n", key, want, got)
		if want != got {
			drift = append(drift, fmt.Sprintf("DRIFT policy %s plan=%s store=%s", key, want, got))
		}
	}
	for i, r := range p.Rows {
		values := rowCmds[i].Val()
		slots, machine := "-", "-"
		if len(values) > 0 {
			if s, ok := values[0].(string); ok {
				slots = s
			}
		}
		if len(values) > 1 {
			if m, ok := values[1].(string); ok {
				machine = m
			}
		}
		want := strconv.Itoa(r.Slots)
		fmt.Fprintf(&b, "%s %s %s plan=%s store=%s\n", r.Kind, r.Name, r.Machine, want, slots)
		if machine != r.Machine {
			drift = append(drift, fmt.Sprintf("DRIFT %s %s machine plan=%s store=%s", r.Kind, r.Name, r.Machine, machine))
		}
		if slots != want {
			drift = append(drift, fmt.Sprintf("DRIFT %s %s slots plan=%s store=%s", r.Kind, r.Name, want, slots))
		}
	}
	fmt.Fprintln(&b, reconcilerLine(lease.Val(), proc.Val(), time.Now()).String())
	if len(drift) == 0 {
		b.WriteString("DRIFT none\n")
	} else {
		b.WriteString(strings.Join(drift, "\n") + "\n")
	}
	_, _ = out.Write(b.Bytes())
	if len(drift) > 0 {
		return 1
	}
	return 0
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// reconcilerLine is preflight check 7.7 (preflight/store.go checkReconciler)
// over the two hashes show already read in its first round trip, with the
// same thresholds and wording. It uses the client clock because TIME is not
// in the viewer seat's grant.
func reconcilerLine(lease, proc map[string]string, now time.Time) preflight.Line {
	const n, name = "7.7", "reconciler"
	var reds []string
	if len(lease) == 0 {
		reds = append(reds, "lease:reconciler missing")
	} else {
		if lease["instance"] == "" || lease["token"] == "" {
			reds = append(reds, "lease:reconciler has no instance or token")
		}
		if at, ok := parseStamp(lease["at"]); !ok {
			reds = append(reds, "lease:reconciler has no at")
		} else if age := now.Sub(at); age > preflight.ReconcilerTTL {
			reds = append(reds, fmt.Sprintf("lease:reconciler stale (renewed %s ago, ttl %s)", wholeSecs(age), wholeSecs(preflight.ReconcilerTTL)))
		}
	}
	passAt, ok := parseStamp(proc["pass_at"])
	if !ok {
		reds = append(reds, "proc:reconciler has no pass_at")
	} else if age := now.Sub(passAt); age > preflight.ReconcilerPass {
		reds = append(reds, fmt.Sprintf("last pass %s ago (limit %s)", wholeSecs(age), wholeSecs(preflight.ReconcilerPass)))
	}
	if len(reds) > 0 {
		return preflight.Line{N: n, Name: name, Red: true, Why: strings.Join(reds, "; ")}
	}
	return preflight.Line{N: n, Name: name, Why: fmt.Sprintf("instance %s on %s, last pass %s ago", lease["instance"], lease["host"], wholeSecs(now.Sub(passAt)))}
}

func parseStamp(s string) (time.Time, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return time.Time{}, false
	}
	if v > 1e12 {
		return time.UnixMilli(int64(v)), true
	}
	sec := int64(v)
	return time.Unix(sec, int64((v-float64(sec))*1e9)), true
}

func wholeSecs(d time.Duration) string {
	return fmt.Sprintf("%ds", int64(d.Round(time.Second)/time.Second))
}
