package main

// `set check --evaluate` (#2664) derives a unit's done from its :acceptance rather
// than from a :status a person typed. Two criteria are evaluable here, both through
// internal/landed and so through one gh seam:
//
//	(:kind :landed :subject "pr:<o/r>#<n>" :predicate :merged-or-closed-in-base)
//	(:kind :merged :subject "pr:<o/r>#<n>" :predicate :merged-at)
//
// :landed on a closed PR is the lander's rule: merging its head into the base
// changes nothing (git merge-tree --write-tree yields the base's own tree).
//
// A :test, :job or :attested criterion needs a record this verb does not read, so
// it is left to the document: a unit is decided by evidence only when EVERY
// criterion it names is evaluable, or when an evaluable one fails (a failed
// criterion is enough to say not done). A criterion the forge could not answer is
// unknown and counts as not done, printed so, never guessed.
//
// --write-status is the mechanical half: a unit whose criteria all hold and whose
// file still says :status "open" has those six bytes rewritten to "landed" and
// nothing else moves, so the diff is one line per unit and a person's comments,
// order and spacing survive.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/landed"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// ghRunner is the seam to the forge; the tests replace it and nothing in them
// dials a network. landedGitURL is where the merge fetches a repo from: nil is
// GitHub, and the tests point it at a repository in their own temp dir.
var (
	ghRunner     landed.Runner = landed.GH
	landedGitURL func(repo string) string
)

// defaultLandedCache is <user cache dir>/nova-work/landed: the bare repositories
// the lander's merge rule runs in are a cache, kept between runs so a repo's
// history is fetched once. An unknown cache dir leaves it empty, and a closed PR's
// criterion is then unknown rather than merged somewhere guessed.
func defaultLandedCache() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "nova-work", "landed")
}

// evaluator is one criterion's resolver, or nil when set check cannot evaluate it.
func evaluator(c worklang.Criterion) func(*landed.Evaluator, context.Context, string) landed.Verdict {
	switch {
	case c.Kind == "landed" && (c.Predicate == "merged-or-closed-in-base" || c.Predicate == "landed-in"):
		return (*landed.Evaluator).Landed
	case c.Kind == "merged" && c.Predicate == "merged-at":
		return (*landed.Evaluator).MergedAt
	}
	return nil
}

// evaluate prints one SET EVAL line per evaluable criterion and returns the
// evidence map Options.Evidence takes, plus the units whose criteria ALL hold --
// the ones --write-status may flip.
func evaluate(stdout io.Writer, ws *worklang.WorkSet, ev *landed.Evaluator) (map[string]bool, map[string]bool) {
	ctx := context.Background()
	// Every PR the criteria name is read up front, one call per repo (#3460), so
	// the loop below answers from the run's cache instead of one REST read each.
	var subjects []string
	for _, u := range ws.Units {
		for _, c := range u.Acceptance() {
			if u.ID != "" && evaluator(c) != nil {
				subjects = append(subjects, c.Subject)
			}
		}
	}
	ev.Prefetch(ctx, subjects)
	evidence, whole := map[string]bool{}, map[string]bool{}
	for _, u := range ws.Units {
		crits := u.Acceptance()
		if u.ID == "" || len(crits) == 0 {
			continue
		}
		evaluated, held := 0, true
		for _, c := range crits {
			resolve := evaluator(c)
			if resolve == nil {
				continue
			}
			evaluated++
			v := resolve(ev, ctx, c.Subject)
			fmt.Fprintf(stdout, "SET EVAL unit=%s criterion=%s kind=%s subject=%s holds=%s why=%s\n",
				oneline.Field(u.ID), field(c.ID), field(c.Kind), field(c.Subject), field(v.Word()), field(v.Why))
			if !v.Holds {
				held = false
			}
		}
		switch {
		case evaluated == 0:
			// nothing here says anything: the document's word stands
		case !held:
			evidence[u.ID] = false
		case evaluated == len(crits):
			evidence[u.ID], whole[u.ID] = true, true
		}
		// every evaluable criterion held but another is not evaluable here: the
		// document's word stands, because half the evidence is not a done.
	}
	return evidence, whole
}

// setBase is the branch "in base" means: --base, else the set's :base. The value
// lands in an API path, so anything but a plain ref name is refused.
func setBase(ws *worklang.WorkSet, flagBase string) (string, error) {
	base := strings.TrimSpace(flagBase)
	if base == "" {
		base = strings.TrimSpace(ws.Fields["base"].Text())
	}
	if base == "" {
		return "", fmt.Errorf("--evaluate needs --base <branch> or a :base on the work set; refusing to guess")
	}
	if strings.ContainsAny(base, " \t\n?#@%:") || strings.Contains(base, "..") || strings.HasPrefix(base, "-") {
		return "", fmt.Errorf("--base %q is not a branch name", base)
	}
	return base, nil
}

// writeStatus rewrites `:status "open"` to `:status "landed"` for each unit in
// flip, in place and nowhere else, and returns the units it rewrote. A unit whose
// :status is not the string "open" is left alone: "landed" and "done" already say
// it, and a keyword or other spelling is the person's to change.
func writeStatus(path string, data []byte, ws *worklang.WorkSet, flip map[string]bool) ([]statusFlip, error) {
	const from, to = `"open"`, `"landed"`
	var flips []statusFlip
	for _, u := range ws.Units {
		if !flip[u.ID] {
			continue
		}
		f, ok := u.Fields["status"]
		if !ok || f.Kind != worklang.String || f.Value != "open" {
			continue
		}
		if f.Offset < 0 || f.Offset+len(from) > len(data) || string(data[f.Offset:f.Offset+len(from)]) != from {
			return nil, fmt.Errorf("unit %q: :status at byte=%d is not spelled %s; refusing to rewrite", u.ID, f.Offset, from)
		}
		flips = append(flips, statusFlip{unit: u.ID, at: f.Offset})
	}
	if len(flips) == 0 {
		return nil, nil
	}
	// back to front, so an earlier offset is still true when it is reached
	sort.Slice(flips, func(i, j int) bool { return flips[i].at > flips[j].at })
	out := append([]byte(nil), data...)
	for _, fl := range flips {
		out = append(out[:fl.at], append([]byte(to), out[fl.at+len(from):]...)...)
	}
	if err := replaceFile(path, out); err != nil {
		return nil, err
	}
	sort.Slice(flips, func(i, j int) bool { return flips[i].at < flips[j].at })
	return flips, nil
}

type statusFlip struct {
	unit string
	at   int
}

// replaceFile writes beside the file and renames over it, so a reader never sees
// half a work set, and the file keeps its mode.
func replaceFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.WriteFile(name, data, info.Mode().Perm()); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, info.Mode().Perm()); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// percent is done over units, rounded down: 26 of 42 is 61, never a 62 the
// evidence does not reach. An empty set is 0.
func percent(done, units int) int {
	if units <= 0 {
		return 0
	}
	return done * 100 / units
}
