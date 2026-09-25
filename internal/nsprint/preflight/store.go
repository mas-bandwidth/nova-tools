package preflight

// The store checks (#2947 rev 3) over one snapshot: Redis and the function
// library (7.1), state files and second writers (7.2), leases against beats
// (7.3), the reconciler (7.7), the policy (7.9), states against their
// receipts (7.10), supply (7.11), card guards (7.13), width per machine
// (7.15), TTLs (7.26) and capacity changed under load (7.27). Nothing here
// reads a file or talks to Redis: preflight.go's snapshot is the only input.

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

func (s *snapshot) checks() []Line {
	lines := []Line{
		s.checkRedis(),
		s.checkStateFiles(StateSources, InterimKeys),
		s.checkLeases(),
		s.checkReconciler(),
		s.checkPolicy(),
		s.checkReceipts(),
		s.checkSupply(),
		s.checkGuards(),
		s.checkCeiling(),
		s.checkTTL(),
		s.checkUnderLoad(),
	}
	// A read that failed is no evidence: every line that needed it is RED.
	if len(s.readErr) > 0 {
		for i := 1; i < len(lines); i++ {
			lines[i] = Line{N: lines[i].N, Name: lines[i].Name, Red: true, Why: "cannot read: " + limit(s.readErr, 3)}
		}
	}
	return lines
}

func verdict(n, name string, reds []string, green string) Line {
	if len(reds) == 0 {
		return Line{N: n, Name: name, Why: green}
	}
	return Line{N: n, Name: name, Red: true, Why: limit(reds, 6)}
}

func limit(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:n], "; ") + fmt.Sprintf("; +%d more", len(items)-n)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// 7.1: standalone, AOF on, nova_sprint loaded at the binary's version, and
// the ACL deployment test's receipt (#2937) ok at the binary's library.
func (s *snapshot) checkRedis() Line {
	const n, name = "7.1", "redis"
	for _, err := range []error{s.infoErr, s.libErr} {
		if isNoPerm(err) {
			return needsSeat(n, name, s.seat, err)
		}
	}
	var reds []string
	if s.infoErr != nil {
		reds = append(reds, "cannot read INFO: "+firstLine(s.infoErr.Error()))
	} else {
		if mode := s.info["redis_mode"]; mode != "standalone" {
			reds = append(reds, fmt.Sprintf("redis_mode %q, not the standalone fleet instance", mode))
		}
		if s.info["aof_enabled"] != "1" {
			reds = append(reds, "AOF off")
		}
	}
	want, err := fn.Source()
	if err != nil {
		reds = append(reds, "the binary's library does not build: "+err.Error())
	}
	if s.libErr != nil {
		reds = append(reds, "cannot list functions: "+firstLine(s.libErr.Error()))
	} else if lib, ok := library(s.libs, fn.Library); !ok {
		reds = append(reds, "library "+fn.Library+" not loaded")
	} else if err == nil && strings.TrimSpace(lib.Code) != strings.TrimSpace(want) {
		reds = append(reds, "library "+fn.Library+" is not the binary's version")
	}
	acl := s.acl
	green := "standalone, AOF on, " + fn.Library + " at the binary's version"
	switch {
	case len(acl) == 0:
		reds = append(reds, "proc:acl-test missing (the ACL deployment test writes it, #2937)")
	case acl["result"] != "ok":
		reds = append(reds, fmt.Sprintf("proc:acl-test result %q, not ok", acl["result"]))
	case err == nil && acl["library_sha"] != fn.Sum(want):
		reds = append(reds, fmt.Sprintf("proc:acl-test library_sha %q is not the binary's %s", acl["library_sha"], fn.Sum(want)))
	default:
		if at, ok := parseStamp(acl["at"]); ok {
			green += ", proc:acl-test ok " + wholeSecs(s.now.Sub(at)) + " ago"
		} else {
			reds = append(reds, "proc:acl-test has no at")
		}
	}
	return verdict(n, name, reds, green)
}

// fieldRef is one field a state source names: the key, the field and its value.
type fieldRef struct{ key, field, value string }

// StateSource is one row of 7.2's first table: a place the tool is
// configured from, which must never name a state file.
type StateSource struct {
	Name   string
	fields func(s *snapshot) []fieldRef
}

// InterimKey is one row of 7.2's second table: a key a bash interim writer
// still writes beside its Go writer. judge returns one reason per key written
// by the interim writer inside the window.
type InterimKey struct {
	Name  string
	judge func(s *snapshot, window time.Duration) []string
}

// cardWorkFields are a card's own description of its work: they may name any
// file the work touches (a card may edit a TSV it names), so 7.2 does not
// read them as configuration.
var cardWorkFields = map[string]bool{"paths": true, "done_when": true, "task": true, "test": true,
	"origin": true, "depends_on": true, "depends_on_typed": true}

// StateSources is 7.2's first table (#2947 rev 3): every field it reads, and
// nothing outside it. A test removes one row to prove that row alone catches
// its case.
var StateSources = []StateSource{
	{Name: "policy", fields: func(s *snapshot) []fieldRef {
		var out []fieldRef
		for _, st := range s.sprints {
			for _, f := range sortedKeys(st.policy) {
				out = append(out, fieldRef{"s:" + st.name + ":policy", f, st.policy[f]})
			}
		}
		return out
	}},
	{Name: "card", fields: func(s *snapshot) []fieldRef {
		var out []fieldRef
		for _, st := range s.sprints {
			for _, label := range st.idx["card:queued"] {
				rec := st.cards[label]
				for _, f := range sortedKeys(rec) {
					if !cardWorkFields[f] {
						out = append(out, fieldRef{"s:" + st.name + ":card:" + label, f, rec[f]})
					}
				}
			}
		}
		return out
	}},
	{Name: "bench-launcher", fields: beatField("bench", "launcher")},
	{Name: "friend-harness", fields: beatField("friend", "harness")},
	{Name: "friend-session", fields: beatField("friend", "session")},
}

// beatField reads one field of every <kind>:<n>:beat, as presence.lua writes it.
func beatField(kind, field string) func(s *snapshot) []fieldRef {
	return func(s *snapshot) []fieldRef {
		var out []fieldRef
		for _, k := range s.consumers {
			if k.kind == kind {
				if v, ok := k.beat[field]; ok {
					out = append(out, fieldRef{k.key(":beat"), field, v})
				}
			}
		}
		return out
	}
}

// benchRowCounts are the fields bash bench-row writes on bench:<b> and
// presence.lua's bench beat never does (it leaves counts to the card views).
var benchRowCounts = []string{"ready", "ok", "fail"}

// InterimKeys is 7.2's second table. Rev 3 named four rows; three of them
// (q:<f>, q:<f>:front and friend:<f>) are written today by the library itself
// (friend_queue.lua and route_duty.lua XADD the queues, presence.lua's
// friend_row HSETs the row, #3440) in the same shape the bash wrote, so a
// fresh entry there is the Go path's own write and no evidence of a second
// writer. bench:<b> still tells the two apart: bash bench-row sets a TTL and
// the count fields, presence.lua writes host, load1, ncpu and at with neither.
var InterimKeys = []InterimKey{
	{Name: "bench-row", judge: func(s *snapshot, window time.Duration) []string {
		var out []string
		for _, k := range s.consumers {
			if k.kind != "bench" || len(k.row) == 0 {
				continue
			}
			at, ok := parseStamp(k.row["at"])
			if !ok || s.now.Sub(at) > window {
				continue
			}
			var shape []string
			if d := s.pttl[k.key("")]; d > 0 {
				shape = append(shape, "a TTL")
			}
			for _, f := range benchRowCounts {
				if _, has := k.row[f]; has {
					shape = append(shape, "field "+f)
					break
				}
			}
			if len(shape) > 0 {
				out = append(out, fmt.Sprintf("%s written %s ago in bash bench-row's shape (%s) beside presence.lua (two writers)",
					k.key(""), wholeSecs(s.now.Sub(at)), strings.Join(shape, ", ")))
			}
		}
		return out
	}},
}

func rowNames[T any](rows []T, name func(T) string) string {
	var out []string
	for _, r := range rows {
		out = append(out, name(r))
	}
	return strings.Join(out, ", ")
}

// 7.2: a state file named in any StateSources field, or an InterimKeys key
// written by its interim writer inside interim_window_s.
func (s *snapshot) checkStateFiles(sources []StateSource, interim []InterimKey) Line {
	const n, name = "7.2", "state-files"
	var reds []string
	for _, src := range sources {
		for _, f := range src.fields(s) {
			for _, p := range append(stateFiles(f.field), stateFiles(f.value)...) {
				reds = append(reds, fmt.Sprintf("%s %s names %s", f.key, f.field, p))
			}
		}
	}
	note := ""
	if window, ok := s.window("interim_window_s"); ok {
		for _, row := range interim {
			reds = append(reds, row.judge(s, window)...)
		}
	} else {
		note = ", interim writers unjudged: interim_window_s unset (7.9)"
	}
	return verdict(n, name, reds, fmt.Sprintf("%d state sources name no state file (%s); %d interim keys have no second writer (%s)%s",
		len(sources), rowNames(sources, func(r StateSource) string { return r.Name }),
		len(interim), rowNames(interim, func(r InterimKey) string { return r.Name }), note))
}

// stateFiles returns the path-like tokens in text that name a file the tool
// would read or write as state: a TSV, lock or pid file, a BEAT file, a
// backpressure file or a control file.
func stateFiles(text string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return r <= ' ' || strings.ContainsRune("=,;:\"'()[]{}<>", r)
	}) {
		if isStateFile(tok) {
			out = append(out, tok)
		}
	}
	return out
}

// isStateFile reports whether tok names a state file. The state-file names (a
// BEAT file, a file named backpressure or control) count bare as well as in a
// path, since a relative name such as `BEAT` is a file the tool would open in
// its working directory. Any other word without a `/` or `.` is a word, not a
// path, so `backpressure_missing` and `beat` stay green.
func isStateFile(tok string) bool {
	base := path.Base(tok)
	lower := strings.ToLower(base)
	stem := strings.TrimSuffix(lower, path.Ext(lower))
	switch path.Ext(lower) {
	case ".tsv", ".lock", ".pid":
		return true
	}
	if strings.HasPrefix(base, "BEAT") || stem == "backpressure" || stem == "control" {
		return true
	}
	if !strings.Contains(tok, "/") && !strings.Contains(base, ".") {
		return false // a word, not a path
	}
	return strings.Contains(lower, "backpressure")
}

// 7.3: leased is not equal to living beats past the start window (Johnny 10).
func (s *snapshot) checkLeases() Line {
	const n, name = "7.3", "leases-vs-beats"
	start, startOK := s.window("start_window_s")
	stale, staleOK := s.window("beat_stale_s")
	var reds, unset []string
	if !startOK {
		unset = append(unset, "start_window_s")
	}
	if !staleOK {
		unset = append(unset, "beat_stale_s")
	}
	leased, beats := 0, 0
	for _, k := range s.consumers {
		late, old := 0, 0
		for _, z := range k.starting {
			if startOK && s.now.Sub(stamp(z.Score)) > start {
				late++
			}
		}
		for _, z := range k.living {
			if staleOK && s.now.Sub(stamp(z.Score)) > stale {
				old++
			}
		}
		n := len(k.starting) + len(k.living)
		leased += n
		beats += len(k.living) - old
		var why []string
		if late > 0 {
			why = append(why, fmt.Sprintf("%d starting past %s", late, wholeSecs(start)))
		}
		if old > 0 {
			why = append(why, fmt.Sprintf("%d living beat past %s", old, wholeSecs(stale)))
		}
		if len(why) > 0 {
			reds = append(reds, fmt.Sprintf("%s %s %s (leased %d, living beats %d)", k.kind, k.name,
				strings.Join(why, ", "), n, len(k.living)-old))
		}
	}
	green := fmt.Sprintf("%d consumers, leased %d, living beats %d", len(s.consumers), leased, beats)
	if startOK && staleOK {
		green += fmt.Sprintf(", no reservation past %s and no beat past %s", wholeSecs(start), wholeSecs(stale))
	} else {
		green += "; unjudged: " + strings.Join(unset, ", ") + " unset (7.9)"
	}
	return verdict(n, name, reds, green)
}

// 7.7: the reconciler lease is missing or not renewed within a pass, or its
// last pass is older than reconciler_pass_s. A second instance is refused by
// the lease itself (#2726); the store shows only the holder.
func (s *snapshot) checkReconciler() Line {
	const n, name = "7.7", "reconciler"
	pass, ok := s.window("reconciler_pass_s")
	if !ok {
		return Line{N: n, Name: name, Why: "unjudged: reconciler_pass_s unset (7.9)"}
	}
	var reds []string
	l := s.lease
	leaseAge := "?"
	if len(l) == 0 {
		reds = append(reds, "lease:reconciler missing")
	} else {
		if l["instance"] == "" || l["token"] == "" {
			reds = append(reds, "lease:reconciler has no instance or token")
		}
		if at, ok := parseStamp(l["at"]); !ok {
			reds = append(reds, "lease:reconciler has no at")
		} else if age := s.now.Sub(at); age > pass {
			reds = append(reds, fmt.Sprintf("lease:reconciler stale (renewed %s ago, limit %s)", wholeSecs(age), wholeSecs(pass)))
		} else {
			leaseAge = wholeSecs(age)
		}
	}
	passAge := "?"
	if passAt, ok := parseStamp(s.proc["pass_at"]); !ok {
		reds = append(reds, "proc:reconciler has no pass_at")
	} else if age := s.now.Sub(passAt); age > pass {
		reds = append(reds, fmt.Sprintf("last pass %s ago (limit %s)", wholeSecs(age), wholeSecs(pass)))
	} else {
		passAge = wholeSecs(age)
	}
	return verdict(n, name, reds, fmt.Sprintf("instance %s, lease renewed %s ago, last pass %s ago (limit %s)",
		l["instance"], leaseAge, passAge, wholeSecs(pass)))
}

// 7.9: every checked sprint's policy sets the brakes and the windows. A
// missing field is RED and never defaults.
func (s *snapshot) checkPolicy() Line {
	const n, name = "7.9", "policy"
	if len(s.sprints) == 0 {
		return Line{N: n, Name: name, Red: true, Why: "no sprint to check: sprints is empty and no --sprint"}
	}
	var reds, names []string
	for _, st := range s.sprints {
		names = append(names, "s:"+st.name+":policy")
		var missing []string
		for _, f := range PolicyFields {
			if strings.TrimSpace(st.policy[f]) == "" {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			reds = append(reds, fmt.Sprintf("s:%s:policy missing %s", st.name, strings.Join(missing, ", ")))
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%s sets all %d fields", strings.Join(names, ", "), len(PolicyFields)))
}

// 7.10: an id in an index set whose latest receipt names a different state.
// The index keys come from fn.IndexStates; nothing is SCANned.
func (s *snapshot) checkReceipts() Line {
	const n, name = "7.10", "receipts"
	var reds []string
	ids := 0
	for _, st := range s.sprints {
		latest := map[string]string{}
		for _, m := range st.log {
			key := fmt.Sprint(m.Values["kind"]) + " " + fmt.Sprint(m.Values["id"])
			if _, seen := latest[key]; !seen {
				latest[key] = fmt.Sprint(m.Values["to"])
			}
		}
		for _, kind := range fn.IndexKinds {
			for _, state := range fn.IndexStates[kind] {
				for _, id := range st.idx[kind+":"+state] {
					ids++
					got, seen := latest[kind+" "+id]
					if !seen {
						got = "none"
					}
					if got != state {
						reds = append(reds, fmt.Sprintf("%s %s %s, receipt %s", kind, id, state, got))
					}
				}
			}
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d sprints, %d indexed ids, each at its latest receipt", len(s.sprints), ids))
}

func paused(k *consumer) bool { return k.desired["paused"] == "1" }

func hasRole(k *consumer, role string) bool {
	for _, r := range strings.Split(k.roles["roles"], ",") {
		if strings.TrimSpace(r) == role {
			return true
		}
	}
	return false
}

// 7.11: a ready task or pool card that no registered, unpaused consumer may
// take. A task's kind picks the role the way redistribute_assign.lua does (a
// read or review needs may-hold, any other kind builder or coordinator) once
// any friend has a roles hash; a pool card needs an unpaused bench, the one
// it is pinned to when it names one.
func (s *snapshot) checkSupply() Line {
	const n, name = "7.11", "supply"
	rolesSet := false
	for _, k := range s.consumers {
		if k.kind == "friend" && len(k.roles) > 0 {
			rolesSet = true
		}
	}
	var reds []string
	tasks, cards := 0, 0
	for _, st := range s.sprints {
		for _, id := range st.ready {
			tasks++
			kind := st.tasks[id]["kind"]
			if kind == "" {
				kind = "work"
			}
			need := []string{"builder", "coordinator"}
			if kind == "read" || kind == "review" {
				need = []string{"may-hold"}
			}
			found := false
			for _, k := range s.consumers {
				if k.kind != "friend" || paused(k) {
					continue
				}
				if !rolesSet {
					found = true
					break
				}
				for _, r := range need {
					if hasRole(k, r) {
						found = true
					}
				}
			}
			if !found {
				why := "no unpaused friend"
				if rolesSet {
					why = "no unpaused friend has the " + strings.Join(need, " or ") + " role"
				}
				reds = append(reds, fmt.Sprintf("task %s (kind %s) has no eligible consumer: %s", id, kind, why))
			}
		}
		for _, label := range st.pool {
			cards++
			pin := st.cards[label]["bench"]
			if pin == "_pool" {
				pin = ""
			}
			found := false
			for _, k := range s.consumers {
				if k.kind == "bench" && !paused(k) && (pin == "" || pin == k.name) {
					found = true
					break
				}
			}
			if !found {
				why := "no unpaused bench"
				if pin != "" {
					why = "bench " + pin + " is not registered or is paused"
				}
				reds = append(reds, fmt.Sprintf("card %s has no eligible consumer: %s", label, why))
			}
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d ready tasks and %d pool cards each have an eligible consumer", tasks, cards))
}

// 7.13: every queued card passes card.Lint, the same header rules push held
// it to.
func (s *snapshot) checkGuards() Line {
	const n, name = "7.13", "guards"
	var reds []string
	queued := 0
	for _, st := range s.sprints {
		for _, label := range st.idx["card:queued"] {
			queued++
			rec := st.cards[label]
			if len(rec) == 0 {
				reds = append(reds, fmt.Sprintf("card %s is queued with no record s:%s:card:%s", label, st.name, label))
				continue
			}
			if err := card.Lint(rec); err != nil {
				reds = append(reds, fmt.Sprintf("card %s: %v", label, err))
			}
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d queued cards lint clean", queued))
}

// 7.15: width per machine (2.4).
func (s *snapshot) checkCeiling() Line {
	const n, name = "7.15", "machine-ceiling"
	var reds []string
	sum := map[string]int{}
	for _, k := range s.consumers {
		slots, machine := k.desired["slots"], k.desired["machine"]
		if k.slotsKey {
			reds = append(reds, fmt.Sprintf("friend:%s:slots exists beside friend:%s:desired (two writers)", k.name, k.name))
		}
		if machine == "" {
			reds = append(reds, fmt.Sprintf("%s %s has no desired machine", k.kind, k.name))
			continue
		}
		width, err := strconv.Atoi(slots)
		switch {
		case slots == "":
			reds = append(reds, fmt.Sprintf("%s %s has no desired slots on %s", k.kind, k.name, machine))
		case err != nil:
			reds = append(reds, fmt.Sprintf("%s %s desired slots %q is not a number", k.kind, k.name, slots))
		}
		sum[machine] += width
		if host := k.beat["host"]; host != "" && host != machine {
			reds = append(reds, fmt.Sprintf("%s %s beats on %s, desired %s", k.kind, k.name, host, machine))
		}
	}
	machines := make([]string, 0, len(sum))
	for m := range sum {
		machines = append(machines, m)
	}
	sort.Strings(machines)
	var green []string
	for _, m := range machines {
		v := s.ceilings[m]
		ceiling, err := strconv.Atoi(v)
		switch {
		case v == "":
			reds = append(reds, m+" has no ceiling")
		case err != nil:
			reds = append(reds, fmt.Sprintf("%s ceiling %q is not a number", m, v))
		case sum[m] > ceiling:
			reds = append(reds, fmt.Sprintf("%s %d/%d over its ceiling", m, sum[m], ceiling))
		default:
			green = append(green, fmt.Sprintf("%s %d/%d", m, sum[m], ceiling))
		}
	}
	if len(green) == 0 {
		green = []string{"no consumer has a desired machine"}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d machines: %s", len(machines), strings.Join(green, ", ")))
}

// interimKey reports a key 7.2's InterimKeys judge (bench:<b>), which 7.26
// leaves alone: bash bench-row sets EXPIRE 5 there, and one fault turns one
// line RED.
func (s *snapshot) interimKey(key string) bool {
	for _, k := range s.consumers {
		if k.kind == "bench" && key == k.key("") {
			return true
		}
	}
	return false
}

// 7.26: a key the snapshot read carries a TTL and is not in fn.TTLAllow.
func (s *snapshot) checkTTL() Line {
	const n, name = "7.26", "ttl"
	keys := make([]string, 0, len(s.pttl))
	for k := range s.pttl {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var reds, allowed []string
	held := 0
	for _, k := range keys {
		d := s.pttl[k]
		if d != -2 { // PTTL -2: the key does not exist
			held++
		}
		if d <= 0 || s.interimKey(k) {
			continue
		}
		if fn.TTLAllowed(k) {
			allowed = append(allowed, k)
			continue
		}
		reds = append(reds, fmt.Sprintf("%s has a TTL (%s left); only fn.TTLAllow keys expire", k, wholeSecs(d)))
	}
	if len(allowed) == 0 {
		allowed = []string{"none"}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d keys held, TTLs only on fn.TTLAllow keys (%s)", held, strings.Join(allowed, ", ")))
}

// 7.27: an open sprint's capacity changed after it opened (a cap:log entry
// whose kind starts "capacity ", as capacity.lua writes it, at after
// s:<S>.opened_at).
func (s *snapshot) checkUnderLoad() Line {
	const n, name = "7.27", "under-load"
	var reds, open []string
	for _, st := range s.sprints {
		if !st.open() {
			continue
		}
		opened, ok := parseStamp(st.meta["opened_at"])
		if !ok {
			reds = append(reds, fmt.Sprintf("s:%s is open with no opened_at", st.name))
			continue
		}
		open = append(open, "s:"+st.name)
		for _, m := range s.capLog {
			kind := fmt.Sprint(m.Values["kind"])
			if !strings.HasPrefix(kind, "capacity ") {
				continue
			}
			at, ok := parseStamp(fmt.Sprint(m.Values["at"]))
			if ok && at.After(opened) {
				reds = append(reds, fmt.Sprintf("%s %v on %v %s ago, after s:%s opened", kind, m.Values["target"], m.Values["machine"],
					wholeSecs(s.now.Sub(at)), st.name))
			}
		}
	}
	if len(open) == 0 {
		return verdict(n, name, reds, "no open sprint checked")
	}
	return verdict(n, name, reds, "no capacity change since "+strings.Join(open, ", ")+" opened")
}
