package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// briefBaseFetch bounds the fetch of one base into the lander's clone: a forge that does not
// answer is a MISSING finding, never an add that hangs.
const briefBaseFetch = 2 * time.Minute

func init() { verbClasses["lint"] = classRead }

// briefBaseRef is where add fetches a base's tip in the lander's clone: a namespace of its
// own, so a fetch here never takes the lock of a ref the lander is moving.
const briefBaseRef = "refs/nova-add/"

// briefLintRow is one card brief held to the brief checks at its base tip: the findings, the
// corrected header lines the lint computed, the evidence a fix is re-linted against, and
// where the brief stood in the call.
type briefLintRow struct {
	idx      int
	id       string
	brief    string
	bb       swarm.BriefBase
	findings []swarm.CardHeaderFinding
	fix      swarm.BriefFix
}

// briefLintRows holds each card brief of an add (one with a PATHS: line) to the brief checks
// at its base (swarm.LintBrief; docs/SPEC-SPRINT.md section 11, the brief checks): the
// repository its REPO: names, in the lander's clone of it (made as land makes it, when there
// is none), at the tip of its BASE: fetched once a call, read with git and no go command;
// beside it the bases the store lists in use and the friends' tiers. It returns one row per
// card brief, and a non-zero code when the store or a clone could not be read. A brief with
// no PATHS: line is no card brief and is not held here.
func (a *app) briefLintRows(verbName string, st *store.Store, allowPersonal bool, stderr io.Writer, briefs ...briefCheck) ([]briefLintRow, int) {
	var cards []int
	friendNeeded := false
	for bi, b := range briefs {
		if _, ok := swarm.CardHeaderValue([]byte(b.brief), "PATHS"); !ok {
			continue
		}
		cards = append(cards, bi)
		if w, _ := cardhdr.ReadWho(b.brief); w.Friend {
			friendNeeded = true
		}
	}
	if len(cards) == 0 {
		return nil, 0
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return nil, a.readFailed(verbName, err, stderr)
	}
	var listed []string
	for _, r := range basesInUse(s) {
		if !slices.Contains(listed, r.Base) {
			listed = append(listed, r.Base)
		}
	}
	var friends map[string][]string
	if friendNeeded {
		seats, err := st.FriendSeats(ctx, a.now())
		if err != nil {
			return nil, a.readFailed(verbName, err, stderr)
		}
		friends = map[string][]string{}
		for _, f := range seats {
			tiers := f.Tiers
			if len(tiers) == 0 {
				tiers = sprint.Split(f.Class)
			}
			friends[f.Name] = tiers
		}
	}
	type key struct{ repo, ref, pin string }
	evidence := map[key]swarm.BriefBase{}
	l := &lander{a: a}
	rows := make([]briefLintRow, 0, len(cards))
	for _, bi := range cards {
		c := briefs[bi]
		cb := swarm.ReadCardBase([]byte(c.brief))
		bb := swarm.BriefBase{Friends: friends, Listed: listed}
		if cb.Ref != "" && cb.Named != "" {
			k := key{cb.Named, cb.Ref, cb.Sha}
			got, ok := evidence[k]
			if !ok {
				got = a.briefBaseAt(ctx, l, cb)
				evidence[k] = got
			}
			bb.Repo, bb.Sha, bb.Missing, bb.Gone = got.Repo, got.Sha, got.Missing, got.Gone
			if allowPersonal {
				bb.Listed = append(append([]string(nil), listed...), cb.Ref) // --allow-personal-base admits the base it names
			}
		}
		fs, fix := swarm.LintBrief([]byte(c.brief), bb)
		id := c.id
		if id == "" {
			id = "-"
		}
		rows = append(rows, briefLintRow{idx: bi, id: id, brief: c.brief, bb: bb, findings: fs, fix: fix})
	}
	return rows, 0
}

// briefFindingsLines is every finding as a LINT DRIFT line and every corrected header line
// as a LINT FIX line, in the order the rows were given.
func briefFindingsLines(rows []briefLintRow) (lines []string, findings int, first string) {
	for _, row := range rows {
		for _, f := range row.findings {
			if first == "" {
				first = f.Check + ": " + f.Excerpt
			}
			lines = append(lines, fmt.Sprintf("LINT DRIFT card=%s check=%s line=%d: %s remedy=%s", oneline.Field(row.id), f.Check, f.Line,
				oneline.Escape(oneline.Cap(f.Excerpt, oneline.TailBytes)), oneline.Escape(swarm.BriefRemedy(f.Check))))
		}
		for _, x := range row.fix {
			lines = append(lines, "LINT FIX card="+oneline.Field(row.id)+" "+oneline.Escape(x))
		}
		findings += len(row.findings)
	}
	return lines, findings, first
}

// holdBriefBase holds each card brief of an add to the brief checks at its base and refuses
// the call, exit 2, nothing written, each finding a LINT DRIFT line with its remedy and each
// corrected header line a LINT FIX line. It is the refusal form: a caller that admits the
// corrected brief uses fixBriefBase instead.
func (a *app) holdBriefBase(verbName string, st *store.Store, allowPersonal bool, stderr io.Writer, briefs ...briefCheck) int {
	rows, code := a.briefLintRows(verbName, st, allowPersonal, stderr, briefs...)
	if code != 0 {
		return code
	}
	lines, findings, first := briefFindingsLines(rows)
	if findings == 0 {
		return 0
	}
	for _, x := range lines {
		fmt.Fprintln(stderr, x)
	}
	return refuse(stderr, verbName, fmt.Sprintf("%d brief finding(s) at the base, the first %s; nothing was written; each LINT FIX line above is a corrected header line: apply it to the brief and add it again", findings, oneline.Cap(first, 300)))
}

// fixBriefBase is add's brief lint that applies the lint's own fix lines: add holds each
// card brief to the brief checks at its base, and every finding the corrected header lines
// answer is applied to the brief the call stores, printed as a LINT APPLIED line, and the
// card is admitted (docs/SPEC-SPRINT.md section 11, the brief checks). A finding with no
// fix line, or one the fix does not answer on the re-lint, refuses the whole call as
// holdBriefBase does. noFix keeps that refusal (add --no-fix). The corrected briefs come
// back in the order given, beside how many fix lines were applied.
func (a *app) fixBriefBase(verbName string, st *store.Store, allowPersonal, noFix bool, stderr io.Writer, briefs ...briefCheck) ([]briefCheck, int, int) {
	rows, code := a.briefLintRows(verbName, st, allowPersonal, stderr, briefs...)
	if code != 0 {
		return nil, 0, code
	}
	out := append([]briefCheck(nil), briefs...)
	if len(rows) == 0 {
		return out, 0, 0
	}
	fixed := make([]string, len(rows))
	ok := make([]bool, len(rows))
	anyRefuse := false
	for i, row := range rows {
		if len(row.findings) == 0 {
			ok[i] = true
			continue
		}
		if noFix {
			anyRefuse = true
			continue
		}
		applied := sprint.ApplyBriefFix(row.brief, row.fix)
		fs, _ := swarm.LintBrief([]byte(applied), row.bb)
		if len(fs) > 0 {
			// the fix left a finding the lint has no line for: the whole call refuses
			anyRefuse = true
			continue
		}
		fixed[i], ok[i] = applied, true
	}
	if anyRefuse {
		var lines []string
		var findings int
		first := ""
		for i, row := range rows {
			if ok[i] {
				continue
			}
			l, n, f := briefFindingsLines([]briefLintRow{row})
			if n == 0 {
				continue
			}
			lines = append(lines, l...)
			findings += n
			if first == "" {
				first = f
			}
		}
		for _, x := range lines {
			fmt.Fprintln(stderr, x)
		}
		return nil, 0, refuse(stderr, verbName, fmt.Sprintf("%d brief finding(s) at the base, the first %s; nothing was written; each LINT FIX line above is a corrected header line: apply it to the brief and add it again, or drop --no-fix to apply the ones the lint computed", findings, oneline.Cap(first, 300)))
	}
	applied := 0
	var says []string
	for i, row := range rows {
		if len(row.findings) == 0 {
			continue
		}
		out[row.idx].brief = fixed[i]
		for _, x := range row.fix {
			says = append(says, "LINT APPLIED card="+oneline.Field(row.id)+" "+oneline.Escape(x))
			applied++
		}
	}
	for _, x := range says {
		fmt.Fprintln(stderr, x)
	}
	return out, applied, 0
}

// briefDriftFunc is the deal-time brief lint (Store.BriefDrift, sprint.TickReq.BriefDrift):
// briefAtTheBase under the host guard, nil under NOVA_TEST_NO_HOST, where no host may be
// cloned or fetched.
func (a *app) briefDriftFunc() func(*sprint.Snapshot, *sprint.Card) string {
	if os.Getenv("NOVA_TEST_NO_HOST") != "" {
		return nil
	}
	return a.briefAtTheBase()
}

// briefAtTheBase is briefDriftFunc's lint without the host guard, for a caller that brings
// its own repository (a test). Every ready card's brief is held to the brief checks at the
// tip of its BASE of this moment, and a card that no longer passes returns the line the tick
// parks it with, "BRIEF DRIFT check=<check> line=<n>: <finding>". The evidence and the
// answers are cached for the tick alone, keyed by the snapshot's clock: the first card of a
// tick whose Now moved drops both, so the base tip is read again and a brief that passed at
// an earlier tip is not dealt on the stale answer. nil is returned for a brief that names no
// PATHS: line, no card brief.
func (a *app) briefAtTheBase() func(*sprint.Snapshot, *sprint.Card) string {
	type bkey struct{ repo, ref, pin string }
	var tick time.Time
	evidence := map[bkey]swarm.BriefBase{}
	seen := map[string]string{}
	l := &lander{a: a}
	return func(s *sprint.Snapshot, c *sprint.Card) string {
		if !s.Now.Equal(tick) { // a new tick: the base tip of this moment, never the last tick's
			tick = s.Now
			evidence = map[bkey]swarm.BriefBase{}
			seen = map[string]string{}
		}
		brief := c.F("brief")
		if brief == "" {
			return ""
		}
		if _, ok := swarm.CardHeaderValue([]byte(brief), "PATHS"); !ok {
			return ""
		}
		if line, ok := seen[brief]; ok {
			return line
		}
		var listed []string
		for _, r := range basesInUse(s) {
			if !slices.Contains(listed, r.Base) {
				listed = append(listed, r.Base)
			}
		}
		friends := map[string][]string{}
		for _, f := range s.Friends {
			tiers := f.Tiers
			if len(tiers) == 0 {
				tiers = sprint.Split(f.Class)
			}
			friends[f.Name] = tiers
		}
		cb := swarm.ReadCardBase([]byte(brief))
		bb := swarm.BriefBase{Friends: friends, Listed: listed}
		if cb.Ref != "" && cb.Named != "" {
			k := bkey{cb.Named, cb.Ref, cb.Sha}
			got, ok := evidence[k]
			if !ok {
				got = a.briefBaseAt(context.Background(), l, cb)
				evidence[k] = got
			}
			bb.Repo, bb.Sha, bb.Missing, bb.Gone = got.Repo, got.Sha, got.Missing, got.Gone
		}
		line := ""
		if fs, _ := swarm.LintBrief([]byte(brief), bb); len(fs) > 0 {
			f := fs[0]
			line = fmt.Sprintf("BRIEF DRIFT check=%s line=%d: %s", f.Check, f.Line, oneline.Cap(f.Excerpt, oneline.TailBytes))
		}
		seen[brief] = line
		return line
	}
}

// cmdLint is the lint verb: it runs the brief checks (swarm.LintBrief) on the briefs of the
// named primaries against their base tips and prints each finding as a LINT DRIFT line and
// each corrected header line as a LINT FIX line (docs/SPEC-SPRINT.md section 11, the brief
// checks). It is a read: nothing is written. Exit 1 when a brief fails, 0 when every named
// brief passes.
func (a *app) cmdLint(args []string, stdout, stderr io.Writer) int {
	const name = "lint"
	fs, c := a.verbSetup(name)
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(ids) == 0 {
		return refuse(stderr, name, "wants one or more primary ids: lint <id>...")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	var checks []briefCheck
	for _, id := range ids {
		card := s.Work.Card(id)
		if card == nil {
			return refuse(stderr, name, "no primary "+id+" on the work table")
		}
		checks = append(checks, briefCheck{id: id, brief: card.F("brief")})
	}
	rows, code := a.briefLintRows(name, st, true, stderr, checks...)
	if code != 0 {
		return code
	}
	lines, findings, _ := briefFindingsLines(rows)
	for _, x := range lines {
		fmt.Fprintln(stdout, x)
	}
	if findings > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "LINT OK %d brief(s) pass the checks at their base\n", len(checks))
	return 0
}

// pinOf is the @<sha> a BASE: line pins, "" when it pins none.
func pinOf(cb swarm.CardBase) string {
	if cb.Sha == "" {
		return ""
	}
	return "@" + cb.Sha
}

// briefBaseAt is the evidence at one card's base: the lander's clone of its repository and
// the commit at the base's tip (or the commit BASE: pins, cb.Sha), with Missing naming what
// could not be had, and Gone when origin holds no branch of the base's name.
func (a *app) briefBaseAt(ctx context.Context, l *lander, cb swarm.CardBase) swarm.BriefBase {
	if cb.Repo == "" {
		return swarm.BriefBase{Missing: "REPO " + cb.Named + " is no repository a clone can be made of, so the brief was not read at its base"}
	}
	ctx, cancel := context.WithTimeout(ctx, briefBaseFetch)
	defer cancel()
	dir, why := l.clone(ctx, cb.Repo)
	if why != "" {
		return swarm.BriefBase{Missing: "the lander's clone of " + cb.Named + " could not be had: " + why}
	}
	if _, err := l.git(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/"+cb.Ref+":"+briefBaseRef+cb.Ref); err != nil {
		heads, lerr := l.git(ctx, dir, "ls-remote", "--heads", "origin", "refs/heads/"+cb.Ref)
		if lerr == nil && strings.TrimSpace(heads) == "" {
			return swarm.BriefBase{Gone: true, Missing: "origin " + cb.Named + " holds no branch " + cb.Ref + ", so the brief was not read at its base"}
		}
		return swarm.BriefBase{Missing: "the base " + cb.Ref + " could not be fetched into " + dir + ": " + firstLine("", err)}
	}
	want := briefBaseRef + cb.Ref
	if cb.Sha != "" {
		want = cb.Sha
	}
	sha, err := l.git(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", want+"^{commit}")
	if err != nil || sha == "" {
		return swarm.BriefBase{Missing: "the base " + cb.Ref + pinOf(cb) + " is no commit in " + dir}
	}
	return swarm.BriefBase{Repo: dir, Sha: sha}
}
