package main

import (
	"fmt"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

// verify gates links, coverage and frontmatter over one tree. It finds; the
// author decides. An empty side of a glob is a broken check, not a pass.

type issue struct{ kind, detail string }

var (
	wikilink   = regexp.MustCompile(`\[\[([^\[\]]+?)\]\]`)
	fence      = regexp.MustCompile("^[ \t]{0,3}(`{3,}|~{3,})")
	quotedWiki = regexp.MustCompile("`[ \t]*\\[\\[[^\\[\\]]+?\\]\\][ \t]*`")
	mdLink     = regexp.MustCompile(`\]\(([^)\n]+)\)`)
	scheme     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
)

// maskCode blanks fenced code and backticked wikilinks, so a [[link]] shown as an example is not a link.
func maskCode(s string) string {
	lines := strings.Split(s, "\n")
	open := ""
	for i, ln := range lines {
		if m := strings.TrimLeft(fence.FindString(ln), " \t"); m != "" {
			if open == "" {
				open = m
			} else if m[0] == open[0] && len(m) >= len(open) {
				open = ""
			}
			lines[i] = ""
		} else if open != "" {
			lines[i] = ""
		} else {
			lines[i] = quotedWiki.ReplaceAllString(ln, " ")
		}
	}
	return strings.Join(lines, "\n")
}

// linkTarget is the .md file a markdown link destination points at, if it is a relative one.
func linkTarget(dest string) (string, bool) {
	t := strings.TrimSpace(dest)
	if strings.HasPrefix(t, "<") {
		if end := strings.IndexByte(t, '>'); end >= 0 {
			t = t[1:end]
		}
	} else if i := strings.IndexAny(t, " \t"); i >= 0 {
		t = t[:i]
	}
	if t == "" || t[0] == '#' || t[0] == '/' || scheme.MatchString(t) {
		return "", false
	}
	if i := strings.IndexAny(t, "#?"); i >= 0 {
		t = t[:i]
	}
	return t, strings.HasSuffix(t, ".md")
}

// named reports whether stem appears in hay as a whole word (letters, digits, - and _ continue a word).
func named(hay, stem string) bool {
	word := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b >= 0x80
	}
	for i := 0; stem != "" && i < len(hay); {
		j := strings.Index(hay[i:], stem)
		if j < 0 {
			return false
		}
		s, e := i+j, i+j+len(stem)
		if (s == 0 || !word(hay[s-1])) && (e == len(hay) || !word(hay[e])) {
			return true
		}
		i = s + 1
	}
	return false
}

func verify(r *roots, links string, coverage, front, exempt multi, failMax int, refuse func(string) int, fail func(error) int, stdout, stderr io.Writer) int {
	switch {
	case links != "gate" && links != "info":
		return refuse(fmt.Sprintf("--links must be gate or info (got %q); refusing to guess", links))
	case failMax < 0:
		return refuse("--fail-max must be zero or more; 0 means print them all")
	case len(r.root) != 1:
		return refuse(fmt.Sprintf("--root names exactly one tree for verification, but %d were given; verify one tree per run", len(r.root)))
	case len(coverage) == 0 && len(front) == 0 && links == "info":
		return refuse("no gating check requested (no --coverage, no --frontmatter, --links=info) — a run that cannot fail is not a verification; add --links gate, --coverage or --frontmatter")
	case len(exempt) > 0 && len(front) == 0:
		return refuse("--exempt only applies to --frontmatter, which was not given; add --frontmatter <glob> or drop --exempt")
	}
	files, texts, _, err := r.load(r.root[0])
	if err != nil {
		return fail(err)
	}
	glob := func(g string) []string {
		var out []string
		for _, f := range files {
			name := f
			if !strings.Contains(g, "/") { // a glob with no slash matches a file name at any depth
				name = path.Base(f)
			}
			if ok, _ := path.Match(g, name); ok {
				out = append(out, f)
			}
		}
		return out
	}
	var gating, info []issue
	var checked strings.Builder // one line per rule: how many files it checked
	nCoverage, nFront := 0, 0
	for _, pair := range coverage {
		ga, gb, ok := strings.Cut(pair, ":")
		if !ok || ga == "" || gb == "" {
			return refuse(fmt.Sprintf("--coverage wants A:B (notes:indexes, two globs), got %q", pair))
		}
		as, bs := glob(ga), glob(gb)
		if len(as) == 0 || len(bs) == 0 {
			return refuse(fmt.Sprintf("coverage %s:%s matched %d and %d files — an empty side is a broken check, not a pass; fix the glob", ga, gb, len(as), len(bs)))
		}
		line(&checked, "VERIFY CHECKED", "rule", "coverage", "glob", pair, "files", len(as), "indexes", len(bs))
		var all strings.Builder
		inB, linked := map[string]bool{}, map[string]bool{}
		for _, b := range bs {
			all.WriteString(texts[b] + "\n")
			inB[b] = true
			for _, m := range mdLink.FindAllStringSubmatch(maskCode(texts[b]), -1) {
				if target, ok := linkTarget(m[1]); ok {
					linked[path.Join(path.Dir(b), target)] = true
				}
			}
		}
		for _, a := range as {
			stem := strings.TrimSuffix(path.Base(a), ".md")
			if !inB[a] && !linked[a] && !named(all.String(), stem) {
				gating = append(gating, issue{"coverage", fmt.Sprintf("%s: stem %q appears in no file matching %s", a, stem, gb)})
				nCoverage++
			}
		}
		for _, b := range bs {
			for _, m := range mdLink.FindAllStringSubmatch(maskCode(texts[b]), -1) {
				if target, ok := linkTarget(m[1]); ok {
					if resolved := path.Join(path.Dir(b), target); !slices.Contains(files, resolved) {
						gating = append(gating, issue{"backlink", fmt.Sprintf("%s links %s which does not exist (resolved %s)", b, strings.TrimSpace(m[1]), resolved)})
						nCoverage++
					}
				}
			}
		}
	}
	for _, g := range front {
		matched := glob(g)
		if len(matched) == 0 {
			return refuse(fmt.Sprintf("frontmatter glob %s matched nothing — a broken check, not a pass; fix the glob", g))
		}
		n := 0
		for _, f := range matched {
			if slices.ContainsFunc(exempt, func(e string) bool { return strings.HasPrefix(path.Base(f), e) }) {
				continue
			}
			n++
			if name, _, _ := frontmatter(texts[f]); name == "" {
				gating = append(gating, issue{"frontmatter", f + ": no name: in frontmatter"})
				nFront++
			}
		}
		line(&checked, "VERIFY CHECKED", "rule", "frontmatter", "glob", g, "files", n)
	}
	// a [[stem]] resolves to a file's stem or to a frontmatter name.
	stems := map[string]bool{}
	for _, f := range files {
		stems[strings.TrimSuffix(path.Base(f), ".md")] = true
		if name, _, _ := frontmatter(texts[f]); name != "" {
			stems[name] = true
		}
	}
	unresolved := map[string][]string{}
	for _, f := range files {
		for _, m := range wikilink.FindAllStringSubmatch(maskCode(texts[f]), -1) {
			stem, _, _ := strings.Cut(m[1], "|")
			stem, _, _ = strings.Cut(stem, "#")
			if stem = strings.TrimSpace(stem); stem != "" && !stems[stem] && len(unresolved[stem]) < 3 {
				unresolved[stem] = append(unresolved[stem], f)
			}
		}
	}
	var wl []issue
	for _, k := range slices.Sorted(maps.Keys(unresolved)) {
		wl = append(wl, issue{"wikilink", fmt.Sprintf("[[%s]] resolves to no file (e.g. from %s)", k, strings.Join(unresolved[k], ", "))})
	}
	if links == "gate" {
		gating = append(gating, wl...)
	} else {
		info = wl
	}
	// emit prints each kind up to failMax lines, then one MORE line for the rest.
	emit := func(w io.Writer, fs []issue, label, sep string) (total, shown int) {
		per, order := map[string]int{}, []string{}
		for _, f := range fs {
			if per[f.kind]++; per[f.kind] == 1 {
				order = append(order, f.kind)
			}
			total++
			if failMax == 0 || per[f.kind] <= failMax {
				shown++
				fmt.Fprintf(w, "VERIFY %s %s%s %s\n", label, f.kind, sep, esc(truncate(f.detail, 500)))
			}
		}
		for _, k := range order {
			if failMax > 0 && per[k] > failMax {
				more(w, "VERIFY", k, failMax, per[k], "--fail-max")
			}
		}
		return total, shown
	}
	fmt.Fprint(stdout, checked.String())
	nInfo, shownInfo := emit(stdout, info, "INFO", ":")
	nGating, shownGating := emit(stderr, gating, "FAIL", "")
	if nGating > 0 {
		fmt.Fprintf(stderr, "VERIFY FAIL gating=%d shown=%d info=%d coverage=%d frontmatter=%d links=%s\n", nGating, shownGating, nInfo, nCoverage, nFront, links)
		return 1
	}
	fmt.Fprintf(stdout, "VERIFY OK gating=0 info=%d shown=%d coverage=%d frontmatter=%d links=%s\n", nInfo, shownInfo, nCoverage, nFront, links)
	return 0
}
