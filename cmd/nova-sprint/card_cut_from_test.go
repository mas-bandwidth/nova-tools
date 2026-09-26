package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

const cutFromSHA = "0123456789abcdef0123456789abcdef01234567"

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

// fakeCutStore is the one-pipeline push in tests: every batch kept; an id
// in refuse is refused as the Lua push refuses it.
type fakeCutStore struct {
	batches [][]taskcard.PushRequest
	refuse  map[string]string
}

func (f *fakeCutStore) push(_ context.Context, reqs []taskcard.PushRequest) ([]taskcard.PushOutcome, error) {
	f.batches = append(f.batches, reqs)
	out := make([]taskcard.PushOutcome, len(reqs))
	for i, r := range reqs {
		if why, ok := f.refuse[r.ID]; ok {
			out[i].Err = &taskcard.Refused{Why: why}
			continue
		}
		out[i].Result = taskcard.PushResult{Where: r.Where}
	}
	return out, nil
}

func cutDeps(forge *fakeCutForge, st *fakeCutStore) cutFromDeps {
	t0 := time.Unix(1_790_000_000, 0)
	d := cutFromDeps{Now: func() time.Time { return t0 },
		BaseSHA: func(string, string) (string, error) { return cutFromSHA, nil }}
	if forge != nil {
		d.File = forge.file
	}
	if st != nil {
		d.Push = st.push
	}
	return d
}

func runCutFrom(o cutFromOpts, d cutFromDeps) (int, string) {
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
	return cardCutFrom(context.Background(), o, d, &b), b.String()
}

// hundredRows is a header and 100 card rows; row 2 names row:7, a later
// row, and every tenth row names the row before it.
func hundredRows() string {
	var b strings.Builder
	b.WriteString("title\tstream\twho\tpaths\tdone-when\tbody\tdepends-on\troute\test\n")
	for i := 1; i <= 100; i++ {
		dep := "none"
		switch {
		case i == 2:
			dep = "row:7"
		case i%10 == 0:
			dep = fmt.Sprintf("row:%d", i-1)
		}
		route := "friend"
		if i%2 == 0 {
			route = "pro"
		}
		fmt.Fprintf(&b, "Card %d does its thing\tswarm: cards\tany\tcmd/nova-sprint/c%d.go\tgo test ./cmd/nova-sprint -run TestC%d passes\tWhy %d:\\nline two\t%s\t%s\t30\n",
			i, i, i, i, dep, route)
	}
	return b.String()
}

// TestCardCutFromHundredRows is nova-tools#4340's DONE-WHEN: 100 cards land
// in waiting from one file with 100 receipts. Each row's issue is filed
// through the one writer, the cards go in one push batch in dependency
// order, each onto its stream's waiting set with its issue on the record,
// and a DEPENDS-ON row:<n> is the dependency's ref in the issue and its task
// id in blocked_on.
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
	if !strings.HasPrefix(lines[100], "CARD CUT FROM file=cards.tsv rows=100 cut=100 refused=0 filed=100 github=on ms=0") {
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
	if seven.ID != "nova-tools-5005" || seven.Ref != "mas-bandwidth/nova-tools#5005" || seven.Where != "waiting" || seven.Stream != "swarm: cards" {
		t.Fatalf("row 7 push %+v", seven)
	}
	if two.DependsOn != "nova-tools-5005" || !strings.Contains(forge.bodies[6], "DEPENDS-ON: mas-bandwidth/nova-tools#5005\n") {
		t.Fatalf("row 2 blocked_on %q, issue:\n%s", two.DependsOn, forge.bodies[6])
	}
	if ten.DependsOn != byTitle["9"].ID || ten.Spec == nil || ten.Spec.Route != "pro" || ten.Spec.BaseSHA != cutFromSHA || ten.Spec.Repo != "mas-bandwidth/nova-tools" {
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
		"Good card\ts\tany\tp.go\tdone\tbody\tnone\t\t",
		"No paths\ts\tany\t\tdone\tbody\tnone\t\t",
		"Bad route\ts\tany\tp.go\tdone\tbody\tnone\tturbo\t",
		"Ghost dep\ts\tany\tp.go\tdone\tbody\trow:99\t\t",
		"Loop a\ts\tany\tp.go\tdone\tbody\trow:6\t\t",
		"Loop b\ts\tany\tp.go\tdone\tbody\trow:5\t\t",
		"Extra cells\ts\tany\tp.go\tdone\tbody\tnone\t\t30\tspill",
		"Bad who\ts\tsomeone\tp.go\tdone\tbody\tnone\t\t",
		"Bad est\ts\tany\tp.go\tdone\tbody\tnone\t\tsoon",
		"\ts\tany\tp.go\tdone\tbody\tnone\t\t",
	}, "\n")
	forge, st := &fakeCutForge{}, &fakeCutStore{}
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows)}, cutDeps(forge, st))
	if code != 1 || len(forge.titles) != 0 || len(st.batches) != 0 {
		t.Fatalf("exit %d filed %d pushed %d; want 1 and nothing written:\n%s", code, len(forge.titles), len(st.batches), out)
	}
	for _, want := range []string{
		"CARD CUT REFUSED row=2 line=3 id=- why=\"no paths\"",
		"row=3 line=4 id=- why=\"route \\\"turbo\\\" is not frontier, pro or flash, or friend\"",
		"row=4 line=5 id=- why=\"depends-on row:99 names no row (the file has 10)\"",
		"row=5 line=6 id=- why=\"depends-on is a cycle among row:5,row:6\"",
		"row=6 line=7 id=- why=\"depends-on is a cycle among row:5,row:6\"",
		"row=7 line=8 id=- why=\"10 cells where the columns are 9",
		"row=8 line=9 id=- why=\"who \\\"someone\\\" is not any, only <names> or except <names>\"",
		"row=9 line=10 id=- why=\"est \\\"soon\\\" is not minutes",
		"row=10 line=11 id=- why=\"no title\"",
		"CARD CUT FROM file=cards.tsv rows=10 cut=0 refused=9 filed=0 github=on",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "row=1 ") {
		t.Errorf("the good row printed a line:\n%s", out)
	}
}

// TestCardCutFromDryRun prints the rows in push order and touches nothing:
// the deps are nil, so a filing or a push would panic.
func TestCardCutFromDryRun(t *testing.T) {
	t.Parallel()
	rows := "second\ts\tonly rowan,stella\tp.go\tdone\tb\trow:2\tflash\t45 min\nfirst\ts\t\tp.go\tdone\tb\t#12\t\t\n"
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows), DryRun: true, NoGitHub: true}, cutDeps(nil, nil))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	want := "CARD CUT DRY row=2 id=first stream=s who=any route=friend est=30 depends=mas-bandwidth/nova-tools#12 title=first\n" +
		"CARD CUT DRY row=1 id=second stream=s who=\"only rowan,stella\" route=flash est=\"45 min\" depends=first title=second\n" +
		"CARD CUT FROM file=cards.tsv rows=2 cut=0 refused=0 filed=0 github=off ms=0\n"
	if out != want {
		t.Fatalf("dry run:\n%s\nwant:\n%s", out, want)
	}
}

// TestCardCutFromNoGitHub pushes the cards alone: ids from the titles (or
// an id cell), no issue filed, no ref on the record.
func TestCardCutFromNoGitHub(t *testing.T) {
	t.Parallel()
	rows := "id\ttitle\tpaths\tdone-when\tdepends-on\n\tSpeed up the Table!\tp.go\tdone\t\nmy-id\tNamed\tp.go\tdone\trow:1\n"
	st := &fakeCutStore{}
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows), NoGitHub: true, Stream: "nova-sprint"}, cutDeps(nil, st))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "CARD CUT row=1 id=speed-up-the-table ref=- stream=nova-sprint to=waiting depends=none\n") ||
		!strings.Contains(out, "CARD CUT row=2 id=my-id ref=- stream=nova-sprint to=waiting depends=speed-up-the-table\n") {
		t.Fatalf("receipts:\n%s", out)
	}
	if r := st.batches[0][1]; r.Ref != "" || r.Origin != "" || r.DependsOn != "speed-up-the-table" {
		t.Fatalf("push %+v", r)
	}
}

// TestCardCutFromRefusalsPrint: a forge failure stops the filing and names
// every row behind it; a push the store refuses is named; the rows filed
// before the failure are still pushed.
func TestCardCutFromRefusalsPrint(t *testing.T) {
	t.Parallel()
	rows := "a\ts\tany\tp\td\tb\t\t\t\nb\ts\tany\tp\td\tb\t\t\t\nc\ts\tany\tp\td\tb\t\t\t\nd\ts\tany\tp\td\tb\t\t\t\n"
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
		"CARD CUT REFUSED row=4 line=4 id=- why=\"not filed: the filing stopped at row:3\"\n",
		"CARD CUT FROM file=cards.tsv rows=4 cut=1 refused=3 filed=2 github=on",
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
	if err := os.WriteFile(path, []byte("one\ts\tany\tp.go\tdone\tbody\tnone\t\t\n"), 0o600); err != nil {
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
