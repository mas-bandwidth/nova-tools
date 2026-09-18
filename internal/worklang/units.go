package worklang

// Amendment 1's keys (docs/SPEC-WORKLANG.md, "Amendment 1 (2026-09-18)"):
// :resources, :writes, :tools, :collects, :warm, :attempts, :state, :was and the
// typed spelling of :acceptance. A coordinator writes a set as
//
//	(work-set "<id>" :title "..." :under (:open-root) :units ((unit "<id>" ...) ...))
//
// THERE IS ONE READER OF THAT FORM, and it is workset.go's ParseWorkSet. This
// file is the amendment's half of it and owns no type and no entry point: the
// shape checks below are called by readUnit as it walks a unit's keys, and the
// accessors below read the Fields map that same walk fills. Two readers of one
// form was the defect this file was landed with -- a second Unit and a second
// ParseWorkSet, each with its own idea of what a unit is -- and the fix was to
// keep the reader the tree already had and fold these keys into it.
//
// It is PARSE ONLY: every key below is read and its shape checked, and nothing
// here admits, orders, serialises or leases. Scheduling semantics -- one unit per
// lane, writes that intersect serialising, a resource vector admitted atomically,
// an uncertain unit keeping its reservation -- belong to the kernel.
//
// Which side of workset.go's split a key falls on: an UNREADABLE value is a
// refusal (a :resources that is not a vector, an attempt outcome with no
// termination proof, a :state outside the closed set -- text this reader cannot
// turn into the thing the key names), and wrong CONTENT is a Check finding (a
// duplicate id, an absent need, an owner no registry knows).

import (
	"fmt"
	"strings"
)

// unitKeys are the fields a unit carries: the ones the real set of 2026-09-17
// already uses, plus the amendment's. A key not listed here is preserved in
// Unit.Unknown and ignored, exactly as a :node's unknown keys are.
var unitKeys = map[string]bool{
	// as the real work set writes them today
	"needs": true, "blocks": true, "title": true, "inputs": true, "owner": true,
	"status": true, "evidence": true, "note": true, "lane": true, "deadline": true,
	"budget": true, "affinity": true, "derive": true, "replaces": true,
	"findings": true, "pr": true, "prs": true, "card": true, "cards": true,
	"spec": true, "sprint": true, "under": true, "kind": true, "repo": true,
	"base": true, "output": true, "done-when": true, "units": true,
	// the amendment
	"id": true, "was": true, "resources": true, "writes": true, "tools": true,
	"collects": true, "warm": true, "attempts": true, "acceptance": true,
	"state": true,
}

// knownStates is the closed set of unit states. `uncertain` is one of them and
// not a synonym for open or failed: a unit whose last attempt cannot prove
// termination is uncertain, and it keeps its reservation until it can (Stella's
// lease rule -- an expiry is UNKNOWN until termination).
var knownStates = []string{
	"open", "ready", "live", "blocked", "uncertain", "closed", "refused", "abandoned",
}

// KnownStates returns the unit states the grammar admits, in spec order.
func KnownStates() []string { return append([]string(nil), knownStates...) }

// knownOutcomes is the closed set of attempt outcomes. An outcome that is not
// uncertain owes a termination proof.
var knownOutcomes = []string{"green", "red", "refused", "abandoned", "uncertain"}

// KnownOutcomes returns the attempt outcomes the grammar admits.
func KnownOutcomes() []string { return append([]string(nil), knownOutcomes...) }

var (
	knownAcceptanceKinds      = []string{"test", "job", "merged", "attested"}
	knownAcceptancePredicates = []string{"passes", "succeeds", "merged-at", "attested-by"}
)

// numericResources are the vector's dimensions that carry a count. A keyword
// not listed here is a named scarce resource and still carries an integer, so
// the vector is open without becoming untyped.
var numericResources = map[string]bool{
	"cpu": true, "memory-gb": true, "disk-gb": true, "network": true, "gpu": true,
}

// Attempt is one record of one try at a unit: which rung ran it, who owned it,
// when it started, how it ended, and whether it proved termination.
type Attempt struct {
	N        int64
	Rung     string
	Owner    string
	Started  string
	Outcome  string
	HasProof bool
	Proof    Form
}

// Tool is one verb a unit needs installed, at a version, with an optional
// semantic key: when the key moves the unit's outputs are invalid even though
// the version did not change.
type Tool struct {
	Verb string
	At   string
	Key  string
}

// Collection is one output collection: named before the run, its members known
// only after it.
type Collection struct {
	Name           string
	Under          string
	MembersUnknown bool
}

// WarmState is the retained/active split. Retained entries are held between
// units (a kept worktree, a loaded compiler); active entries are what the unit
// is using while it runs. They are accounted apart so warmth is never charged
// as running capacity.
type WarmState struct {
	Retained []Form
	Active   []Form
}

// Criterion is one acceptance criterion, in the schema the work spec fixes.
type Criterion struct {
	ID        string
	Kind      string
	Subject   string
	Predicate string
}

// checkUnitField validates the shape of one key. Only the keys this reader owns
// are checked; everything else is data it carries.
func checkUnitField(file, id string, key, val Form) error {
	switch key.Value {
	case "state":
		if !isOneOf(val, knownStates) {
			return refuse(file, fmt.Sprintf(
				":state %s in unit %q is not one of %s; refusing to guess",
				renderVal(val), id, strings.Join(knownStates, ", ")))
		}
	case "owner":
		if val.Kind != String || val.Value == "" {
			return refuse(file, fmt.Sprintf(
				`:owner in unit %q must name one mind as a string -- a friend ("Emma"), a child rung ("child:opus"), a swarm ("swarm:flash") or "all"`,
				id))
		}
	case "resources":
		return checkResources(file, id, val)
	case "writes":
		return checkWrites(file, id, val)
	case "tools":
		return checkTools(file, id, val)
	case "collects":
		return checkCollections(file, id, val)
	case "warm":
		return checkWarm(file, id, val)
	case "attempts":
		return checkAttempts(file, id, val)
	case "acceptance":
		return checkAcceptance(file, id, val)
	}
	return nil
}

func checkResources(file, id string, val Form) error {
	if val.Kind != List {
		return refuse(file, fmt.Sprintf(
			":resources in unit %q must be a list of (:<name> <value>) entries", id))
	}
	lanes := 0
	for _, entry := range val.List {
		if entry.Kind != List || len(entry.List) == 0 || entry.List[0].Kind != Keyword {
			return refuse(file, fmt.Sprintf(
				":resources in unit %q holds an entry at byte=%d that is not a (:<name> <value>) list; an admission request is never a bare token",
				id, entry.Offset))
		}
		name := entry.List[0].Value
		if len(entry.List) < 2 {
			return refuse(file, fmt.Sprintf(
				":%s in unit %q carries no value; refusing to guess", name, id))
		}
		switch name {
		case "lane":
			lanes++
			if entry.List[1].Kind != String || entry.List[1].Value == "" {
				return refuse(file, fmt.Sprintf(
					":lane in unit %q must name one lane of the lanes file as a string", id))
			}
		case "class":
			if entry.List[1].Kind != String || entry.List[1].Value == "" {
				return refuse(file, fmt.Sprintf(
					":class in unit %q must name the scarce class as a string", id))
			}
			if _, ok := plistInt(entry.List[2:], "n"); !ok {
				return refuse(file, fmt.Sprintf(
					":class in unit %q needs :n <integer>; a class with no count admits nothing", id))
			}
		default:
			if entry.List[1].Kind != Integer {
				return refuse(file, fmt.Sprintf(
					":%s in unit %q must carry an integer count, not %s; a resource vector is numbers",
					name, id, renderVal(entry.List[1])))
			}
			_ = numericResources[name] // named scarce resources are admitted too
		}
	}
	if lanes > 1 {
		return refuse(file, fmt.Sprintf(
			"unit %q names :lane %d times in :resources; a lane is a resource of capacity 1 over ONE area of the tree",
			id, lanes))
	}
	return nil
}

func checkWrites(file, id string, val Form) error {
	if val.Kind != List {
		return refuse(file, fmt.Sprintf(
			":writes in unit %q must be a list of repo-relative paths", id))
	}
	for _, p := range val.List {
		if p.Kind != String || p.Value == "" {
			return refuse(file, fmt.Sprintf(
				":writes in unit %q holds %s at byte=%d; every write is a path string",
				id, renderVal(p), p.Offset))
		}
		if strings.HasPrefix(p.Value, "/") {
			return refuse(file, fmt.Sprintf(
				":writes in unit %q names the absolute path %q; a write is relative to the unit's repo",
				id, p.Value))
		}
		if p.Value == ".." || strings.HasPrefix(p.Value, "../") || strings.Contains(p.Value, "/../") {
			return refuse(file, fmt.Sprintf(
				":writes in unit %q escapes its repo with %q", id, p.Value))
		}
	}
	return nil
}

func checkTools(file, id string, val Form) error {
	if val.Kind != List {
		return refuse(file, fmt.Sprintf(
			":tools in unit %q must be a list of (:<verb> :at \"<version>\") entries", id))
	}
	for _, entry := range val.List {
		if entry.Kind != List || len(entry.List) == 0 || entry.List[0].Kind != Keyword {
			return refuse(file, fmt.Sprintf(
				":tools in unit %q holds an entry at byte=%d that does not name a verb", id, entry.Offset))
		}
		if _, ok := plistString(entry.List[1:], "at"); !ok {
			return refuse(file, fmt.Sprintf(
				":%s in unit %q needs :at \"<version>\"; a tool with no version pins nothing",
				entry.List[0].Value, id))
		}
	}
	return nil
}

func checkCollections(file, id string, val Form) error {
	if val.Kind != List {
		return refuse(file, fmt.Sprintf(
			":collects in unit %q must be a list of (:name ... :under ...) entries", id))
	}
	for _, entry := range val.List {
		if entry.Kind != List {
			return refuse(file, fmt.Sprintf(
				":collects in unit %q holds a non-list entry at byte=%d", id, entry.Offset))
		}
		if _, ok := plistString(entry.List, "name"); !ok {
			return refuse(file, fmt.Sprintf(
				":collects in unit %q needs :name \"<collection>\"; a collection is named before the run even though its members are not",
				id))
		}
		if _, ok := plistString(entry.List, "under"); !ok {
			return refuse(file, fmt.Sprintf(
				":collects in unit %q needs :under \"<path>\"; the collection's members are gathered from there",
				id))
		}
	}
	return nil
}

func checkWarm(file, id string, val Form) error {
	if val.Kind != List {
		return refuse(file, fmt.Sprintf(
			":warm in unit %q must be (:retained (...) :active (...))", id))
	}
	if _, ok := plistList(val.List, "retained"); !ok {
		return refuse(file, fmt.Sprintf(
			":warm in unit %q needs :retained (...); warm state is accounted apart from active resources or it is charged twice",
			id))
	}
	if _, ok := plistList(val.List, "active"); !ok {
		return refuse(file, fmt.Sprintf(
			":warm in unit %q needs :active (...); the split is the point of the key", id))
	}
	return nil
}

func checkAttempts(file, id string, val Form) error {
	if val.Kind != List {
		return refuse(file, fmt.Sprintf(
			":attempts in unit %q must be a list of attempt records", id))
	}
	for _, entry := range val.List {
		if entry.Kind != List {
			return refuse(file, fmt.Sprintf(
				":attempts in unit %q holds a non-list record at byte=%d", id, entry.Offset))
		}
		if _, ok := plistInt(entry.List, "n"); !ok {
			return refuse(file, fmt.Sprintf(
				"an attempt of unit %q carries no :n; attempts are numbered records, never a counter", id))
		}
		if _, ok := plistString(entry.List, "rung"); !ok {
			return refuse(file, fmt.Sprintf(
				"an attempt of unit %q carries no :rung; the rung that ran it is evidence the ladder reads", id))
		}
		outcome, ok := plistWord(entry.List, "outcome")
		if !ok {
			return refuse(file, fmt.Sprintf(
				"an attempt of unit %q carries no :outcome; refusing to guess", id))
		}
		if !oneOfWord(outcome, knownOutcomes) {
			return refuse(file, fmt.Sprintf(
				":outcome %s in unit %q is not one of %s; refusing to guess",
				outcome, id, strings.Join(knownOutcomes, ", ")))
		}
		_, hasProof := plistAny(entry.List, "proof")
		if !hasProof && outcome != "uncertain" {
			return refuse(file, fmt.Sprintf(
				"attempt of unit %q ends :%s with no :proof of termination; an attempt that cannot prove it stopped is :outcome :uncertain and keeps its resources",
				id, outcome))
		}
	}
	return nil
}

func checkAcceptance(file, id string, val Form) error {
	if val.Kind != List || len(val.List) == 0 {
		return refuse(file, fmt.Sprintf(
			":acceptance in unit %q must be a non-empty list of criteria; a unit naming no evidence names no finish line",
			id))
	}
	for _, entry := range val.List {
		// TWO spellings, ONE key, one reader. A coordinator writes acceptance either
		// as prose lines -- `:acceptance ("a line in notes.md" "a test")`, which the
		// ask side has always read into Unit.Acceptance -- or as the amendment's
		// typed criteria, which Criteria() reads from this same value. A string
		// member is the first spelling and owes this checker nothing. A LIST member
		// claims to be a criterion, and a criterion missing :id, :subject, :kind or
		// :predicate is not one.
		if entry.Kind == String {
			continue
		}
		if entry.Kind != List {
			return refuse(file, fmt.Sprintf(
				":acceptance in unit %q holds a criterion at byte=%d that is neither a line of prose nor a (:id ... :kind ... :subject ... :predicate ...) form",
				id, entry.Offset))
		}
		if _, ok := plistString(entry.List, "id"); !ok {
			return refuse(file, fmt.Sprintf("a criterion of unit %q carries no :id", id))
		}
		if _, ok := plistString(entry.List, "subject"); !ok {
			return refuse(file, fmt.Sprintf("a criterion of unit %q carries no :subject", id))
		}
		kind, ok := plistWord(entry.List, "kind")
		if !ok || !oneOfWord(kind, knownAcceptanceKinds) {
			return refuse(file, fmt.Sprintf(
				":kind %s in an acceptance criterion of unit %q is not one of %s",
				kind, id, strings.Join(knownAcceptanceKinds, ", ")))
		}
		predicate, ok := plistWord(entry.List, "predicate")
		if !ok || !oneOfWord(predicate, knownAcceptancePredicates) {
			return refuse(file, fmt.Sprintf(
				":predicate %s in an acceptance criterion of unit %q is not one of %s",
				predicate, id, strings.Join(knownAcceptancePredicates, ", ")))
		}
	}
	return nil
}

// checkLaneAgrees refuses a unit that names its area twice and differently: the
// old `:lane "docs"` spelling and the vector's `(:lane "work")` are one
// capacity-1 reservation, so they must be the same lane.
func checkLaneAgrees(file string, u Unit) error {
	short, hasShort := "", false
	if f, ok := u.Fields["lane"]; ok && f.Kind == String {
		short, hasShort = f.Value, true
	}
	vector, hasVector := laneOfResources(u)
	if hasShort && hasVector && short != vector {
		return refuse(file, fmt.Sprintf(
			"unit %q: :lane %q and :resources (:lane %q) disagree; one unit sits in one area of the tree",
			u.ID, short, vector))
	}
	return nil
}

func laneOfResources(u Unit) (string, bool) {
	res, ok := u.Fields["resources"]
	if !ok || res.Kind != List {
		return "", false
	}
	for _, entry := range res.List {
		if entry.Kind == List && len(entry.List) >= 2 &&
			entry.List[0].IsKeyword("lane") && entry.List[1].Kind == String {
			return entry.List[1].Value, true
		}
	}
	return "", false
}

// Resources returns the unit's resource vector as counts. A lane is capacity 1
// under the key "lane"; a named scarce class is "class:<name>"; every other
// entry keeps its own name. Admission against these counts is the kernel's, not
// this reader's.
func (u Unit) Resources() map[string]int64 {
	out := map[string]int64{}
	res, ok := u.Fields["resources"]
	if !ok || res.Kind != List {
		if u.Lane != "" {
			out["lane"] = 1
		}
		return out
	}
	for _, entry := range res.List {
		if entry.Kind != List || len(entry.List) < 2 || entry.List[0].Kind != Keyword {
			continue
		}
		switch name := entry.List[0].Value; name {
		case "lane":
			out["lane"] = 1
		case "class":
			if n, ok := plistInt(entry.List[2:], "n"); ok {
				out["class:"+entry.List[1].Value] = n
			}
		default:
			if entry.List[1].Kind == Integer {
				out[name] = entry.List[1].Int
			}
		}
	}
	return out
}

// Writes returns the repo-relative paths the unit edits, in source order.
func (u Unit) Writes() []string { return u.strings("writes") }

// State returns the unit's state, "open" when it carries none. `uncertain` is a
// state of its own.
func (u Unit) State() string {
	if f, ok := u.Fields["state"]; ok && (f.Kind == Keyword || f.Kind == Symbol) {
		return f.Value
	}
	return "open"
}

// Attempts returns the unit's attempt records in source order.
func (u Unit) Attempts() []Attempt {
	f, ok := u.Fields["attempts"]
	if !ok || f.Kind != List {
		return nil
	}
	var out []Attempt
	for _, entry := range f.List {
		if entry.Kind != List {
			continue
		}
		a := Attempt{}
		a.N, _ = plistInt(entry.List, "n")
		a.Rung, _ = plistString(entry.List, "rung")
		a.Owner, _ = plistString(entry.List, "owner")
		a.Started, _ = plistString(entry.List, "started")
		a.Outcome, _ = plistWord(entry.List, "outcome")
		if proof, ok := plistAny(entry.List, "proof"); ok {
			a.HasProof, a.Proof = true, proof
		}
		out = append(out, a)
	}
	return out
}

// Tools returns the verbs the unit needs installed, with their versions and
// optional semantic keys.
func (u Unit) Tools() []Tool {
	f, ok := u.Fields["tools"]
	if !ok || f.Kind != List {
		return nil
	}
	var out []Tool
	for _, entry := range f.List {
		if entry.Kind != List || len(entry.List) == 0 || entry.List[0].Kind != Keyword {
			continue
		}
		t := Tool{Verb: entry.List[0].Value}
		t.At, _ = plistString(entry.List[1:], "at")
		t.Key, _ = plistString(entry.List[1:], "key")
		out = append(out, t)
	}
	return out
}

// Collections returns the unit's output collections.
func (u Unit) Collections() []Collection {
	f, ok := u.Fields["collects"]
	if !ok || f.Kind != List {
		return nil
	}
	var out []Collection
	for _, entry := range f.List {
		if entry.Kind != List {
			continue
		}
		c := Collection{}
		c.Name, _ = plistString(entry.List, "name")
		c.Under, _ = plistString(entry.List, "under")
		if members, ok := plistWord(entry.List, "members"); ok {
			c.MembersUnknown = members == "unknown-before-run"
		}
		out = append(out, c)
	}
	return out
}

// Warm returns the retained/active split of the unit's warm state.
func (u Unit) Warm() WarmState {
	var w WarmState
	f, ok := u.Fields["warm"]
	if !ok || f.Kind != List {
		return w
	}
	if retained, ok := plistList(f.List, "retained"); ok {
		w.Retained = retained
	}
	if active, ok := plistList(f.List, "active"); ok {
		w.Active = active
	}
	return w
}

// Criteria returns the unit's acceptance CRITERIA: the typed evidence that closes
// it. A unit with none names no finish line.
func (u Unit) Criteria() []Criterion {
	f, ok := u.Fields["acceptance"]
	if !ok || f.Kind != List {
		return nil
	}
	var out []Criterion
	for _, entry := range f.List {
		if entry.Kind != List {
			continue
		}
		c := Criterion{}
		c.ID, _ = plistString(entry.List, "id")
		c.Kind, _ = plistWord(entry.List, "kind")
		c.Subject, _ = plistString(entry.List, "subject")
		c.Predicate, _ = plistWord(entry.List, "predicate")
		out = append(out, c)
	}
	return out
}

func (u Unit) strings(key string) []string {
	f, ok := u.Fields[key]
	if !ok || f.Kind != List {
		return nil
	}
	var out []string
	for _, item := range f.List {
		if item.Kind == String {
			out = append(out, item.Value)
		}
	}
	return out
}

// plist helpers read a keyword/value plist inside one form. They never evaluate
// and never guess: a missing key is reported as missing.

func plistAny(body []Form, key string) (Form, bool) {
	for i := 0; i+1 < len(body); i++ {
		if body[i].IsKeyword(key) {
			return body[i+1], true
		}
	}
	return Form{}, false
}

func plistString(body []Form, key string) (string, bool) {
	f, ok := plistAny(body, key)
	if !ok || f.Kind != String || f.Value == "" {
		return "", false
	}
	return f.Value, true
}

func plistInt(body []Form, key string) (int64, bool) {
	f, ok := plistAny(body, key)
	if !ok || f.Kind != Integer {
		return 0, false
	}
	return f.Int, true
}

// plistWord reads a value written as a keyword or a bare symbol: the grammar
// admits `:outcome :green` and `:outcome green` alike, because both are data.
func plistWord(body []Form, key string) (string, bool) {
	f, ok := plistAny(body, key)
	if !ok || (f.Kind != Keyword && f.Kind != Symbol) {
		return "", false
	}
	return f.Value, true
}

func plistList(body []Form, key string) ([]Form, bool) {
	f, ok := plistAny(body, key)
	if !ok || f.Kind != List {
		return nil, false
	}
	return f.List, true
}

func isOneOf(f Form, set []string) bool {
	if f.Kind != Keyword && f.Kind != Symbol {
		return false
	}
	return oneOfWord(f.Value, set)
}

func oneOfWord(word string, set []string) bool {
	for _, s := range set {
		if s == word {
			return true
		}
	}
	return false
}
