package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/hygiene"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

func init() {
	// preflight is a read: it changes nothing and needs no actor. The verb
	// registers its own class here, beside its own code, and the class test holds
	// every verb to one.
	verbClasses["preflight"] = classRead
}

// preflight is the read-only check of a batch of briefs before they are
// dispatched (docs/SPEC-SPRINT.md, `### preflight`). It holds each brief to the
// card lint add uses, reads every DEPENDS-ON as a card of the table or of the
// directory and none dropped, the PATHS against a ready, working or review
// card's PATHS and another brief's, the TEST against a test git grep finds at
// BASE in --repo-dir, and BASE in --repo-dir. It prints a table, PREFLIGHT OK or
// PREFLIGHT FAIL with the count, and writes nothing.
func (a *app) cmdPreflight(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("preflight")
	briefDir := fs.String("brief-dir", "", "one brief per *.md file in this directory, in byte order of file name: preflight checks each one")
	repoDir := fs.String("repo-dir", "", "a repository clone: TEST names a test git grep finds at BASE, and BASE is checked there; without it both are unchecked")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "preflight", argErr("takes no words ", err, pos...))
	}
	if *briefDir == "" {
		return refuse(stderr, "preflight", "wants --brief-dir <dir>, the brief files it checks")
	}
	files, err := decide.CardFilePaths(*briefDir)
	if err != nil {
		return refuse(stderr, "preflight", "--brief-dir: "+err.Error())
	}
	// a directory with no *.md file is a batch of none: vacuously clean, exit 0,
	// while a directory that cannot be read is refused above.
	var st *store.Store
	rs, code := a.briefRules("preflight", "", c, &st, stderr)
	if code != 0 {
		return code
	}
	ctx := context.Background()
	cards, code := a.preflightCards(files, stderr)
	if code != 0 {
		return code
	}
	if st == nil {
		if st, err = a.store(*c); err != nil {
			return refuse(stderr, "preflight", err.Error())
		}
	}
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return a.readFailed("preflight", err, stderr)
	}
	v, code := a.preflightCheck(ctx, st, cards, snap, rs, *repoDir, stderr)
	if code != 0 {
		return code
	}
	if c.json {
		b, _ := json.Marshal(v)
		fmt.Fprintln(stdout, string(b))
		return preflightExit(v)
	}
	for _, p := range v.Briefs {
		if len(p.Findings) == 0 {
			fmt.Fprintf(stdout, "PREFLIGHT %s OK\n", oneline.Escape(p.ID))
			continue
		}
		for _, f := range p.Findings {
			fmt.Fprintf(stdout, "PREFLIGHT %s FAIL %s: %s\n", oneline.Escape(p.ID), f.Key, oneline.Escape(f.Why))
		}
	}
	if *repoDir == "" {
		fmt.Fprintln(stdout, "NOTE preflight read no --repo-dir: TEST and BASE are unchecked")
	}
	if v.OK {
		fmt.Fprintf(stdout, "PREFLIGHT OK briefs=%d\n", len(v.Briefs))
	} else {
		fmt.Fprintf(stdout, "PREFLIGHT FAIL briefs=%d failed=%d\n", len(v.Briefs), v.Failed)
	}
	return preflightExit(v)
}

// preflightCard is one brief of the batch and the header lines preflight reads.
type preflightCard struct {
	id    string
	file  string
	brief string
	paths []string
	needs []string
	test  string
	base  string
}

// preflightCards reads the brief files in order: each card's id is the file's
// base name without .md (as add reads it), its PATHS, DEPENDS-ON, TEST and BASE
// from the typed header block (swarm.CardHeaderValue, swarm.CardPaths).
func (a *app) preflightCards(files []string, stderr io.Writer) ([]preflightCard, int) {
	cards := make([]preflightCard, 0, len(files))
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		if !sprint.ValidID(id) {
			return nil, refuse(stderr, "preflight", fmt.Sprintf("%s: the card id is the file's base name without .md, and %q is not one (letters, digits, _ and -)", path, id))
		}
		text, err := readTextFile(path, briefReadCap)
		if err != nil {
			return nil, refuse(stderr, "preflight", fmt.Sprintf("%s: %v", path, err))
		}
		brief := strings.TrimSuffix(text, "\n")
		test, _ := swarm.CardHeaderValue([]byte(brief), "TEST")
		base, _ := swarm.CardHeaderValue([]byte(brief), "BASE")
		cards = append(cards, preflightCard{id: id, file: path, brief: brief,
			paths: swarm.CardPaths([]byte(brief)), needs: briefNeeds(brief), test: test, base: base})
	}
	return cards, 0
}

// preflightFinding is one reason a brief of the batch is not ready: the check
// (form, depends-on, paths, test, base) and the one line that says it.
type preflightFinding struct {
	Key string `json:"key"`
	Why string `json:"why"`
}

// preflightBrief is one brief of the batch and what preflight found in it.
type preflightBrief struct {
	ID       string             `json:"id"`
	File     string             `json:"file"`
	Findings []preflightFinding `json:"findings,omitempty"`
}

// preflightView is the whole read, one object under --json.
type preflightView struct {
	Briefs []preflightBrief `json:"briefs"`
	Failed int              `json:"failed"`
	Repos  bool             `json:"repos,omitempty"` // --repo-dir was given
	OK     bool             `json:"ok"`
}

func preflightExit(v preflightView) int {
	if v.OK {
		return 0
	}
	return 1
}

// preflightCheck builds the batch's findings: the card lint (lintBriefReads, the
// one add uses), the DEPENDS-ON closure against the table and the directory, the
// PATHS against the live cards and the other briefs, and, with --repo-dir, TEST
// and BASE read with git.
func (a *app) preflightCheck(ctx context.Context, st *store.Store, cards []preflightCard, snap *sprint.Snapshot, rs ruleSet, repoDir string, stderr io.Writer) (preflightView, int) {
	inDir := map[string]bool{}
	for _, p := range cards {
		inDir[p.id] = true
	}
	live := preflightLive(snap)
	var v preflightView
	v.Repos = repoDir != ""
	failed := 0
	for _, p := range cards {
		var findings []preflightFinding
		add := func(key, why string) { findings = append(findings, preflightFinding{Key: key, Why: why}) }
		// form: the card lint add holds every brief to
		if modelWhy, lints := lintBriefReads(p.brief, rs); modelWhy != "" {
			add("form", modelLinesWhy(modelWhy))
		} else {
			for _, f := range lints {
				add("form", fmt.Sprintf("%s: line %d: %s", f.Check, f.Line, f.Excerpt))
			}
		}
		// DEPENDS-ON: every id a card of the table or of the directory, none dropped
		for _, need := range p.needs {
			if inDir[need] {
				continue
			}
			recs, err := st.Records(ctx, sprint.Work, []string{need})
			if err != nil {
				return v, a.readFailed("preflight", err, stderr)
			}
			switch {
			case len(recs) == 0:
				add("DEPENDS-ON", fmt.Sprintf("%s is not on the table or in the directory", need))
			case !recs[0].Placed() && recs[0].F("outcome") == "dropped":
				add("DEPENDS-ON", fmt.Sprintf("%s is dropped", need))
			}
		}
		// PATHS: overlapping a ready, working or review card's PATHS, or another brief's
		for _, path := range p.paths {
			for _, lc := range live {
				for _, lp := range lc.paths {
					if preflightOverlap(path, lp) {
						add("PATHS", fmt.Sprintf("%s overlaps the card %s (%s)", path, lc.id, lp))
					}
				}
			}
			for _, other := range cards {
				if other.id == p.id {
					continue
				}
				for _, op := range other.paths {
					if preflightOverlap(path, op) {
						add("PATHS", fmt.Sprintf("%s overlaps %s (%s)", path, other.id, op))
					}
				}
			}
		}
		// TEST and BASE, at BASE in --repo-dir
		if p.test == "" {
			add("TEST", "no test named (TEST: [-tags <tags>] <package> <TestName>)")
		} else if tl, why := cardhdr.ParseTest(p.test); why != "" {
			add("TEST", why)
		} else if repoDir != "" && !tl.None {
			base := p.base
			if base == "" {
				base = "HEAD"
			}
			if _, err := a.preflightGit(ctx, repoDir, "grep", "-q", "-F", "--", tl.Name, base, "--", "*.go"); err != nil {
				add("TEST", fmt.Sprintf("%s is not at %s in %s", tl.Name, base, repoDir))
			}
		}
		if repoDir != "" && p.base != "" {
			if _, err := a.preflightGit(ctx, repoDir, "rev-parse", "--verify", "--quiet", p.base+"^{commit}"); err != nil {
				add("BASE", fmt.Sprintf("%s is not in %s", p.base, repoDir))
			}
		}
		if len(findings) > 0 {
			failed++
		}
		v.Briefs = append(v.Briefs, preflightBrief{ID: p.id, File: p.file, Findings: findings})
	}
	v.Failed = failed
	v.OK = failed == 0
	return v, 0
}

// preflightLive is the ready, working and review cards of the work table with the
// PATHS of their briefs: the cards a new brief's work would run beside.
func preflightLive(snap *sprint.Snapshot) []preflightCard {
	var out []preflightCard
	if snap == nil || snap.Work == nil {
		return out
	}
	for _, card := range snap.Work.Cards() {
		if !card.Placed() {
			continue
		}
		if card.Col != string(sprint.Ready) && card.Col != string(sprint.Working) && card.Col != string(sprint.Review) {
			continue
		}
		brief := card.F("brief")
		if brief == "" {
			continue
		}
		out = append(out, preflightCard{id: card.ID, paths: swarm.CardPaths([]byte(brief))})
	}
	return out
}

// preflightOverlap answers whether two PATHS globs name any file in common: the
// globs are equal, or one matches the other as hygiene.MatchGlob matches a path,
// so a broad `docs/*.md` overlaps a file below it that the glob matches.
func preflightOverlap(a, b string) bool {
	return a == b || hygiene.MatchGlob(a, b) || hygiene.MatchGlob(b, a)
}

// preflightGit runs one git in the repository --repo-dir names.
func (a *app) preflightGit(ctx context.Context, dir string, args ...string) (string, error) {
	return gitrun.Output(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: true}, args...)
}
