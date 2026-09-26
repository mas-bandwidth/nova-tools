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
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/redis/go-redis/v9"
)

// The routes a card can take out of waiting: a swarm tier (the bench harness
// picks its model from routes.yaml by it) or a friend's ready queue.
const (
	RoutePro    = cardhdr.RoutePro
	RouteFlash  = cardhdr.RouteFlash
	RouteFriend = "friend"
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
	goTestRE  = regexp.MustCompile("go test (?:-[a-z]+(?:=\\S+)? )*(\\./\\S+)\\s+(?:-[a-z]+(?:=\\S+)? )*-run[ =]'?\\^?(Test[A-Za-z0-9_]*)")
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
	if s.Test == "" {
		s.Test = "none"
		if m := goTestRE.FindStringSubmatch(s.DoneWhen); m != nil {
			s.Test = m[1] + " " + m[2]
		}
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
	if s.Route != RoutePro && s.Route != RouteFlash {
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
	return missing
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

// Record reads one task record: one HGETALL. A missing record is a *Refused.
func Record(ctx context.Context, c redis.Cmdable, id string) (map[string]string, error) {
	recs, err := Records(ctx, c, id)
	if err != nil {
		return nil, err
	}
	return recs[0], nil
}

// Records reads the named records in one pipeline of HGETALLs.
func Records(ctx context.Context, c redis.Cmdable, ids ...string) ([]map[string]string, error) {
	pipe := c.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGetAll(ctx, Key(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	out := make([]map[string]string, len(ids))
	for i, cmd := range cmds {
		m := cmd.Val()
		if len(m) == 0 {
			return nil, &Refused{Why: "NOTASK " + Key(ids[i])}
		}
		out[i] = m
	}
	return out, nil
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

// RenderHeader is the harness card a bench runs, rendered from the record
// alone: the RESULT contract line, the typed header, the standard lines and
// DO: with the issue text. It refuses (naming the header keys) a record a
// swarm run could not start from; the rendered card passes card.Lint.
func RenderHeader(id string, rec map[string]string) ([]byte, error) {
	s := specOf(rec)
	missing := s.Complete(rec["ref"], rec["origin"])
	if s.Route != RoutePro && s.Route != RouteFlash {
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
	line("NO-SUBAGENTS", "work in this session only; do not spawn an Explore, Task or child agent.")
	line("WALL", "read and write only inside the job dir; the repo clone is under it; never read $HOME or ~/rowan-working outside the job dir (the job dir itself sits under ~/rowan-working/tmp, so never walk above it); a read outside is refused by the wall and ends the card.")
	line("RESULT-FORMAT", fmt.Sprintf("RESULT.md in the job dir, outside repo/: line 1 is line 1 of this card verbatim; line 2 is DONE, ABSTAIN <why> or BLOCKED <why>; then SCHEMA: v2, KIND: %s, ATTEMPT: 1, CHECK: pass|fail|not-run, REPO: %s, BRANCH: <branch you committed>, PATHS: <space-separated paths changed>, RED: <the test failing on base-sha>, GREEN: <the same test passing at your head>; then a \"## Gates\" section and a \"## Left owed\" section, each with at least one \"- \" row.", kind, s.Repo))
	line("COMMIT", fmt.Sprintf("make your change in repo/ on a new branch %s (git checkout -b %s) and commit it there with the DONE-WHEN summary as the first line; never push and never open a PR; harvest pushes the branch and opens the PR from your commit; an uncommitted change counts as NO-COMMIT and the card is refused.", branch, branch))
	line("UNATTENDED", "never ask a question and never offer to proceed; decide, and record the decision in RESULT.md.")
	line("OUTPUT", "RESULT.md, notes and scratch go in the job directory, outside repo/, and are never committed; the harness moves them to the results root at card end and deletes the job directory.")
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
