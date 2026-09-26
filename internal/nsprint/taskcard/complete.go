package taskcard

// A waiting card is complete (nova-tools#3911, Glenn 2026-09-25 11:05 AM ET:
// "Moving cards from waiting to a friend's ready queue, or waiting to the
// fleet, should be near instant"). The record IS the card: push fills every
// field a friend or the swarm needs from the issue text, by one parser
// (ParseIssue, reading each line by card push's own header rule), and refuses
// a swarm card that lacks one, naming it. The harness header a bench runs
// (RenderHeader) and the brief a friend reads (RenderBrief) are rendered from
// the record with no generation step, and the deal is one move per card
// (Deal: one pipeline of the one move).

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// The routes a card can take out of waiting: a swarm tier (the bench harness
// picks its model from routes.yaml by it) or a friend's ready queue.
const (
	RouteFrontier = cardhdr.RouteFrontier
	RoutePro      = cardhdr.RoutePro
	RouteFlash    = cardhdr.RouteFlash
	RouteFriend   = "friend"
)

// Spec is the card content a record carries beside its pointer: every field
// the harness header and the friend brief are rendered from. Field names on
// the record are specFields' second column.
type Spec struct {
	Route, Who, Kind, Type, Repo, Base, BaseSHA, Paths, Test string
	DependsOn, DoneWhen, Est, Priority, Source, Task, Body   string
	Stream, Origin                                           string // header lines that fill the push's own options
}

// specFields maps each header key an issue may carry to the record field it
// fills. STREAM and ORIGIN fill the push's stream option and origin field.
var specFields = []struct{ key, field string }{
	{"ROUTE", "route"}, {"WHO", "who"}, {"KIND", "kind"}, {"TYPE", "type"}, {"REPO", "repo"},
	{"BASE", "base"}, {"base-sha", "base_sha"}, {"PATHS", "paths"}, {"TEST", "test"},
	{"DEPENDS-ON", "depends_on"}, {"DONE-WHEN", "done_when"}, {"EST", "est"}, {"PRIORITY", "priority"},
	{"SOURCE", "source"}, {"TASK", "task"}, {"STREAM", "stream"}, {"ORIGIN", "origin"},
}

func (s *Spec) slot(field string) *string {
	switch field {
	case "route":
		return &s.Route
	case "who":
		return &s.Who
	case "kind":
		return &s.Kind
	case "type":
		return &s.Type
	case "repo":
		return &s.Repo
	case "base":
		return &s.Base
	case "base_sha":
		return &s.BaseSHA
	case "paths":
		return &s.Paths
	case "test":
		return &s.Test
	case "depends_on":
		return &s.DependsOn
	case "done_when":
		return &s.DoneWhen
	case "est":
		return &s.Est
	case "priority":
		return &s.Priority
	case "source":
		return &s.Source
	case "task":
		return &s.Task
	case "stream":
		return &s.Stream
	case "origin":
		return &s.Origin
	case "body":
		return &s.Body
	}
	return nil
}

// ParseIssue fills a Spec from an issue's text: every line card push would
// read as a `KEY: value` header line (cardhdr.KeyValue) whose key a card
// header carries, the first of each key winning, wherever it sits in the
// text (an issue's DONE-WHEN line is usually last). The whole text is the
// body. Nothing is defaulted here; Complete does that from facts.
func ParseIssue(text string) Spec {
	var s Spec
	s.Body = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	seen := map[string]bool{}
	for _, line := range strings.Split(s.Body, "\n") {
		k, v, ok := cardhdr.KeyValue(line)
		if !ok || seen[k] {
			continue
		}
		for _, f := range specFields {
			if f.key == k {
				seen[k] = true
				*s.slot(f.field) = v
			}
		}
	}
	// BASE: dev@<sha> names both, as staging reads it (swarm.ReadCardBase).
	if ref, sha, ok := strings.Cut(s.Base, "@"); ok {
		s.Base = strings.TrimSpace(ref)
		if s.BaseSHA == "" {
			s.BaseSHA = strings.TrimSpace(sha)
		}
	}
	return s
}

var (
	shaRE     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	refRE     = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*)#[1-9][0-9]*$`)
	onelineRE = regexp.MustCompile(`\s+`)
)

// Complete fills every field a fact decides and returns the header keys a
// run on route still lacks. The facts: REPO from the ref (owner/name#n),
// TEST from a `go test <pkg> -run <TestName>` in DONE-WHEN, and the
// defaults a header states when an issue is silent (WHO any, TYPE code,
// DEPENDS-ON none, EST 30, PRIORITY 0, SOURCE issue). A swarm route (pro or
// flash) needs REPO, BASE, base-sha, PATHS, DONE-WHEN and a KIND card push
// accepts; a friend needs nothing more than the record.
func (s *Spec) Complete(ref, origin string) []string {
	for _, p := range []*string{&s.Route, &s.Who, &s.Kind, &s.Type, &s.Repo, &s.Base, &s.BaseSHA, &s.Paths,
		&s.Test, &s.DependsOn, &s.DoneWhen, &s.Est, &s.Priority, &s.Source, &s.Task} {
		*p = strings.TrimSpace(onelineRE.ReplaceAllString(*p, " "))
	}
	s.Route = strings.ToLower(s.Route)
	if s.Repo == "" {
		if m := refRE.FindStringSubmatch(ref); m != nil {
			s.Repo = m[1]
		}
	}
	derived := s.Test == ""
	if derived {
		s.Test = TestFromDoneWhen(s.DoneWhen)
	}
	def := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	def(&s.Who, "any")
	def(&s.Type, "code")
	def(&s.DependsOn, "none")
	def(&s.Est, "30")
	def(&s.Priority, "0")
	if s.Source == "" {
		s.Source = "task"
		if strings.Contains(origin, "github.com/") || refRE.MatchString(ref) {
			s.Source = "issue"
		}
	}
	if !cardhdr.IsRoute(s.Route) {
		return nil
	}
	var missing []string
	need := func(key, v string) {
		if v == "" || v == "-" {
			missing = append(missing, key)
		}
	}
	need("REPO", s.Repo)
	need("BASE", s.Base)
	if !shaRE.MatchString(s.BaseSHA) {
		missing = append(missing, "base-sha")
	}
	need("PATHS", s.Paths)
	need("DONE-WHEN", s.DoneWhen)
	if _, err := ResultKind(s.Kind); err != nil {
		missing = append(missing, "KIND")
	}
	// The card is a spec (#4313): a swarm card names the test that proves
	// it, or says why it has none, and the wrapper holds it to that. A card
	// with no TEST line whose DONE-WHEN names no test is told so, not that
	// its (derived) none says no why.
	test := s.Test
	if derived && test == "none" {
		test = ""
	}
	if _, why := cardhdr.ParseTest(test); why != "" {
		missing = append(missing, "TEST ("+why+")")
	}
	return missing
}

// TestFromDoneWhen is the TEST line a DONE-WHEN implies: `[-tags <tags>]
// <package> <TestName>` from the first `go test ... <./pkg> ... -run
// <TestName>` in the sentence, else none (with no why: the card must say
// it, cardhdr.ParseTest). The command is read as go test reads it: a flag
// that takes a value takes the next word (`-p 2`, `-tags functional`,
// `-timeout 30s`) or its own `=value`, so a value is never read as the
// package, and -tags travels to the TEST line.
func TestFromDoneWhen(doneWhen string) string {
	words := strings.Fields(doneWhen)
	for i := 0; i+1 < len(words); i++ {
		if trimWord(words[i]) != "go" || trimWord(words[i+1]) != "test" {
			continue
		}
		if t := goTestLine(words[i+2:]); t != "" {
			return t
		}
	}
	return "none"
}

// goTestValueFlags are go test's (and go build's) flags that take a value.
var goTestValueFlags = map[string]bool{
	"p": true, "tags": true, "run": true, "skip": true, "timeout": true, "count": true, "parallel": true,
	"cpu": true, "bench": true, "benchtime": true, "list": true, "shuffle": true, "fuzz": true, "fuzztime": true,
	"fuzzminimizetime": true, "coverprofile": true, "coverpkg": true, "covermode": true, "o": true, "exec": true,
	"ldflags": true, "gcflags": true, "asmflags": true, "gccgoflags": true, "mod": true, "modfile": true,
	"vet": true, "C": true, "outputdir": true, "cpuprofile": true, "memprofile": true, "memprofilerate": true,
	"blockprofile": true, "blockprofilerate": true, "mutexprofile": true, "mutexprofilefraction": true,
	"trace": true, "overlay": true, "pgo": true, "pkgdir": true, "toolexec": true, "installsuffix": true,
	"compiler": true, "buildvcs": true,
}

// goTestLine reads the words after `go test` up to the command's end (a
// word that closes a backtick or ends in ; | &): the first
// ./-relative package, -run's test name and -tags.
func goTestLine(words []string) string {
	var pkg, name, tags string
	for i := 0; i < len(words); i++ {
		w := words[i]
		last := endsCommand(w)
		w = trimWord(w)
		if strings.HasPrefix(w, "-") {
			flag, raw, hasVal := strings.Cut(strings.TrimLeft(strings.Trim(words[i], "`"), "-"), "=")
			if !hasVal && goTestValueFlags[flag] && !last && i+1 < len(words) {
				i++
				last = endsCommand(words[i])
				raw = words[i]
			}
			if openQuote(strings.Trim(raw, "`;|&")) {
				return "" // a quoted value of more than one word is not a TEST line
			}
			val := trimWord(raw)
			switch flag {
			case "run":
				name = strings.TrimSuffix(strings.TrimPrefix(strings.TrimRight(val, "."), "^"), "$")
			case "tags":
				tags = val
			}
		} else if pkg == "" && (strings.HasPrefix(w, "./") || w == ".") {
			pkg = w
		}
		if last {
			break
		}
	}
	if pkg == "" || name == "" {
		return ""
	}
	line := pkg + " " + name
	if tags != "" {
		line = "-tags " + tags + " " + line
	}
	if _, why := cardhdr.ParseTest(line); why != "" {
		return ""
	}
	return line
}

// openQuote is a word that opens a quote it does not close.
func openQuote(w string) bool {
	if w == "" || (w[0] != '\'' && w[0] != '"') {
		return false
	}
	return len(w) == 1 || w[len(w)-1] != w[0]
}

// endsCommand is a word that closes the command: a closing backtick or a
// shell separator.
func endsCommand(w string) bool {
	return strings.HasSuffix(w, "`") || strings.HasSuffix(w, ";") || strings.HasSuffix(w, "|") || strings.HasSuffix(w, "&")
}

// trimWord drops the quoting a sentence puts around a command's words.
func trimWord(w string) string {
	return strings.Trim(w, "`'\";|&,()")
}

// fields are the record's HSET pairs for the spec (never a pointer field).
func (s Spec) fields() []string {
	var out []string
	for _, f := range specFields {
		if f.field == "stream" || f.field == "origin" {
			continue
		}
		if v := *s.slot(f.field); v != "" {
			out = append(out, f.field, v)
		}
	}
	if s.Body != "" {
		out = append(out, "body", s.Body)
	}
	return out
}

// ResultKind is the header's KIND for a record's kind: a RESULT kind or a
// runner kind as it is, a classification kind by cardhdr.KindMap, and a build
// task (build, work, or no kind) as fix.
func ResultKind(kind string) (string, error) {
	switch {
	case typedrec.IsKind(kind):
		return kind, nil
	case kind == cardhdr.KindModel || kind == cardhdr.KindScript:
		return kind, nil
	case cardhdr.KindMap[kind] != "":
		return cardhdr.KindMap[kind], nil
	case kind == "" || kind == "build" || kind == "work":
		return typedrec.KindFix, nil
	}
	return "", fmt.Errorf("KIND %q is not a card kind", kind)
}

// quote renders the issue text under a header: every line behind "> ", so no
// line of it is read as a header key (card push, the typed-header lint and
// staging all read `KEY:` at column 0).
func quote(body string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		b.WriteString(strings.TrimRight("> "+l, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func dash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

// The standard lines every harness card carries (RenderHeader for a primary,
// card.RenderCopy for its work copy: the first copy-model quack's model got a
// card with the title alone and wandered, 2026-09-25 11:43 PM ET).
const (
	LineNoSubagents = "work in this session only; do not spawn an Explore, Task or child agent."
	LineWall        = "read and write only inside the job dir; the repo clone is under it; never read $HOME or ~/rowan-working outside the job dir (the job dir itself sits under ~/rowan-working/tmp, so never walk above it); a read outside is refused by the wall and ends the card."
	LineTests       = "the card is a spec: your diff adds or changes at least one test, the one TEST names, that fails at base-sha and passes at your head (a card with TEST: none <why> is excused from that, not from CI). Every new Go test opens with t.Parallel(); no time.Sleep, no real-time poll, no real deadline (inject the clock); no service, process or socket in a unit test (mock the seam; a test that needs the real thing goes behind //go:build slow); keep the package's tests under one minute. Before you commit, run `nova-ci local` in repo/ (`go run ./cmd/nova-ci local` in a nova-tools clone): exactly the unit tier CI runs for your diff, at -p 2 with the budgets; outside nova-tools test only the packages you touched (nice -n 15 go test -p 2 -count=1 <packages>); never test the whole tree. Every CI job is capped at two minutes: a red at the cap is your defect to fix, never a number to raise."
	// LineGate is the wrapper's card only (never a friend's brief, which
	// has no wrapper): what the spec gate (#4313) does with the commit.
	LineGate       = "the wrapper runs TEST at base-sha (it must fail) and at your head (it must pass) and `nova-ci local` on the touched packages before it pushes, and refuses to open the PR on a red, naming the red tests under ## Gates in RESULT.md."
	LineUnattended = "never ask a question and never offer to proceed; decide, and record the decision in RESULT.md."
	LineOutput     = "RESULT.md, notes and scratch go in the job directory, outside repo/, and are never committed; the harness moves them to the results root at card end and deletes the job directory."
)

// Quote renders an issue text as the quoted block under a card's --- line.
func Quote(body string) string { return quote(body) }

// RenderHeader is the harness card a bench runs, rendered from the record
// alone: the RESULT contract line, the typed header, the standard lines and
// DO: with the issue text. It refuses (naming the header keys) a record a
// swarm run could not start from; the rendered card passes card.Lint.
func RenderHeader(id string, rec map[string]string) ([]byte, error) {
	s := specOf(rec)
	missing := s.Complete(rec["ref"], rec["origin"])
	if !cardhdr.IsRoute(s.Route) {
		missing = append(missing, "ROUTE")
		if s.Route == "" {
			s.Route = "-"
		}
	}
	if len(missing) > 0 {
		return nil, &Refused{Why: fmt.Sprintf("INCOMPLETE task:%s route=%s lacks %s", id, s.Route, strings.Join(missing, ", "))}
	}
	kind, _ := ResultKind(s.Kind)
	task := dash(firstNonEmpty(s.Task, rec["title"]))
	branch := "rowan/" + id
	var b strings.Builder
	line := func(k, v string) { fmt.Fprintf(&b, "%s: %s\n", k, v) }
	fmt.Fprintf(&b, "RESULT: %s sha=%s\n", id, s.BaseSHA[:12])
	line("KIND", kind)
	line("TYPE", s.Type)
	line("REPO", s.Repo)
	line("BASE", s.Base)
	line("base-sha", s.BaseSHA)
	line("PATHS", s.Paths)
	line("TEST", s.Test)
	line("DEPENDS-ON", s.DependsOn)
	if st := rec["stream"]; st != "" {
		line("STREAM", st)
	}
	if o := rec["origin"]; o != "" {
		line("ORIGIN", o)
	}
	line("PRIORITY", s.Priority)
	line("WHO", s.Who)
	line("ROUTE", s.Route)
	line("EST", s.Est)
	line("SOURCE", s.Source)
	line("TASK", task)
	line("DONE-WHEN", s.DoneWhen)
	line("NO-SUBAGENTS", LineNoSubagents)
	line("WALL", LineWall)
	line("RESULT-FORMAT", fmt.Sprintf("RESULT.md in the job dir, outside repo/: line 1 is line 1 of this card verbatim; line 2 is DONE, ABSTAIN <why> or BLOCKED <why>; then SCHEMA: v2, KIND: %s, ATTEMPT: 1, CHECK: pass|fail|not-run, REPO: %s, BRANCH: <branch you committed>, PATHS: <space-separated paths changed>, RED: <the test failing on base-sha>, GREEN: <the same test passing at your head>; then a \"## Gates\" section and a \"## Left owed\" section, each with at least one \"- \" row.", kind, s.Repo))
	line("COMMIT", fmt.Sprintf("make your change in repo/ on a new branch %s (git checkout -b %s) and commit it there with the DONE-WHEN summary as the first line; never push and never open a PR; harvest pushes the branch and opens the PR from your commit; an uncommitted change counts as NO-COMMIT and the card is refused.", branch, branch))
	line("TESTS", LineTests)
	line("GATE", LineGate)
	line("UNATTENDED", LineUnattended)
	line("OUTPUT", LineOutput)
	line("PR-BODY", fmt.Sprintf("the PR body must carry `STREAM: %s` and the DONE-WHEN line above, verbatim; put both lines, verbatim, in the commit message body under line 1.", dash(rec["stream"])))
	line("DO", fmt.Sprintf("the work is the issue text quoted below (%s): change only PATHS, make DONE-WHEN hold, run TEST, write RESULT.md per RESULT-FORMAT, and exit.", dash(firstNonEmpty(rec["origin"], rec["ref"]))))
	b.WriteString("\n---\n")
	b.WriteString(quote(dash(s.Body)))
	return []byte(b.String()), nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// specOf reads a record's spec fields.
func specOf(rec map[string]string) Spec {
	var s Spec
	for _, f := range specFields {
		if f.field == "stream" || f.field == "origin" {
			continue
		}
		*s.slot(f.field) = rec[f.field]
	}
	s.Body = rec["body"]
	return s
}
