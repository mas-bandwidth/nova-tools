package jev

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// TestPromptFilesAreTheBuiltInPrompts: every built-in prompt is
// docs/jev/<type>.<version>.txt byte for byte, so a prompt change is a
// versioned file in review before it is a measured line in the report.
// JEV_PROMPTS_WRITE=1 rewrites the files from the code.
func TestPromptFilesAreTheBuiltInPrompts(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "..", "..", "docs", "jev")
	for _, typ := range Types {
		p, ok := PromptFor(typ)
		if !ok {
			continue
		}
		if !strings.HasPrefix(p.Version, typ+"-v") {
			t.Errorf("%s prompt version %q is not %s-v<n>", typ, p.Version, typ)
		}
		path := filepath.Join(dir, typ+"."+strings.TrimPrefix(p.Version, typ+"-")+".txt")
		if os.Getenv("JEV_PROMPTS_WRITE") == "1" {
			if err := os.WriteFile(path, []byte(p.Render()), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v (JEV_PROMPTS_WRITE=1 writes it)", typ, err)
			continue
		}
		if string(got) != p.Render() {
			t.Errorf("%s is not the %s prompt in code; bump the version when the prompt changes", path, typ)
		}
	}
	for _, typ := range []string{TypeWorkType, TypeTier, TypeReview, TypeReadSane} {
		if _, ok := PromptFor(typ); !ok {
			t.Errorf("decision point %s has no built-in prompt", typ)
		}
	}
}

func TestDecisionCheckRefusesWhatCannotBeARow(t *testing.T) {
	t.Parallel()

	ok := Decision{Type: TypeTier, Subject: "nova-tools-1", State: "CARD x", Rules: "pro", Ask: true}
	if err := ok.Check(); err != nil {
		t.Fatalf("a good decision refused: %v", err)
	}
	hook := Decision{Type: TypeOrder, Subject: "a|b", State: "pair", Rules: "before", Ask: true,
		Prompt: &Prompt{Version: "order-v1", Instructions: "which first?", Options: map[string]string{"before": "a", "after": "b"}}}
	if err := hook.Check(); err != nil {
		t.Fatalf("a hook's decision refused: %v", err)
	}
	for name, d := range map[string]Decision{
		"bad type":        {Type: "Tier", Subject: "x", State: "s"},
		"spaced subject":  {Type: TypeTier, Subject: "a b", State: "s"},
		"no state":        {Type: TypeTier, Subject: "x", State: " "},
		"rules off":       {Type: TypeTier, Subject: "x", State: "s", Rules: "huge"},
		"ask no prompt":   {Type: TypeOrder, Subject: "x", State: "s", Ask: true},
		"reserved field":  {Type: TypeTier, Subject: "x", State: "s", Fields: map[string]string{"outcome": "pro"}},
		"one-option hook": {Type: TypeOrder, Subject: "x", State: "s", Prompt: &Prompt{Version: "o-v1", Instructions: "i", Options: map[string]string{"a": "a"}}},
	} {
		if err := d.Check(); err == nil {
			t.Errorf("%s: accepted %+v", name, d)
		}
	}
}

func ev(id string, kv ...string) Event {
	f := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		f[kv[i]] = kv[i+1]
	}
	return Event{ID: id, Fields: f}
}

func snap(recs map[string]map[string]string) Snapshot {
	return Snapshot{Records: recs, Rows: map[string]bool{}, Reads: map[string]map[string]string{}, Open: map[string]string{},
		Built: map[string]string{}}
}

func findDecision(pl Plan, typ, subject string) (Decision, bool) {
	for _, d := range pl.Decisions {
		if d.Type == typ && d.Subject == subject {
			return d, true
		}
	}
	return Decision{}, false
}

func findOutcome(pl Plan, typ, subject string) (Outcome, bool) {
	for _, o := range pl.Outcomes {
		if o.Type == typ && o.Subject == subject {
			return o, true
		}
	}
	return Outcome{}, false
}

// TestPlanCutIsTierAndWorkType: a pushed primary is a tier decision (rules:
// its declared tier, else flash) and a work-type decision, both asked of Jev;
// a TYPE the card declares is the work type's outcome. A friend-queue task is
// not a primary and makes no row; a row already there is not made again.
func TestPlanCutIsTierAndWorkType(t *testing.T) {
	t.Parallel()

	events := []Event{
		ev("1-0", "id", "p1", "from", "", "to", "waiting", "by", "rowan", "at", "1"),
		ev("2-0", "id", "p2", "from", "", "to", "ready"),
		ev("3-0", "id", "f1", "from", "", "to", "ready"),
		ev("4-0", "id", "p3", "from", "", "to", "waiting"),
	}
	recs := map[string]map[string]string{
		"p1": {"title": "a verb", "route": "pro", "type": "verb", "paths": "cmd/x.go", "done_when": "go test passes"},
		"p2": {"title": "no route"},
		"f1": {"title": "a friend's task", "friend": "stella"},
		"p3": {"title": "seen"},
	}
	if got := Needs(events); strings.Join(got, ",") != "p1,p2,f1,p3" {
		t.Fatalf("Needs = %v", got)
	}
	rows, _, _, _ := Then(events)
	if len(rows) != 8 {
		t.Fatalf("Then rows = %v", rows)
	}
	s := snap(recs)
	s.Rows[RowKey(TypeTier, "p3")] = true
	s.Rows[RowKey(TypeWorkType, "p3")] = true
	pl := MakePlan(events, s)
	if pl.Cursor != "4-0" {
		t.Errorf("cursor %q", pl.Cursor)
	}
	d, ok := findDecision(pl, TypeTier, "p1")
	if !ok || d.Rules != "pro" || !d.Ask || !strings.Contains(d.State, "PATHS: cmd/x.go") || d.At != 1 {
		t.Errorf("p1 tier decision %+v %v", d, ok)
	}
	if d, ok := findDecision(pl, TypeTier, "p2"); !ok || d.Rules != "flash" {
		t.Errorf("p2 tier decision %+v %v; a card with no ROUTE is flash", d, ok)
	}
	if d, ok := findDecision(pl, TypeWorkType, "p1"); !ok || d.Rules != "" || !d.Ask {
		t.Errorf("p1 worktype decision %+v %v", d, ok)
	}
	if o, ok := findOutcome(pl, TypeWorkType, "p1"); !ok || o.Outcome != "verb" || o.By != "card" {
		t.Errorf("p1 worktype outcome %+v %v", o, ok)
	}
	if _, ok := findOutcome(pl, TypeWorkType, "p2"); ok {
		t.Error("p2 declares no TYPE and has an outcome")
	}
	for _, id := range []string{"f1", "p3"} {
		if _, ok := findDecision(pl, TypeTier, id); ok {
			t.Errorf("%s made a tier row", id)
		}
	}
	if len(pl.Decisions) != 4 {
		t.Errorf("decisions %d, want 4: %+v", len(pl.Decisions), pl.Decisions)
	}
}

// TestPlanReviewSuggestThenVerdict: a failed copy's primary in review is a
// review decision (rules: the REVIEW-JEV suggest) keyed by the move's own at,
// which is the review_at the same call wrote; the posted verdict joins the
// primary's open review, by whoever posted it; a read reassign (a copy cut in
// the verdict's call, no primary move) does too. A second review before sync
// reads the first leaves the first move's record behind: a counted gap, no
// row, and the verdict joins the second, never the first. A build's move into
// review (a PR, no fail) is no decision.
func TestPlanReviewSuggestThenVerdict(t *testing.T) {
	t.Parallel()

	recs := map[string]map[string]string{
		"p1": {"review_at": "1000", "review_jev": "REVIEW-JEV id=p1 consumer=bench:b shape=exit-1 same_card=1 same_consumer=1 suggest=redeal",
			"review_shape": "exit-1", "review_model": "kimi-k3", "review_line": "BLOCKED: red"},
		"p2": {"review_at": "2000", "review_jev": "REVIEW-JEV id=p2 suggest=reassign", "review_verdict": "reassign",
			"reviewed_at": "2500", "reviewed_by": "rowan", "review": "REVIEW verdict=reassign:bench:c by=rowan: the reader keeps failing"},
		"p2~3": {"leg": "read", "primary": "p2"},
		"p3":   {"review_at": "3500", "review_jev": "REVIEW-JEV id=p3 suggest=recut", "review_shape": "exit-2"},
	}
	events := []Event{
		ev("1000-0", "id", "p1", "from", "working", "to", "review", "why", "child exit 1", "at", "1000"),
		ev("1500-0", "id", "p1", "from", "review", "to", "ready", "by", "rowan", "why", "review redeal: a crash", "at", "1500"),
		ev("2000-0", "id", "p2", "from", "working", "to", "review", "why", "read failed", "at", "2000"),
		ev("2500-0", "id", "p2~3", "from", "", "to", "bench:c:ready", "at", "2500"),
		ev("9000-0", "id", "p2~3", "from", "", "to", "bench:c:ready", "at", "9000"),
		ev("3000-0", "id", "p3", "from", "working", "to", "review", "why", "child exit 1", "at", "3000"),
		ev("3200-0", "id", "p3", "from", "review", "to", "ready", "by", "stella", "why", "review redeal: first", "at", "3200"),
		ev("3500-0", "id", "p3", "from", "working", "to", "review", "why", "child exit 2", "at", "3500"),
		ev("3600-0", "id", "p3", "from", "review", "to", "waiting", "by", "rowan", "why", "review recut: second", "at", "3600"),
		ev("4000-0", "id", "p1", "from", "working", "to", "review", "why", "ok pr nova-tools#9 head abc", "at", "4000"),
	}
	rows, _, open, _ := Then(events)
	if !contains(rows, RowKey(TypeReview, "p3@3000")) || !contains(open, "p3") {
		t.Fatalf("Then rows %v open %v", rows, open)
	}
	pl := MakePlan(events, snap(recs))
	d, ok := findDecision(pl, TypeReview, "p1@1000")
	if !ok || d.Rules != "redeal" || d.At != 1000 || !strings.Contains(d.State, "SHAPE: exit-1") || !strings.Contains(d.State, "MODEL: kimi-k3") {
		t.Fatalf("review decision %+v %v", d, ok)
	}
	if o, ok := findOutcome(pl, TypeReview, "p1@1000"); !ok || o.Outcome != "redeal" || o.By != "rowan" || o.Why != "a crash" {
		t.Errorf("verdict outcome %+v %v", o, ok)
	}
	n := 0
	for _, o := range pl.Outcomes {
		if o.Subject == "p2@2000" {
			n++
			if o.Outcome != "reassign" || o.By != "rowan" || o.Why != "the reader keeps failing" {
				t.Errorf("read reassign outcome %+v", o)
			}
		}
	}
	if n != 1 {
		t.Errorf("read reassign joined %d times, want once (a later read cut is not the verdict)", n)
	}
	if _, ok := findDecision(pl, TypeReview, "p3@3000"); ok || strings.Join(pl.Moved, ",") != "p3@3000" {
		t.Errorf("a review whose record moved on made a row, or no gap was counted: moved %v", pl.Moved)
	}
	if o, ok := findOutcome(pl, TypeReview, "p3@3500"); !ok || o.Outcome != "recut" || o.By != "rowan" {
		t.Errorf("the second review's verdict %+v %v", o, ok)
	}
	for _, o := range pl.Outcomes {
		if o.Subject == "p3@3000" || (o.Subject == "p3@3500" && o.Outcome == "redeal") {
			t.Errorf("the first verdict joined a row it does not close: %+v", o)
		}
	}
	if _, ok := findDecision(pl, TypeReview, "p1@4000"); ok {
		t.Error("a build's move into review made a review row")
	}
	for _, id := range []string{"p1", "p2", "p3"} {
		if v, set := pl.Open[id]; !set || v != "" {
			t.Errorf("%s's open review after its verdict: %q %v, want cleared", id, v, set)
		}
	}
}

func contains(list []string, x string) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}

// TestPlanReadSanityAndTheHeadsFate: a read copy's score is a readsane
// decision (rules: ReadRules) waiting on its primary's head; when the
// primary lands, a read at the landed head is trusted when its pass or fail
// matched, a low read at an older head asked for the fix that came; merging
// is the tier's outcome.
func TestPlanReadSanityAndTheHeadsFate(t *testing.T) {
	t.Parallel()

	const h1, h2 = "aaaaaaaaaaaa1111", "bbbbbbbbbbbb2222"
	recs := map[string]map[string]string{
		"p1":   {"head": h2, "tier": "pro", "title": "the card", "done_when": "go test passes"},
		"p1~2": {"leg": "read", "primary": "p1", "score": "4", "head": h1, "finding": "no test for the refusal"},
		"p1~5": {"leg": "read", "primary": "p1", "score": "9", "head": h2, "gates": "ci:green,base:ok,scope:ok", "finding": "the doc comment drifts"},
		"p1~6": {"leg": "read", "primary": "p1", "score": "6", "head": h2},
		"p1~7": {"leg": "work", "primary": "p1", "tier": "frontier", "model": "opus-5.5"},
	}
	events := []Event{
		ev("1-0", "id", "p1~2", "from", "bench:b:working", "to", "bench:b:ok"),
		ev("2-0", "id", "p1~7", "from", "bench:a:working", "to", "bench:a:ok"),
		ev("3-0", "id", "p1~5", "from", "bench:c:working", "to", "bench:c:ok"),
		ev("4-0", "id", "p1~6", "from", "bench:d:working", "to", "bench:d:ok"),
		ev("5-0", "id", "p1", "from", "review", "to", "merging"),
		ev("6-0", "id", "p1", "from", "merging", "to", "landed"),
	}
	rows, waits, _, built := Then(events)
	if strings.Join(waits, ",") != "p1" || strings.Join(built, ",") != "p1" {
		t.Fatalf("Then waits %v built %v", waits, built)
	}
	s := snap(recs)
	for _, k := range rows {
		s.Rows[k] = false
	}
	s.Rows[RowKey(TypeTier, "p1")] = true
	pl := MakePlan(events, s)
	for id, rules := range map[string]string{"p1~2": "trust", "p1~5": "trust", "p1~6": "suspect"} {
		d, ok := findDecision(pl, TypeReadSane, id)
		if !ok || d.Rules != rules || d.Fields["primary"] != "p1" || !strings.Contains(d.State, "DONE-WHEN: go test passes") {
			t.Errorf("%s readsane decision %+v %v, want rules %s", id, d, ok, rules)
		}
	}
	if _, ok := findDecision(pl, TypeReadSane, "p1~7"); ok {
		t.Error("a work copy's end made a readsane row")
	}
	// the card declares pro; its work copy ran at frontier: the outcome is
	// what ran, not what was declared
	if o, ok := findOutcome(pl, TypeTier, "p1"); !ok || o.Outcome != "frontier" || o.By != "merging" ||
		!strings.Contains(o.Why, "p1~7") || !strings.Contains(o.Why, "opus-5.5") {
		t.Errorf("tier outcome %+v %v", o, ok)
	}
	if v, set := pl.Built["p1"]; !set || v != "" {
		t.Errorf("built tier after merging %q %v, want cleared", v, set)
	}
	for id, want := range map[string]string{"p1~2": "trust", "p1~5": "trust", "p1~6": "suspect"} {
		if o, ok := findOutcome(pl, TypeReadSane, id); !ok || o.Outcome != want || o.By != "landed" {
			t.Errorf("%s fate %+v %v, want %s", id, o, ok, want)
		}
	}
	if len(pl.Reads) != 3 || strings.Join(pl.Settled, ",") != "p1" {
		t.Errorf("reads %+v settled %v", pl.Reads, pl.Settled)
	}
}

func TestReadFateAndRules(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		landed          bool
		head, score, rh string
		want            string
	}{
		{true, "abc1234", "9", "abc1234", "trust"},
		{true, "abc1234", "5", "abc1234", "suspect"},
		{false, "abc1234", "9", "abc1234", "suspect"},
		{false, "abc1234", "5", "abc1234", "trust"},
		{true, "abc1234", "5", "def5678", "trust"},
		{true, "abc1234", "9", "def5678", ""},
		{false, "abc1234", "5", "def5678", ""},
	} {
		o, ok := ReadFate(c.landed, c.head, c.score, c.rh)
		if got := map[bool]string{true: o.Outcome}[ok]; got != c.want {
			t.Errorf("ReadFate(%v %s %s %s) = %q, want %q", c.landed, c.head, c.score, c.rh, got, c.want)
		}
	}
	for _, c := range []struct {
		score          int
		gates, finding string
		want           string
	}{
		{10, "", "", "trust"},
		{9, "ci:green,base:ok,scope:ok", "a nit", "trust"},
		{9, "ci:red,base:ok,scope:ok", "a nit", "suspect"},
		{8, "ci:green,base:behind,scope:ok", "x", "suspect"},
		{8, "ci:green,base:ok,scope:over", "x", "suspect"},
		{7, "", "", "suspect"},
		{3, "ci:red,base:ok,scope:ok", "red at head", "trust"},
	} {
		if got, _ := ReadRules(c.score, c.gates, c.finding); got != c.want {
			t.Errorf("ReadRules(%d %q %q) = %s, want %s", c.score, c.gates, c.finding, got, c.want)
		}
	}
	if f := ReadFinding(map[string]string{"line2": "SCORE 7/10 gates=ci:green,base:ok,scope:ok finding=no test"}); f != "no test" {
		t.Errorf("ReadFinding from line2 = %q", f)
	}
}

// TestSummarizeIsAgreementPerTypeSourceVersion is the report's arithmetic:
// per type, rules and each Jev prompt version, answers against outcomes;
// open rows are counted, not scored; a person's outcome is an override.
func TestSummarizeIsAgreementPerTypeSourceVersion(t *testing.T) {
	t.Parallel()

	rows := []Row{
		{Type: TypeTier, Rules: "flash", Jev: "pro", Version: "tier-v1", Outcome: "pro", OutcomeBy: "merging", Cost: 0.001, CostKnown: true, MS: 300, HasMS: true},
		{Type: TypeTier, Rules: "flash", Jev: "flash", Version: "tier-v1", Outcome: "flash", OutcomeBy: "merging", MS: 500, HasMS: true},
		{Type: TypeTier, Rules: "pro", Jev: "pro", Version: "tier-v2"},
		{Type: TypeReview, Rules: "redeal", Jev: "recut", Version: "review-v1", Outcome: "recut", OutcomeBy: "rowan"},
		{Type: "zz-hook", Rules: "a", Outcome: "a", OutcomeBy: "card"},
	}
	lines := Summarize(rows)
	var got []string
	for _, l := range lines {
		got = append(got, l.String())
	}
	want := []string{
		"JEV type=tier source=rules version=rules answers=3 outcomes=2 agree=1 agreement=50% open=1 overrides=0 override_agreement=-",
		"JEV type=tier source=jev version=tier-v1 answers=2 outcomes=2 agree=2 agreement=100% open=0 overrides=0 override_agreement=- cost=$0.001000 ms=400",
		"JEV type=tier source=jev version=tier-v2 answers=1 outcomes=0 agree=0 agreement=- open=1 overrides=0 override_agreement=- cost=- ms=-",
		"JEV type=review source=rules version=rules answers=1 outcomes=1 agree=0 agreement=0% open=0 overrides=1 override_agreement=0%",
		"JEV type=review source=jev version=review-v1 answers=1 outcomes=1 agree=1 agreement=100% open=0 overrides=1 override_agreement=100% cost=- ms=-",
		"JEV type=zz-hook source=rules version=rules answers=1 outcomes=1 agree=1 agreement=100% open=0 overrides=0 override_agreement=-",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("report:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	f := Filter(lines, TypeTier, "tier-v2")
	if len(f) != 2 || f[0].Source != SourceRules || f[1].Version != "tier-v2" {
		t.Errorf("Filter tier tier-v2 = %+v", f)
	}
	if f := Filter(lines, TypeReview, ""); len(f) != 2 {
		t.Errorf("Filter review = %+v", f)
	}
	var b bytes.Buffer
	Print(&b, rows, 7, TypeReview, "")
	if !strings.HasPrefix(b.String(), "JEV REPORT rows=5 pending=7 lines=2\n") {
		t.Errorf("Print header %q", b.String())
	}
}

// fakeJev is Jev in a unit test: a scripted answer per question, never the
// real provider.
type fakeJev struct {
	choice string
	conf   float64
	usage  decide.Usage
	err    error
	calls  int
	state  string
	qs     map[string]decide.Question
}

func (f *fakeJev) Decide(_ context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	f.calls++
	f.state, f.qs = state, qs
	if f.err != nil {
		return nil, decide.Usage{}, f.err
	}
	out := map[string]decide.Answer{}
	for k := range qs {
		out[k] = decide.Answer{Type: "choice", Choice: f.choice, Confidence: f.conf}
	}
	return out, f.usage, nil
}

// stepClock steps 250 ms per read, so a call's ms is known without a wall
// clock.
func stepClock() func() time.Time {
	at := time.UnixMilli(1_700_000_000_000)
	return func() time.Time { at = at.Add(250 * time.Millisecond); return at }
}

// TestAskRowIsOneTypedCall: a row is asked once, with its state and its
// type's prompt (or the hook's own), and the answer carries the version, the
// confidence, the ms and the cost; an answer off the options is refused.
func TestAskRowIsOneTypedCall(t *testing.T) {
	t.Parallel()

	clock := stepClock()
	f := &fakeJev{choice: "pro", conf: 0.9, usage: decide.Usage{InputTokens: 900, OutputTokens: 100, HasInput: true, HasOutput: true}}
	row := map[string]string{"type": TypeTier, "subject": "p1", "state": "CARD p1: x", "input_sha": "abc"}
	a, err := AskRow(context.Background(), f, row, clock)
	if err != nil || f.calls != 1 || f.state != "CARD p1: x" {
		t.Fatalf("AskRow %+v %v calls=%d state=%q", a, err, f.calls, f.state)
	}
	if q := f.qs[TypeTier]; len(q.Choice) != 3 || q.Instructions == "" {
		t.Errorf("question %+v", q)
	}
	if a.Answer != "pro" || a.Version != "tier-v1" || a.MS != 250 || a.Tokens != 1000 || a.Cost() != "0.00004200" || a.InputSHA != "abc" {
		t.Errorf("answer %+v cost %s", a, a.Cost())
	}
	if (Answer{}).Cost() != "-" {
		t.Error("an answer with no usage costs a number, not -")
	}
	f.choice = "huge"
	if _, err := AskRow(context.Background(), f, row, clock); err == nil {
		t.Error("an answer off the options was taken")
	}
	f.choice = "after"
	hook := map[string]string{"type": TypeOrder, "subject": "a|b", "state": "pair", "q_version": "order-v1",
		"q_instructions": "which first?", "q_options": `{"after":"b first","before":"a first"}`}
	if a, err := AskRow(context.Background(), f, hook, clock); err != nil || a.Answer != "after" || a.Version != "order-v1" {
		t.Errorf("hook row %+v %v", a, err)
	}
	f.err = errors.New("decide: provider error: status 503")
	if _, err := AskRow(context.Background(), f, row, clock); err == nil {
		t.Error("a provider error was an answer")
	}
}

// TestVerbRefusalsPrint: usage is exit 2 on stderr before anything is
// opened; ask with no key refuses on one JEV REFUSED line naming the
// variable, before Redis; no Redis named is one refusal too.
func TestVerbRefusalsPrint(t *testing.T) {
	t.Parallel()

	e := env{
		newAsker: func(string, string) (Asker, error) {
			return nil, errors.New("decide: JEV_API_KEY is not set; refusing to guess")
		},
		getenv: func(string) string { return "" },
	}
	var out, errOut bytes.Buffer
	for _, args := range [][]string{nil, {"nope"}, {"sync", "--n", "0"}, {"ask", "--n", "999"}, {"outcome", "--type", "tier"}, {"report", "x"}} {
		out.Reset()
		errOut.Reset()
		if code := run(context.Background(), args, &out, &errOut, e); code != 2 || !strings.HasPrefix(errOut.String(), "nova-sprint jev") {
			t.Errorf("%v: exit %d stderr %q", args, code, errOut.String())
		}
	}
	out.Reset()
	if code := run(context.Background(), []string{"ask", "--redis", "127.0.0.1:1"}, &out, &errOut, e); code != 1 ||
		!strings.HasPrefix(out.String(), "JEV REFUSED ask why=") || !strings.Contains(out.String(), "remedy=export\\x20JEV_API_KEY") {
		t.Errorf("ask with no key: exit %d out %q", code, out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"report"}, &out, &errOut, e); code != 1 || !strings.Contains(out.String(), "JEV REFUSED report why=no\\x20Redis\\x20named") {
		t.Errorf("report with no Redis: exit %d out %q", code, out.String())
	}
}
