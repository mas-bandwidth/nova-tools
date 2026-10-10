package main

import (
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

// widened is what brief --widen edits a held card's brief to, in place: the brief, and
// where its next attempt starts.
type widened struct {
	brief string
	carry member.Carry
}

// widen is the widened brief of a card held for PATHS too narrow (docs/SPEC-SPRINT.md section
// 2, "recut-widen-r.w1: a HOLD's PATHS-PROPOSED line widens the card in place"): its latest
// attempt's report's PATHS-PROPOSED globs, each read as the path before its prose (proposals),
// each one that stays in the repository and names a file at the brief's base or at the
// attempt's pushed head, joined to the old PATHS on every PATHS: line, and a CARRY: line
// naming that head, so the card's next attempt starts from it (member.Carried). why refuses
// it, naming every problem: no attempt, no line, no pushed head, no clone to read the trees
// in, a glob that climbs out with .. or names no file, and an item with no path before its prose.
func (a *app) widen(ctx context.Context, st *store.Store, id, repoDir string) (w widened, why string, err error) {
	v, err := st.CardOf(ctx, id)
	if err != nil {
		return w, "", err
	}
	if v.Primary == nil || !v.Primary.Placed() {
		return w, "no primary " + id + " on the work table", nil
	}
	var last *sprint.Card
	for _, c := range v.Work {
		if last == nil || c.Int("attempt") > last.Int("attempt") {
			last = c
		}
	}
	if last == nil {
		return w, id + " has no attempt, so no report proposes PATHS", nil
	}
	n := last.Int("attempt")
	ps, ok := proposals(last.F("report"))
	if !ok || len(ps) == 0 {
		return w, "the report of " + id + " attempt " + strconv.Itoa(n) + " has no PATHS-PROPOSED line (docs/SPEC-CARD-CONTRACT.md section 4): widen it by hand with brief " + id + " --brief-file <path>", nil
	}
	head := last.F("head")
	if !typedrec.IsFullSha(head) {
		return w, id + " attempt " + strconv.Itoa(n) + " pushed no head to start its next attempt from", nil
	}
	brief := v.Primary.F("brief")
	base := swarm.ReadCardBase([]byte(brief))
	if repoDir == "" {
		root, err := a.landRoot()
		if err != nil || base.Repo == "" {
			return w, "no clone of the card's repository to read its trees in: name one with --repo-dir <clone>", nil
		}
		repoDir = filepath.Join(root, repoDirName(base.Repo))
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		return w, "no clone at " + repoDir + ": name one with --repo-dir <clone>", nil
	}
	files, why := a.widenTrees(ctx, repoDir, base, head, last.F("branch"))
	if why != "" {
		return w, why, nil
	}
	var globs, bad []string
	for _, p := range ps {
		if p.path == "" {
			bad = append(bad, p.item+": no path before its prose")
			continue
		}
		globs = append(globs, p.path)
	}
	for _, g := range globs {
		if path.IsAbs(g) || slices.Contains(strings.Split(g, "/"), "..") {
			bad = append(bad, g+" climbs out of the repository")
		} else if _, err := path.Match(g, ""); err != nil {
			bad = append(bad, g+" is no glob: "+err.Error())
		} else if !slices.ContainsFunc(files, func(f string) bool { return globNames(g, f) }) {
			bad = append(bad, g+" names no file at "+baseName(base)+" or at the head "+head[:12])
		}
	}
	if len(bad) > 0 {
		return w, strings.Join(bad, "; "), nil
	}
	w.carry = member.Carry{Card: id, Attempt: n, Head: head}
	w.brief = widenBrief(brief, globs, member.CarryLine(w.carry))
	return w, "", nil
}

// proposal is one comma-separated item of a PATHS-PROPOSED line: item as the writer wrote it,
// and path, the path at its start, empty when the item has prose but no path.
type proposal struct {
	item, path string
}

// proposals is every item of the first PATHS-PROPOSED line of report, in the order written
// (docs/SPEC-CARD-CONTRACT.md section 4): the line runs to its end, each comma-separated item
// is a path up to its first whitespace, dash or semicolon and the rest of the item is the
// writer's reason, read as prose and ignored. ok is false when report has no such line.
func proposals(report string) (ps []proposal, ok bool) {
	_, rest, ok := strings.Cut(report, member.ProposedKey)
	if !ok {
		return nil, false
	}
	rest, _, _ = strings.Cut(rest, "\n")
	for _, item := range strings.Split(rest, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		ps = append(ps, proposal{item: item, path: proposedPath(item)})
	}
	return ps, true
}

// proposedPath is the path at the start of one PATHS-PROPOSED item: up to its first whitespace,
// dash or semicolon, that punctuation and everything after it the writer's reason. It is ""
// when the item has no path before its prose. A hyphen inside a path (cmd/nova-sprint) is not
// a dash; a dash is the punctuation a writer puts between the path and the reason.
func proposedPath(item string) string {
	s := strings.Trim(item, " \t`")
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsSpace(r) || r == ';' || proseDash(r) || (r == '-' && (i+1 == len(rs) || unicode.IsSpace(rs[i+1]))) {
			return strings.Trim(string(rs[:i]), " \t`")
		}
	}
	return s
}

// proseDash is whether r is a dash a writer puts between a path and its reason. The ASCII
// hyphen-minus is not one: a path names it (cmd/nova-sprint).
func proseDash(r rune) bool {
	switch r {
	case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212', '\ufe58', '\ufe63', '\uff0d':
		return true
	}
	return false
}

// widenTrees is every file of the base's tree and the head's, read in the clone at dir; the
// head's branch and the base are fetched from origin when the clone lacks them.
func (a *app) widenTrees(ctx context.Context, dir string, base swarm.CardBase, head, branch string) (files []string, why string) {
	git := func(args ...string) (string, error) {
		res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: true}, args...)
		return strings.TrimSpace(string(res.Stdout)), err
	}
	if _, err := git("cat-file", "-e", head+"^{commit}"); err != nil && branch != "" {
		_, _ = git("fetch", "-q", "--no-tags", "origin", branch) // ignored: the check below says what is missing
	}
	if _, err := git("cat-file", "-e", head+"^{commit}"); err != nil {
		return nil, "the head " + head + " is not in " + dir + " and origin's " + branch + " did not bring it"
	}
	at := base.Sha
	if at == "" {
		if base.Ref == "" {
			return nil, "the brief names no BASE: to read the base's files at"
		}
		var err error
		if at, err = git("rev-parse", "--verify", "-q", "refs/remotes/origin/"+base.Ref+"^{commit}"); err != nil {
			if _, err := git("fetch", "-q", "--no-tags", "origin", base.Ref); err != nil {
				return nil, "the base " + base.Ref + " is not on origin of " + dir
			}
			at = "FETCH_HEAD"
		}
	}
	for _, rev := range []string{at, head} {
		out, err := git("ls-tree", "-r", "--name-only", rev)
		if err != nil {
			return nil, "the files at " + rev + " could not be listed in " + dir
		}
		files = append(files, strings.Split(out, "\n")...)
	}
	return files, ""
}

// widenBrief is brief with every PATHS: line the union of its globs and globs, each once in
// that order, and the CARRY: line carry in its header, in place of one there or after line 1.
// Each line is read by the header's one reader (cardhdr.KeyValue, as decide.CardPaths reads
// PATHS), so the line rewritten is the line every gate reads.
func widenBrief(brief string, globs []string, carry string) string {
	lines := strings.Split(brief, "\n")
	var union []string
	at := -1
	for i, l := range lines {
		if k, old, ok := cardhdr.KeyValue(strings.TrimSpace(l)); ok && k == cardhdr.KeyPaths && at < 0 {
			at = i
			for _, g := range strings.Split(old, ",") {
				if g = strings.TrimSpace(g); g != "" && !slices.Contains(union, g) {
					union = append(union, g)
				}
			}
		}
	}
	for _, g := range globs {
		if !slices.Contains(union, g) {
			union = append(union, g)
		}
	}
	set := cardhdr.KeyPaths + ": " + strings.Join(union, ",")
	carried := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		k, _, ok := cardhdr.KeyValue(t)
		switch {
		case ok && k == cardhdr.KeyPaths:
			lines[i] = l[:len(l)-len(strings.TrimLeft(l, " \t"))] + set
		case strings.HasPrefix(t, member.CarryKey) && !carried:
			lines[i], carried = carry, true
		}
	}
	if at < 0 {
		lines = slices.Insert(lines, 1, set)
	}
	if !carried {
		lines = slices.Insert(lines, 1, carry)
	}
	return strings.Join(lines, "\n")
}

// globNames is whether glob g names file f: f matches it, or f is under the directory it names.
func globNames(g, f string) bool {
	m, _ := path.Match(g, f) // ignored: g was checked to be a glob
	return m || strings.HasPrefix(f, strings.TrimSuffix(g, "/")+"/")
}

// baseName is the base a refusal names: its ref, else its sha.
func baseName(b swarm.CardBase) string {
	if b.Ref != "" {
		return b.Ref
	}
	return b.Sha
}

// widenRefused is a widen the verb ran and said no to: the refusal's line, exit 1.
func widenRefused(stderr io.Writer, why string) int {
	refuse(stderr, "brief", why+"; nothing was changed")
	return 1
}
