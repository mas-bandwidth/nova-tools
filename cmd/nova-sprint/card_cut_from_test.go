package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

const cutFromSHA = "0123456789abcdef0123456789abcdef01234567"

// cutInv is a body cell that makes a row one invariant (#4396): its
// INVARIANT and CLASS-TEST lines, a newline written \n as a cell writes it.
const cutInv = `INVARIANT: the card holds one thing.\nCLASS-TEST: TestTheCard`

// fakeCutForge is the one GitHub writer in tests: issues numbered from 5000,
// every title and body kept; failAt makes that call fail.
type fakeCutForge struct {
	titles, bodies []string
	failAt         int
}

func (f *fakeCutForge) file(_ context.Context, repo, title, body string) (int, string, error) {
	if f.failAt > 0 && len(f.titles)+1 == f.failAt {
		return 0, "", errors.New("HTTP 403: rate limited")
	}
	f.titles = append(f.titles, title)
	f.bodies = append(f.bodies, body)
	n := 5000 + len(f.titles) - 1
	return n, fmt.Sprintf("https://github.com/%s/issues/%d", repo, n), nil
}

// fakeCutStore is the one-pipeline push and the cut ledger in tests: every
// batch kept; an id in refuse is refused as the Lua push refuses it, and an
// id pushed before is EXISTS, as the Lua push refuses it; the ledger is a
// map of hashes read through the one parser.
type fakeCutStore struct {
	batches [][]taskcard.PushRequest
	refuse  map[string]string
	pushed  map[string]bool
	ledger  map[string]map[string]string
	noWrite error
}

func (f *fakeCutStore) push(_ context.Context, reqs []taskcard.PushRequest) ([]taskcard.PushOutcome, error) {
	f.batches = append(f.batches, reqs)
	if f.pushed == nil {
		f.pushed = map[string]bool{}
	}
	out := make([]taskcard.PushOutcome, len(reqs))
	for i, r := range reqs {
		if why, ok := f.refuse[r.ID]; ok {
			out[i].Err = &taskcard.Refused{Why: why}
			continue
		}
		if f.pushed[r.ID] {
			out[i].Err = &taskcard.Refused{Why: "EXISTS task:" + r.ID}
			continue
		}
		f.pushed[r.ID] = true
		out[i].Result = taskcard.PushResult{Where: r.Where}
	}
	return out, nil
}

func (f *fakeCutStore) ledgerRead(_ context.Context, key string) (taskcard.CutLedger, error) {
	return taskcard.ParseCutLedger(key, f.ledger[key])
}

func (f *fakeCutStore) ledgerWrite(_ context.Context, key, repo string, row, issue int) error {
	if f.noWrite != nil {
		return f.noWrite
	}
	if f.ledger == nil {
		f.ledger = map[string]map[string]string{}
	}
	if f.ledger[key] == nil {
		f.ledger[key] = map[string]string{}
	}
	f.ledger[key]["repo"] = repo
	f.ledger[key][fmt.Sprint(row)] = fmt.Sprint(issue)
	return nil
}

func cutDeps(forge *fakeCutForge, st *fakeCutStore) cutFromDeps {
	t0 := time.Unix(1_790_000_000, 0)
	d := cutFromDeps{Now: func() time.Time { return t0 },
		BaseSHA: func(string, string) (string, error) { return cutFromSHA, nil }}
	if forge != nil {
		d.File = forge.file
	}
	if st != nil {
		d.Push, d.LedgerRead, d.LedgerWrite = st.push, st.ledgerRead, st.ledgerWrite
	}
	return d
}

// runCutFrom runs card cut --from on o and d: the exit code and stdout, and
// stderr with it (as a terminal shows both) unless errOut is given.
func runCutFrom(o cutFromOpts, d cutFromDeps, errOut ...io.Writer) (int, string) {
	var b strings.Builder
	if o.Repo == "" {
		o.Repo = "mas-bandwidth/nova-tools"
	}
	if o.Base == "" {
		o.Base = "dev"
	}
	if o.Actor == "" {
		o.Actor = "rowan"
	}
	if o.From == "" {
		o.From = "cards.tsv"
	}
	var e io.Writer = &b
	if len(errOut) > 0 {
		e = errOut[0]
	}
	return cardCutFrom(context.Background(), o, d, &b, e), b.String()
}

// runCutFromErr is runCutFrom with stdout and stderr apart.
func runCutFromErr(o cutFromOpts, d cutFromDeps) (int, string, string) {
	var errOut strings.Builder
	code, out := runCutFrom(o, d, &errOut)
	return code, out, errOut.String()
}

// hundredRows is a header and 100 card rows; row 2 names c7 (row 7's id
// cell, a later row), and every tenth row names the row before it by its
// id cell. Only the rows depended on have an id cell.
func hundredRows() string {
	var b strings.Builder
	b.WriteString("id\ttitle\tstream\twho\tpaths\tdone-when\tbody\tdepends-on\troute\test\n")
	for i := 1; i <= 100; i++ {
		id, dep := "", "none"
		if i == 7 || i%10 == 9 {
			id = fmt.Sprintf("c%d", i)
		}
		switch {
		case i == 2:
			dep = "c7"
		case i%10 == 0:
			dep = fmt.Sprintf("task:c%d", i-1)
		}
		route := "friend"
		if i%2 == 0 {
			route = "pro"
		}
		fmt.Fprintf(&b, "%s\tCard %d does its thing\tswarm: cards\tany\tcmd/nova-sprint/c%d.go\tgo test ./cmd/nova-sprint -run TestC%d passes\tWhy %d:\\nline two\\n%s\t%s\t%s\t30\n",
			id, i, i, i, i, cutInv, dep, route)
	}
	return b.String()
}

// TestCardCutFromHundredRows is nova-tools#4340's DONE-WHEN: 100 cards land
// in waiting from one file with 100 receipts. Each row's issue is filed
// through the one writer, the cards go in one push batch in dependency
// order, each onto its stream's waiting set with its issue on the record,
// and a DEPENDS-ON naming another row's id cell is that row's ref in the
// issue and its task id in blocked_on.
func TestCardCutFromHundredRows(t *testing.T) {
	t.Parallel()
	forge, st := &fakeCutForge{}, &fakeCutStore{}
	code, out := runCutFrom(cutFromOpts{Text: []byte(hundredRows()), Sprint: "s1"}, cutDeps(forge, st))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 101 {
		t.Fatalf("%d lines, want 100 receipts and the summary:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[100], "CARD CUT FROM file=cards.tsv rows=100 cut=100 already=0 refused=0 filed=100 reused=0 github=on ms=0") {
		t.Fatalf("summary %q", lines[100])
	}
	receipts := 0
	for _, l := range lines[:100] {
		if strings.HasPrefix(l, "CARD CUT row=") && strings.Contains(l, " to=waiting ") && strings.Contains(l, ` stream="swarm: cards" `) {
			receipts++
		}
	}
	if receipts != 100 || len(forge.titles) != 100 || len(st.batches) != 1 || len(st.batches[0]) != 100 {
		t.Fatalf("receipts=%d filed=%d batches=%d; want 100, 100, one batch of 100", receipts, len(forge.titles), len(st.batches))
	}
	// Order: row 2 waits for row 7, so rows 1,3,4,5,6,7 are filed first.
	wantFirst := []string{"Card 1 ", "Card 3 ", "Card 4 ", "Card 5 ", "Card 6 ", "Card 7 ", "Card 2 ", "Card 8 "}
	for i, w := range wantFirst {
		if !strings.HasPrefix(forge.titles[i], w) {
			t.Fatalf("filing %d is %q, want %q (dependency order)", i, forge.titles[i], w)
		}
	}
	byTitle := map[string]taskcard.PushRequest{}
	for _, r := range st.batches[0] {
		byTitle[strings.Fields(r.Title)[1]] = r
	}
	seven, two, ten := byTitle["7"], byTitle["2"], byTitle["10"]
	if seven.ID != "c7" || seven.Ref != "mas-bandwidth/nova-tools#5005" || seven.Where != "waiting" || seven.Stream != "swarm: cards" {
		t.Fatalf("row 7 push %+v", seven)
	}
	if two.ID != "nova-tools-5006" || two.DependsOn != "c7" || !strings.Contains(forge.bodies[6], "DEPENDS-ON: mas-bandwidth/nova-tools#5005\n") {
		t.Fatalf("row 2 blocked_on %q, issue:\n%s", two.DependsOn, forge.bodies[6])
	}
	if ten.DependsOn != "c9" || byTitle["9"].ID != "c9" || ten.Spec == nil || ten.Spec.Route != "pro" || ten.Spec.BaseSHA != cutFromSHA || ten.Spec.Repo != "mas-bandwidth/nova-tools" {
		t.Fatalf("row 10 push %+v spec %+v", ten, ten.Spec)
	}
	if !strings.Contains(ten.Spec.Body, "Why 10:\nline two") || ten.Sprint != "s1" || ten.By != "rowan" {
		t.Fatalf("row 10 body %q sprint %q by %q", ten.Spec.Body, ten.Sprint, ten.By)
	}
	if byTitle["1"].DependsOn != "" || byTitle["1"].Spec.Route != "friend" || byTitle["1"].Spec.BaseSHA != "" {
		t.Fatalf("row 1 push %+v spec %+v", byTitle["1"], byTitle["1"].Spec)
	}
}

// TestCardCutFromBadRowsAreNamed: a bad row is named, not skipped: every
// bad row prints its row, line and why, and nothing is filed or pushed.
func TestCardCutFromBadRowsAreNamed(t *testing.T) {
	t.Parallel()
	rows := strings.Join([]string{
		"# a comment line",
		"id\ttitle\tstream\twho\tpaths\tdone-when\tbody\tdepends-on\troute\test",
		"\tGood card\ts\tany\tp.go\tdone\tbody\tnone\t\t",
		"\tNo paths\ts\tany\t\tdone\tbody\tnone\t\t",
		"\tBad route\ts\tany\tp.go\tdone\tbody\tnone\tturbo\t",
		"\tRow dep\ts\tany\tp.go\tdone\tbody\trow:1\t\t",
		"loop-a\tLoop a\ts\tany\tp.go\tdone\tbody\tloop-b\t\t",
		"loop-b\tLoop b\ts\tany\tp.go\tdone\tbody\ttask:loop-a\t\t",
		"\tExtra cells\ts\tany\tp.go\tdone\tbody\tnone\t\t30\tspill",
		"\tBad who\ts\tsomeone\tp.go\tdone\tbody\tnone\t\t",
		"\tBad est\ts\tany\tp.go\tdone\tbody\tnone\t\tsoon",
		"\t\ts\tany\tp.go\tdone\tbody\tnone\t\t",
		"self\tSelf dep\ts\tany\tp.go\tdone\tbody\tself\t\t",
		"loop-a\tTwice\ts\tany\tp.go\tdone\tbody\tnone\t\t",
	}, "\n")
	rows = strings.ReplaceAll(rows, "\tdone\tbody\t", "\tdone\t"+cutInv+"\t")
	forge, st := &fakeCutForge{}, &fakeCutStore{}
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows)}, cutDeps(forge, st))
	if code != 1 || len(forge.titles) != 0 || len(st.batches) != 0 || len(st.ledger) != 0 {
		t.Fatalf("exit %d filed %d pushed %d ledger %d; want 1 and nothing written:\n%s", code, len(forge.titles), len(st.batches), len(st.ledger), out)
	}
	for _, want := range []string{
		"CARD CUT REFUSED row=2 line=4 id=- why=\"no paths\"",
		"row=3 line=5 id=- why=\"route \\\"turbo\\\" is not frontier, pro or flash, or friend\"",
		"row=4 line=6 id=- why=\"depends-on row:1 is refused (#3409: one DEPENDS-ON form); add an id column (a header row naming id), give row 1 an id and name that id\"",
		"row=5 line=7 id=loop-a why=\"depends-on is a cycle among rows 5,6\"",
		"row=6 line=8 id=loop-b why=\"depends-on is a cycle among rows 5,6\"",
		"row=7 line=9 id=- why=\"11 cells where the columns are 10",
		"row=8 line=10 id=- why=\"who \\\"someone\\\" is not any, only <names> or except <names>\"",
		"row=9 line=11 id=- why=\"est \\\"soon\\\" is not minutes",
		"row=10 line=12 id=- why=\"no title\"",
		"row=11 line=13 id=self why=\"depends-on self names the row itself\"",
		"row=12 line=14 id=loop-a why=\"id loop-a is row 5's too\"",
		"CARD CUT FROM file=cards.tsv rows=12 cut=0 already=0 refused=11 filed=0 reused=0 github=on",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "row=1 ") {
		t.Errorf("the good row printed a line:\n%s", out)
	}
}

// TestCardCutFromDependedRowNeedsID: #3409 is one DEPENDS-ON form, so a row
// that is depended on is named by its id cell. row:<n> is refused naming
// the id column; with --no-github a row's title slug (its id when it has no
// id cell) is refused the same way, naming the row. Nothing is written.
func TestCardCutFromDependedRowNeedsID(t *testing.T) {
	t.Parallel()
	rows := "title\tpaths\tdone-when\tbody\tdepends-on\nBase work\tp.go\tdone\t" + cutInv + "\t\nOn top\tp.go\tdone\t" + cutInv + "\trow:1\n"
	forge, st := &fakeCutForge{}, &fakeCutStore{}
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows), Stream: "s"}, cutDeps(forge, st))
	if code != 1 || len(forge.titles) != 0 || len(st.batches) != 0 ||
		!strings.Contains(out, "CARD CUT REFUSED row=2 line=3 id=- why=\"depends-on row:1 is refused (#3409: one DEPENDS-ON form); add an id column (a header row naming id), give row 1 an id and name that id\"\n") {
		t.Fatalf("row:<n>: exit %d filed %d pushed %d:\n%s", code, len(forge.titles), len(st.batches), out)
	}
	rows = "title\tpaths\tdone-when\tbody\tdepends-on\nBase work\tp.go\tdone\t" + cutInv + "\t\nOn top\tp.go\tdone\t" + cutInv + "\tbase-work\n"
	code, out = runCutFrom(cutFromOpts{Text: []byte(rows), Stream: "s", NoGitHub: true}, cutDeps(nil, st))
	if code != 1 || len(st.batches) != 0 ||
		!strings.Contains(out, "CARD CUT REFUSED row=2 line=3 id=on-top why=\"depends-on base-work is row 1's title, and a row that is depended on needs an id; add an id column (a header row naming id) and give row 1 an id\"\n") {
		t.Fatalf("slug: exit %d pushed %d:\n%s", code, len(st.batches), out)
	}
}

// TestCardCutFromDryRun prints the rows in push order and touches nothing:
// the deps are nil, so a filing or a push would panic.
func TestCardCutFromDryRun(t *testing.T) {
	t.Parallel()
	rows := "id\ttitle\tstream\twho\tpaths\tdone-when\tbody\tdepends-on\troute\test\n" +
		"\tsecond\ts\tonly rowan,stella\tp.go\tdone\t" + cutInv + "\tfirst-id\tflash\t45 min\nfirst-id\tfirst\ts\t\tp.go\tdone\t" + cutInv + "\t#12\t\t\n"
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows), DryRun: true, NoGitHub: true}, cutDeps(nil, nil))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	want := "CARD CUT DRY row=2 id=first-id stream=s who=any route=friend est=30 depends=mas-bandwidth/nova-tools#12 title=first\n" +
		"CARD CUT DRY row=1 id=second stream=s who=\"only rowan,stella\" route=flash est=\"45 min\" depends=first-id title=second\n" +
		"CARD CUT FROM file=cards.tsv rows=2 cut=0 already=0 refused=0 filed=0 reused=0 github=off ms=0\n"
	if out != want {
		t.Fatalf("dry run:\n%s\nwant:\n%s", out, want)
	}
}

// TestCardCutFromNoGitHub pushes the cards alone: ids from the titles (or
// an id cell), no issue filed, no ref on the record.
func TestCardCutFromNoGitHub(t *testing.T) {
	t.Parallel()
	rows := "id\ttitle\tpaths\tdone-when\tbody\tdepends-on\n\tSpeed up the Table!\tp.go\tdone\t" + cutInv + "\t\nmy-id\tNamed\tp.go\tdone\t" +
		cutInv + "\tbase\nbase\tBase\tp.go\tdone\t" + cutInv + "\t\n"
	st := &fakeCutStore{}
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows), NoGitHub: true, Stream: "nova-sprint"}, cutDeps(nil, st))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "CARD CUT row=1 id=speed-up-the-table ref=- stream=nova-sprint to=waiting depends=none\n"+
		"CARD CUT row=3 id=base ref=- stream=nova-sprint to=waiting depends=none\n"+
		"CARD CUT row=2 id=my-id ref=- stream=nova-sprint to=waiting depends=base\n") || len(st.ledger) != 0 {
		t.Fatalf("receipts (ledger %v):\n%s", st.ledger, out)
	}
	if r := st.batches[0][2]; r.Ref != "" || r.Origin != "" || r.DependsOn != "base" {
		t.Fatalf("push %+v", r)
	}
}

// TestCardCutFromRefusalsPrint: a forge failure stops the filing and names
// every row behind it; a push the store refuses is named; the rows filed
// before the failure are still pushed.
func TestCardCutFromRefusalsPrint(t *testing.T) {
	t.Parallel()
	rows := strings.ReplaceAll("a\ts\tany\tp\td\tb\t\t\t\nb\ts\tany\tp\td\tb\t\t\t\nc\ts\tany\tp\td\tb\t\t\t\nd\ts\tany\tp\td\tb\t\t\t\n", "\td\tb\t", "\td\t"+cutInv+"\t")
	forge := &fakeCutForge{failAt: 3}
	st := &fakeCutStore{refuse: map[string]string{"nova-tools-5001": "EXISTS task:nova-tools-5001"}}
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows)}, cutDeps(forge, st))
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"CARD CUT row=1 id=nova-tools-5000 ref=mas-bandwidth/nova-tools#5000 stream=s to=waiting depends=none\n",
		"CARD CUT REFUSED row=2 line=2 id=nova-tools-5001 why=\"push refused: EXISTS task:nova-tools-5001\"\n",
		"CARD CUT REFUSED row=3 line=3 id=- why=\"file: HTTP 403: rate limited\"\n",
		"CARD CUT REFUSED row=4 line=4 id=- why=\"not filed: the filing stopped at row 3\"\n",
		"CARD CUT FROM file=cards.tsv rows=4 cut=1 already=0 refused=3 filed=2 reused=0 github=on",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(st.batches) != 1 || len(st.batches[0]) != 2 {
		t.Fatalf("pushed %v; want one batch of the two filed rows", st.batches)
	}
}

// TestCardCutFromFlags: --from through the verb. A dry run reads the file
// and needs no store; --from with --issue is usage; a bad repo is refused.
func TestCardCutFromFlags(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cards.tsv")
	if err := os.WriteFile(path, []byte("one\ts\tany\tp.go\tdone\t"+cutInv+"\tnone\t\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runSprint("card", "cut", "--from", path, "--repo", "o/r", "--dry-run")
	if code != 0 || !strings.Contains(out, "CARD CUT DRY row=1 id=- stream=s") || errOut != "" {
		t.Fatalf("dry run: exit %d\n%s%s", code, out, errOut)
	}
	if code, _, errOut := runSprint("card", "cut", "--from", path, "--repo", "o/r", "--issue", "3"); code != 2 || !strings.Contains(errOut, "takes no --issue") {
		t.Fatalf("--from --issue: exit %d %s", code, errOut)
	}
	if code, _, errOut := runSprint("card", "cut", "--from", path, "--repo", "nova-tools", "--dry-run"); code != 2 || !strings.Contains(errOut, "--repo <owner/name>") {
		t.Fatalf("bad repo: exit %d %s", code, errOut)
	}
	if code, _, errOut := runSprint("card", "cut", "--from", path, "--repo", "o/r", "--dry-run", "--base-sha", "abc"); code != 2 || !strings.Contains(errOut, "40 hex") {
		t.Fatalf("bad base-sha: exit %d %s", code, errOut)
	}
}

// TestCardCutFromRerunFilesNothingTwice is the ledger (the cold read of
// #4358): the forge numbers monotonically, so an issue filed twice would
// show as a second number. A first run stops at row 3's filing; the rerun
// takes rows 1 and 2 from the ledger (their cards are already cut) and files
// rows 3 and 4 alone; a third run files nothing and exits 0.
func TestCardCutFromRerunFilesNothingTwice(t *testing.T) {
	t.Parallel()
	rows := strings.ReplaceAll("id\ttitle\tstream\tpaths\tdone-when\tbody\tdepends-on\n"+
		"a\tA\ts\tp\td\tI\t\n\tB\ts\tp\td\tI\ta\n\tC\ts\tp\td\tI\ta\n\tD\ts\tp\td\tI\t\n", "\tI\t", "\t"+cutInv+"\t")
	forge, st := &fakeCutForge{failAt: 3}, &fakeCutStore{}
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows)}, cutDeps(forge, st))
	if code != 1 || !strings.Contains(out, "rows=4 cut=2 already=0 refused=2 filed=2 reused=0 ") {
		t.Fatalf("first run: exit %d:\n%s", code, out)
	}
	key := taskcard.CutLedgerKey([]byte(rows))
	if got := st.ledger[key]; len(got) != 3 || got["repo"] != "mas-bandwidth/nova-tools" || got["1"] != "5000" || got["2"] != "5001" {
		t.Fatalf("ledger after the partial filing %v", got)
	}
	forge.failAt = 0
	code, out = runCutFrom(cutFromOpts{Text: []byte(rows)}, cutDeps(forge, st))
	for _, want := range []string{
		"CARD CUT row=1 id=a ref=mas-bandwidth/nova-tools#5000 stream=s to=already depends=none\n",
		"CARD CUT row=2 id=nova-tools-5001 ref=mas-bandwidth/nova-tools#5001 stream=s to=already depends=a\n",
		"CARD CUT row=3 id=nova-tools-5002 ref=mas-bandwidth/nova-tools#5002 stream=s to=waiting depends=a\n",
		"CARD CUT row=4 id=nova-tools-5003 ref=mas-bandwidth/nova-tools#5003 stream=s to=waiting depends=none\n",
		"rows=4 cut=2 already=2 refused=0 filed=2 reused=2 ",
	} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("rerun: exit %d, output lacks %q:\n%s", code, want, out)
		}
	}
	if !strings.Contains(forge.bodies[2], "DEPENDS-ON: mas-bandwidth/nova-tools#5000\n") {
		t.Fatalf("row 3's issue names its dependency by the ledger's ref:\n%s", forge.bodies[2])
	}
	code, out = runCutFrom(cutFromOpts{Text: []byte(rows)}, cutDeps(forge, st))
	if code != 0 || !strings.Contains(out, "rows=4 cut=0 already=4 refused=0 filed=0 reused=4 ") || strings.Count(out, " to=already ") != 4 {
		t.Fatalf("third run: exit %d:\n%s", code, out)
	}
	if want := []string{"A", "B", "C", "D"}; strings.Join(forge.titles, ",") != strings.Join(want, ",") {
		t.Fatalf("filed %v across three runs, want each row once: %v", forge.titles, want)
	}
}

// TestCardCutFromLedgerRefusals: a ledger that cannot be read files
// nothing; a ledger of another repo is refused with the --repo that fits; a
// ledger write that fails stops the filing and names the HSET that records
// the issue.
func TestCardCutFromLedgerRefusals(t *testing.T) {
	t.Parallel()
	rows := strings.ReplaceAll("a\ts\tany\tp\td\tb\t\t\t\nb\ts\tany\tp\td\tb\t\t\t\n", "\td\tb\t", "\td\t"+cutInv+"\t")
	key := taskcard.CutLedgerKey([]byte(rows))

	forge, st := &fakeCutForge{}, &fakeCutStore{}
	d := cutDeps(forge, st)
	d.LedgerRead = func(context.Context, string) (taskcard.CutLedger, error) {
		return taskcard.CutLedger{}, errors.New("dial tcp: connection refused")
	}
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows)}, d)
	if code != 1 || len(forge.titles) != 0 || !strings.Contains(out, `CARD CUT REFUSED file=cards.tsv why="ledger: dial tcp: connection refused" remedy="check --redis or NOVA_SPRINT_REDIS and rerun; nothing was filed"`) {
		t.Fatalf("unreadable ledger: exit %d filed %d:\n%s", code, len(forge.titles), out)
	}

	st = &fakeCutStore{ledger: map[string]map[string]string{key: {"repo": "o/other", "1": "9"}}}
	code, out = runCutFrom(cutFromOpts{Text: []byte(rows)}, cutDeps(forge, st))
	if code != 1 || len(forge.titles) != 0 || !strings.Contains(out, `filed this file's rows on o/other, not mas-bandwidth/nova-tools" remedy="rerun with --repo o/other"`) {
		t.Fatalf("other repo: exit %d filed %d:\n%s", code, len(forge.titles), out)
	}

	st = &fakeCutStore{noWrite: errors.New("READONLY")}
	code, out = runCutFrom(cutFromOpts{Text: []byte(rows)}, cutDeps(forge, st))
	want := "CARD CUT REFUSED row=1 line=1 id=- why=\"ledger: READONLY; mas-bandwidth/nova-tools#5000 is filed but not in the ledger, so a rerun files it again: record it first with redis-cli HSET " +
		key + " repo mas-bandwidth/nova-tools 1 5000\"\n"
	if code != 1 || len(forge.titles) != 1 || len(st.batches) != 0 || !strings.Contains(out, want) ||
		!strings.Contains(out, "row=2 line=2 id=- why=\"not filed: the filing stopped at row 1\"") {
		t.Fatalf("unwritable ledger: exit %d filed %d pushed %d:\n%s", code, len(forge.titles), len(st.batches), out)
	}
}

// TestCardCutFromRefusesNotOneInvariant (#4396): a row that is not one
// invariant (a body that says "build issue #N as written", with no INVARIANT
// or CLASS-TEST line) is refused, exit 2, its receipt on stdout and one
// REFUSED card-lint line per rule on stderr, and nothing is filed or pushed.
func TestCardCutFromRefusesNotOneInvariant(t *testing.T) {
	t.Parallel()
	rows := "good\ts\tany\tp.go\tdone\t" + cutInv + "\tnone\t\t\n" +
		"list\ts\tany\tp.go\tdone\tbuild issue #4396 as written\tnone\t\t\n"
	forge, st := &fakeCutForge{}, &fakeCutStore{}
	code, out, errOut := runCutFromErr(cutFromOpts{Text: []byte(rows)}, cutDeps(forge, st))
	if code != 2 || len(forge.titles) != 0 || len(st.batches) != 0 {
		t.Fatalf("exit %d filed %d pushed %d; want 2 and nothing written:\n%s", code, len(forge.titles), len(st.batches), out)
	}
	for _, want := range []string{
		`CARD CUT REFUSED row=2 line=2 id=- why="card-lint invariant-missing,class-test-missing,build-issue: not one invariant"` + "\n",
		"rows=2 cut=0 already=0 refused=1 filed=0 ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	wantErr := `REFUSED card-lint rule=invariant-missing line="" remedy="add INVARIANT: <the one sentence the class test proves>" row=2` + "\n" +
		`REFUSED card-lint rule=class-test-missing line="" remedy="add CLASS-TEST: Test<Name>, the one Go test that proves the invariant" row=2` + "\n" +
		`REFUSED card-lint rule=build-issue line="build issue #4396 as written" remedy="cut as a parent with children: card cut --parent" row=2` + "\n"
	if errOut != wantErr || strings.Contains(out, "card-lint rule=") {
		t.Errorf("stderr\n%s\nwant\n%s\nstdout\n%s", errOut, wantErr, out)
	}
	if strings.Contains(out, "row=1 ") {
		t.Errorf("the one-invariant row printed a line:\n%s", out)
	}
}
