// Package jev is Jev's mechanical passes over one pull request (nova-tools
// #3631): lint (every typed body line present, once, in its one form), scope
// (every changed file inside PATHS) and base (the PR targets a trunk, the one
// its body names). No model and no judgement: a friend's attention goes to
// what a regular expression cannot decide, and these passes run first, on
// every PR, before any friend read.
//
// The passes make ONE typed line, recorded on the PR record's reads
// (pr:<name>:<n>, internal/nsprint/land/stream) by `nova-sprint jev mech`:
//
//	JEV who=jev pass=mech head=<sha> gate=ok|fail lint=<w> scope=<w> base=<w> why=<one line>
//
// where <w> is ok, fail or missing. missing is not fail: a pass with no
// evidence to decide on (the mirror has no head yet, the record names no
// base) says so and does not hold the PR.
//
// A JEV line is never a read. It carries no SCORE, DISPOSITION or HOLD word,
// its who is jev and stream.ReadAt skips every who=jev* line, so only a
// friend's line at 8+ lands a PR. The stream lander reads the JEV line as a
// gate instead (Skip): a gate=fail line at head keeps the PR out of a batch,
// and cfg:land jev turns the gate off, on (the default) or to require.
package jev

import (
	"path"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pr"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Who is the name on every JEV line. It is not a friend's name.
const Who = "jev"

// Kind is the pass field of the mechanical line. Other Jev lines (the
// nova-decide review JEV line) carry no pass=mech and are not this line.
const Kind = "mech"

// Word is one pass's answer.
type Word string

// The three answers.
const (
	OK      Word = "ok"
	Fail    Word = "fail"
	Missing Word = "missing"
)

// Check is one pass's answer and the reason it rests on.
type Check struct {
	Word Word
	Why  string
}

// Passes are the passes in the order the line prints them.
var Passes = []string{"lint", "scope", "base"}

// Trunks are the bases a PR may target; any other base is a stacked PR.
var Trunks = map[string]bool{"dev": true, "main": true}

// Body is a PR body's key lines: each `KEY: value` line at the start of a
// line (markdown bullets and bold allowed), the key upper-cased, every value
// in order; and the issue references its Closes lines name.
type Body struct {
	Keys   map[string][]string
	Closes []string
}

var (
	keyLineRx = regexp.MustCompile(`^([A-Za-z][A-Za-z-]*):[ \t]*(.*)$`)
	closesRx  = regexp.MustCompile(`(?i)^(?:closes|fixes|resolves)[ \t]+((?:[A-Za-z0-9_.-]+/)?[A-Za-z0-9_.-]*#[0-9]+)[ \t.]*$`)
	shaRx     = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	wordRx    = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	refRx     = regexp.MustCompile(`^(?:[A-Za-z0-9_.-]+/)?[A-Za-z0-9_.-]*#[0-9]+$`)
	parenRx   = regexp.MustCompile(`\([^)]*\)`)
)

// ParseBody reads the key lines out of a PR body.
func ParseBody(body string) Body {
	b := Body{Keys: map[string][]string{}}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimSpace(strings.ReplaceAll(line, "**", ""))
		if m := closesRx.FindStringSubmatch(line); m != nil {
			b.Closes = append(b.Closes, m[1])
			continue
		}
		if m := keyLineRx.FindStringSubmatch(line); m != nil {
			k := strings.ToUpper(m[1])
			b.Keys[k] = append(b.Keys[k], strings.TrimSpace(m[2]))
		}
	}
	return b
}

// Value is the key's one value: "" when the body has no such line.
func (b Body) Value(key string) string {
	if v := b.Keys[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// field is one required body line: the key as ParseBody stores it, the key
// as a refusal names it, and the check of its one form ("" is the form).
type field struct {
	key, show string
	form      func(v string) string
}

// fields are the typed body lines every PR carries, in the order a refusal
// names them (ops brief: BASE, base-sha, PATHS, DEPENDS-ON, DONE-WHEN,
// STREAM, then Closes).
var fields = []field{
	{"BASE", "BASE:", func(v string) string {
		if !wordRx.MatchString(v) {
			return "BASE: " + v + " is not one branch name"
		}
		return ""
	}},
	{"BASE-SHA", "BASE-SHA:", func(v string) string {
		if !shaRx.MatchString(v) {
			return "BASE-SHA: " + v + " is not a 7-40 hex sha"
		}
		return ""
	}},
	{"PATHS", "PATHS:", func(v string) string {
		if _, err := CanonPaths(v); err != nil {
			return "PATHS: does not parse (" + err.Error() + ")"
		}
		return ""
	}},
	{"DEPENDS-ON", "DEPENDS-ON:", dependsForm},
	{"DONE-WHEN", "DONE-WHEN:", dashForm("DONE-WHEN:")},
	{"STREAM", "STREAM:", dashForm("STREAM:")},
}

func dashForm(show string) func(string) string {
	return func(v string) string {
		if v == "-" {
			return show + " - is a placeholder, not a value"
		}
		return ""
	}
}

// dependsForm is DEPENDS-ON's one form: none, or one or more issue or PR
// references (owner/name#n, name#n, #n) separated by commas, either one
// optionally followed by a parenthesised WHY.
func dependsForm(v string) string {
	s := strings.TrimSpace(v)
	if i := strings.Index(s, "("); i >= 0 && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(s[:i])
	}
	if s == "none" {
		return ""
	}
	for _, r := range strings.Split(s, ",") {
		if !refRx.MatchString(strings.TrimSpace(r)) {
			return "DEPENDS-ON: " + v + " is not none or owner/name#n[, ...]"
		}
	}
	return ""
}

// CanonPaths reads a PATHS line: parenthesised notes such as (new) go, and
// what is left is the one path list internal/nsprint/pr canonicalises.
func CanonPaths(line string) ([]string, error) {
	return pr.CanonPaths(parenRx.ReplaceAllString(line, " "))
}

// Lint is every typed body line present, once (a second line with another
// value is two forms), and in its one form; and a Closes #<n> line or an
// ORIGIN: line naming where the work came from.
func Lint(b Body) Check {
	var missing, bad []string
	for _, f := range fields {
		vals := b.Keys[f.key]
		if len(vals) == 0 || vals[0] == "" {
			missing = append(missing, f.show)
			continue
		}
		for _, v := range vals[1:] {
			if v != vals[0] {
				bad = append(bad, "two "+f.show+" lines")
				break
			}
		}
		if why := f.form(vals[0]); why != "" {
			bad = append(bad, why)
		}
	}
	if len(b.Closes) == 0 && b.Value("ORIGIN") == "" {
		missing = append(missing, "Closes #<n>")
	}
	var why []string
	if len(missing) > 0 {
		why = append(why, "missing "+strings.Join(missing, " "))
	}
	why = append(why, bad...)
	if len(why) > 0 {
		return Check{Fail, strings.Join(why, ", ")}
	}
	return Check{OK, "every body line present in its one form"}
}

// Scope is every changed file inside PATHS: an entry covers the file it
// names, every file under it when it is a directory, and what it matches
// when it is a glob. known is false when the diff could not be read, and
// then the pass is missing with why.
func Scope(paths string, files []string, known bool, why string) Check {
	if !known {
		return Check{Missing, "no diff: " + why}
	}
	if strings.TrimSpace(paths) == "" {
		return Check{Missing, "no PATHS on the body or the record"}
	}
	canon, err := CanonPaths(paths)
	if err != nil {
		return Check{Fail, "the PATHS line does not parse: " + err.Error()}
	}
	var out []string
	for _, f := range files {
		if !covered(canon, f) {
			out = append(out, f)
		}
	}
	if len(out) > 0 {
		return Check{Fail, strings.Join(out, " ") + " outside PATHS"}
	}
	return Check{OK, "every changed file inside PATHS"}
}

func covered(paths []string, f string) bool {
	for _, p := range paths {
		if f == p || strings.HasPrefix(f, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
		if strings.ContainsAny(p, "*?[") {
			if ok, _ := path.Match(p, f); ok {
				return true
			}
		}
	}
	return false
}

// Base is the PR targeting a trunk, the base its body names, cut from the
// base-sha its record holds. recBase and recBaseSHA are the record's base
// and base_sha ("" unknown).
func Base(b Body, recBase, recBaseSHA string) Check {
	bodyBase, bodySHA := b.Value("BASE"), strings.ToLower(b.Value("BASE-SHA"))
	base := recBase
	if base == "" {
		base = bodyBase
	}
	if base == "" {
		return Check{Missing, "no base on the record or the body"}
	}
	var why []string
	if !Trunks[base] {
		why = append(why, "base "+base+" is not dev or main (a stacked PR)")
	}
	if bodyBase != "" && recBase != "" && bodyBase != recBase {
		why = append(why, "the body says BASE: "+bodyBase+" but the PR targets "+recBase)
	}
	rs := strings.ToLower(recBaseSHA)
	if bodySHA != "" && rs != "" && !strings.HasPrefix(rs, bodySHA) && !strings.HasPrefix(bodySHA, rs) {
		why = append(why, "the body's base-sha "+bodySHA+" is not the record's base_sha "+rs)
	}
	if len(why) > 0 {
		return Check{Fail, strings.Join(why, ", ")}
	}
	return Check{OK, "targets " + base}
}

// Input is what the passes read: the record's head, base, base_sha and
// PATHS, the body, and the changed files at head (FilesKnown false when the
// mirror could not say, FilesWhy the reason).
type Input struct {
	Head, Base, BaseSHA, Paths string
	Body                       string
	Files                      []string
	FilesKnown                 bool
	FilesWhy                   string
}

// Line is one JEV mech line.
type Line struct {
	Head              string
	Lint, Scope, Base Check
	// Why is the why= text as parsed; Mech and String build it from the
	// checks.
	Why string
}

// Mech runs the three passes. Scope reads the body's PATHS, else the
// record's.
func Mech(in Input) Line {
	b := ParseBody(in.Body)
	paths := b.Value("PATHS")
	if paths == "" {
		paths = in.Paths
	}
	l := Line{
		Head:  strings.ToLower(strings.TrimSpace(in.Head)),
		Lint:  Lint(b),
		Scope: Scope(paths, in.Files, in.FilesKnown, in.FilesWhy),
		Base:  Base(b, in.Base, in.BaseSHA),
	}
	l.Why = l.why()
	return l
}

func (l Line) checks() []Check { return []Check{l.Lint, l.Scope, l.Base} }

// Gate is fail when any pass failed, else ok.
func (l Line) Gate() Word {
	for _, c := range l.checks() {
		if c.Word == Fail {
			return Fail
		}
	}
	return OK
}

// Failed names the passes that failed, among those gating (nil: every pass).
func (l Line) Failed(gating map[string]bool) []string {
	var out []string
	for i, c := range l.checks() {
		if c.Word == Fail && (gating == nil || gating[Passes[i]]) {
			out = append(out, Passes[i])
		}
	}
	return out
}

func (l Line) why() string {
	var parts []string
	for i, c := range l.checks() {
		if c.Word != OK {
			parts = append(parts, Passes[i]+": "+c.Why)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "; ")
}

// String is the typed line. why is prose, escaped to one line, with no "="
// (so no key=value a line reader could take for a field) and no upper-case
// verdict word.
func (l Line) String() string {
	word := func(c Check) string {
		if c.Word == "" {
			return string(Missing)
		}
		return string(c.Word)
	}
	why := l.Why
	if why == "" {
		why = l.why()
	}
	return "JEV who=" + Who + " pass=" + Kind + " head=" + oneline.Field(l.Head) + " gate=" + string(l.Gate()) +
		" lint=" + word(l.Lint) + " scope=" + word(l.Scope) + " base=" + word(l.Base) + " why=" + prose(why)
}

var verdictRx = regexp.MustCompile(`(?i)\b(approve|hold)`)

func prose(s string) string {
	s = strings.ReplaceAll(oneline.Escape(oneline.Cap(s, 400)), "=", `\x3d`)
	s = verdictRx.ReplaceAllStringFunc(s, strings.ToLower)
	s = strings.ReplaceAll(s, "`", "'")
	return strings.ReplaceAll(s, "**", "*")
}

// Parse reads one JEV mech line. Any other line, a JEV line of another
// pass included, is not one.
func Parse(s string) (Line, bool) {
	s = strings.TrimSpace(s)
	head, why, _ := strings.Cut(s, " why=")
	f := strings.Fields(head)
	if len(f) == 0 || f[0] != "JEV" {
		return Line{}, false
	}
	kv := map[string]string{}
	for _, w := range f[1:] {
		if k, v, ok := strings.Cut(w, "="); ok {
			kv[k] = v
		}
	}
	if kv["pass"] != Kind || !strings.HasPrefix(kv["who"], Who) || kv["head"] == "" {
		return Line{}, false
	}
	w := func(k string) Check {
		switch Word(kv[k]) {
		case OK, Fail:
			return Check{Word: Word(kv[k])}
		}
		return Check{Word: Missing}
	}
	return Line{Head: strings.ToLower(kv["head"]), Lint: w("lint"), Scope: w("scope"), Base: w("base"), Why: why}, true
}

// At is the last JEV mech line at head (its head a prefix of at least 7 hex
// digits of head, as stream.ReadAt counts a read).
func At(lines []string, head string) (Line, bool) {
	head = strings.ToLower(strings.TrimSpace(head))
	var last Line
	found := false
	for _, s := range lines {
		l, ok := Parse(s)
		if !ok || len(l.Head) < 7 || head == "" || !strings.HasPrefix(head, l.Head) {
			continue
		}
		last, found = l, true
	}
	return last, found
}

// Mode is how the lander reads the JEV line: cfg:land jev.
type Mode string

// The modes. Gate is the default: a failing JEV line at head keeps the PR
// out; a PR with no JEV line yet is not held. Require holds that PR too.
const (
	ModeOff     Mode = "off"
	ModeGate    Mode = "gate"
	ModeRequire Mode = "require"
)

// ParseMode reads cfg:land jev; empty or unknown is ModeGate.
func ParseMode(s string) Mode {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeOff:
		return ModeOff
	case ModeRequire:
		return ModeRequire
	}
	return ModeGate
}

// ParseGating reads cfg:land jev_passes, a comma list of passes that gate;
// empty is every pass (nil). A pass whose measured precision falls is turned
// off here, by config, not by hand.
func ParseGating(s string) map[string]bool {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	out := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out[p] = true
		}
	}
	return out
}

// Skip is the lander's why for a record's lines at head: "" when the JEV
// gate lets the PR through, jev:<passes> when a gating pass failed at head,
// no-jev-at-head under ModeRequire when Jev has not run at head.
func Skip(lines []string, head string, mode Mode, gating map[string]bool) string {
	if mode == ModeOff {
		return ""
	}
	l, ok := At(lines, head)
	if !ok {
		if mode == ModeRequire {
			return "no-jev-at-head"
		}
		return ""
	}
	if failed := l.Failed(gating); len(failed) > 0 {
		return "jev:" + strings.Join(failed, ",")
	}
	return ""
}
