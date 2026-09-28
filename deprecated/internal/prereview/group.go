package prereview

import (
	"context"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// Tool-PR mode (#2621). One question over a whole pull request scores its
// SIZE: over the eight tool pull requests of the 2026-09-22 dry pass the score
// ran Spearman -0.64 against diff bytes, and confidence was 0.00 on every one
// over 33 KB, three of them cut at DiffCap. So the question is asked once per
// changed-file group -- the files of one directory -- and the pull request's
// score is the LOWEST group's: a reader's verdict on a change is its weakest
// part, and no group is big enough to be scored for being big. A pull request
// with one group (every conformance cell) is asked exactly as before, over the
// same state, so the 122 recorded cell answers still replay.

// MaxGroups bounds the calls one pull request costs. Past it the groups are
// re-keyed by the first two path segments, then by the first; a group past
// DiffCap is then split into parts of whole files, and whatever still does not
// fit is folded into the last group.
const MaxGroups = 8

// FileGroup is one directory's share of a pull request's diff.
type FileGroup struct {
	Key   string
	Files []string
	Diff  string
}

// diffSection is one file's part of a unified diff.
type diffSection struct {
	path string
	text string
}

// splitDiff cuts a unified diff at its `diff --git` headers. A diff with none
// (a hand-built one) is one section with no path.
func splitDiff(diff string) []diffSection {
	out := make([]diffSection, 0)
	var cur *diffSection
	var b strings.Builder
	flush := func() {
		if cur != nil {
			cur.text = b.String()
			out = append(out, *cur)
		}
		b.Reset()
	}
	for _, line := range strings.SplitAfter(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			cur = &diffSection{path: diffGitPath(strings.TrimRight(line, "\n"))}
		} else if cur == nil {
			cur = &diffSection{}
		}
		b.WriteString(line)
	}
	flush()
	return out
}

// FileGroups is the pull request's diff by directory, in key order. Fixtures
// under testdata/ are left out of every group but the only one (they ride in
// the state's changed-files line): a group of recorded answers is not a change
// a reader scores. A diff with no file headers is one group, the whole diff.
func FileGroups(pr PR) []FileGroup {
	secs := splitDiff(pr.Diff)
	if len(secs) == 0 {
		return nil
	}
	if len(secs) == 1 && secs[0].path == "" {
		return []FileGroup{{Key: ".", Files: pr.Files, Diff: pr.Diff}}
	}
	kept := make([]diffSection, 0, len(secs))
	for _, s := range secs {
		if s.path == "" || strings.HasPrefix(s.path, "testdata/") || strings.Contains(s.path, "/testdata/") {
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		kept = secs
	}
	var g []FileGroup
	for depth := 0; depth <= 2; depth++ {
		if g = groupBy(kept, depth); len(g) <= MaxGroups {
			break
		}
	}
	return foldTail(splitOverCap(g, kept))
}

// splitOverCap packs a group whose diff is past DiffCap into parts of whole
// files, each under it where a file allows: a question over a truncated diff
// is the confidence-0.00 answer the dry pass got on every pull request over
// 33 KB.
func splitOverCap(groups []FileGroup, secs []diffSection) []FileGroup {
	text := make(map[string]string, len(secs))
	for _, s := range secs {
		text[s.path] += s.text
	}
	out := make([]FileGroup, 0, len(groups))
	for _, g := range groups {
		if len(g.Diff) <= DiffCap || len(g.Files) < 2 {
			out = append(out, g)
			continue
		}
		parts := make([]FileGroup, 0)
		cur := FileGroup{}
		for _, f := range g.Files {
			if len(cur.Files) > 0 && len(cur.Diff)+len(text[f]) > DiffCap {
				parts = append(parts, cur)
				cur = FileGroup{}
			}
			cur.Files = append(cur.Files, f)
			cur.Diff += text[f]
		}
		parts = append(parts, cur)
		for i := range parts {
			parts[i].Key = fmt.Sprintf("%s part %d of %d", g.Key, i+1, len(parts))
		}
		out = append(out, parts...)
	}
	return out
}

// foldTail folds every group past MaxGroups into the last one.
func foldTail(g []FileGroup) []FileGroup {
	if len(g) <= MaxGroups {
		return g
	}
	last := &g[MaxGroups-1]
	for _, extra := range g[MaxGroups:] {
		last.Key += "+" + extra.Key
		last.Files = append(last.Files, extra.Files...)
		last.Diff += extra.Diff
	}
	return g[:MaxGroups]
}

// groupBy keys each section by its directory (depth 0), its first two path
// segments (1) or its first (2).
func groupBy(secs []diffSection, depth int) []FileGroup {
	byKey := map[string]*FileGroup{}
	for _, s := range secs {
		k := groupKey(s.path, depth)
		g, ok := byKey[k]
		if !ok {
			g = &FileGroup{Key: k}
			byKey[k] = g
		}
		g.Files = append(g.Files, s.path)
		g.Diff += s.text
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]FileGroup, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byKey[k])
	}
	return out
}

func groupKey(p string, depth int) string {
	dir := path.Dir(p)
	if depth == 0 || dir == "." {
		return dir
	}
	segs := strings.Split(dir, "/")
	if n := 3 - depth; len(segs) > n {
		segs = segs[:n]
	}
	return strings.Join(segs, "/")
}

// groupLineRE is the state's second line in tool-PR mode; the replaying asker
// reads the group number back out of it.
var groupLineRE = regexp.MustCompile(`(?m)^file group ([0-9]+) of ([0-9]+): `)

// GroupState is State over one file group: the same card, RESULT and changed
// files, a line saying which group this is, and only the group's diff.
func GroupState(pr PR, card Card, g FileGroup, k, n int) string {
	one := pr
	one.Diff = g.Diff
	s := State(one, card)
	first, rest, _ := strings.Cut(s, "\n")
	return first + "\n" + fmt.Sprintf("file group %d of %d: %s (%s); the pull request's score is the lowest group's, so score this group's change alone\n",
		k, n, g.Key, strings.Join(g.Files, ", ")) + rest
}

// groupFromState is the group number GroupState wrote, or 0.
func groupFromState(state string) int {
	if m := groupLineRE.FindStringSubmatch(state); m != nil {
		k, _ := strconv.Atoi(m[1])
		return k
	}
	return 0
}

// GroupScore is a pull request's score in tool-PR mode.
type GroupScore struct {
	Raw, Conf float64
	// Groups is how many questions were asked; Lowest the group that set the
	// score ("" when there was one).
	Groups int
	Lowest string
}

// ScoreGroups asks the score question once per file group and keeps the
// lowest answer. One group is the old single question over the old state. An
// error on any group is no score at all: a minimum over the groups that
// answered is not the pull request's minimum.
func ScoreGroups(ctx context.Context, a Asker, qs map[string]decide.Question, pr PR, card Card) (GroupScore, error) {
	groups := FileGroups(pr)
	if len(groups) <= 1 && (len(groups) == 0 || groups[0].Diff == pr.Diff) {
		raw, conf, err := askScore(ctx, a, State(pr, card), qs)
		return GroupScore{Raw: raw, Conf: conf, Groups: 1}, err
	}
	best := GroupScore{Groups: len(groups), Raw: math.Inf(1)}
	for i, g := range groups {
		raw, conf, err := askScore(ctx, a, GroupState(pr, card, g, i+1, len(groups)), qs)
		if err != nil {
			return GroupScore{}, fmt.Errorf("file group %d of %d (%s): %w", i+1, len(groups), g.Key, err)
		}
		if raw < best.Raw {
			best.Raw, best.Conf, best.Lowest = raw, conf, g.Key
		}
	}
	return best, nil
}

func askScore(ctx context.Context, a Asker, state string, qs map[string]decide.Question) (raw, conf float64, err error) {
	answers, err := a.Ask(ctx, state, qs)
	if err != nil {
		return 0, 0, err
	}
	ans, ok := answers["score"]
	if !ok {
		return 0, 0, fmt.Errorf("prereview: the provider returned no score answer")
	}
	return ans.Score, ans.Confidence, nil
}

// RecordingAsker records every answer it passes through as a fixture, one per
// pull request, or one per file group in tool-PR mode, so a paid pass is
// replayed rather than paid for again.
type RecordingAsker struct {
	Inner Asker
	Dir   string
	Repo  string
	// Failed, when set, hears a fixture that could not be written and the
	// answer still stands (the call was paid for); nil makes it the error.
	Failed func(error)
}

// Ask asks the inner asker and records its score answer.
func (a RecordingAsker) Ask(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, error) {
	answers, err := a.Inner.Ask(ctx, state, qs)
	if err != nil {
		return answers, err
	}
	pr, err := prFromState(state)
	if err == nil {
		s := answers["score"]
		err = RecordGroupFixture(a.Dir, a.Repo, pr, groupFromState(state), s.Score, s.Confidence)
	}
	if err != nil && a.Failed != nil {
		a.Failed(err)
		return answers, nil
	}
	return answers, err
}

// Spearman is the rank correlation of x and y (ties take their mean rank),
// the measure the dry pass used for "the score tracks the diff size". NaN
// when either side is constant or the lengths differ.
func Spearman(x, y []float64) float64 {
	if len(x) != len(y) || len(x) < 2 {
		return math.NaN()
	}
	rx, ry := ranks(x), ranks(y)
	var mx, my float64
	for i := range rx {
		mx += rx[i]
		my += ry[i]
	}
	mx /= float64(len(rx))
	my /= float64(len(ry))
	var sxy, sxx, syy float64
	for i := range rx {
		dx, dy := rx[i]-mx, ry[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}

func ranks(v []float64) []float64 {
	idx := make([]int, len(v))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return v[idx[a]] < v[idx[b]] })
	r := make([]float64, len(v))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && v[idx[j+1]] == v[idx[i]] {
			j++
		}
		mean := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			r[idx[k]] = mean
		}
		i = j + 1
	}
	return r
}
