package memindex

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

// verify.go mechanizes the coverage passes a consolidation ritual otherwise
// does by hand — the O(m) reads that make every roll-up cost a scan of the
// whole self. Three checks, all OVER-REPORTING by design: an over-reporting
// loss check is the only kind worth having, because it finds and the author
// decides.
//
// Which findings gate is NOT decided here. The caller states it per run —
// see nova-memory's --links flag and SPEC.md. The port from which this came
// had unresolved wikilinks demoted to informational by a default flag, while
// its own spec promised a nonzero exit on findings; a script trusting the
// spec passed dangling links silently for weeks. Nothing here has a default
// about what counts.

// Finding is one verify observation. Kind is one of "coverage", "backlink",
// "frontmatter", "wikilink".
type Finding struct {
	Kind   string
	Detail string
}

// wikilinkRe matches the whole body of a [[...]], aliases and headings
// included; wikilinkTarget below reduces the body to the half that has to
// resolve. The regex this replaced excluded '|' and '#' from the body, so
// `[[target|alias]]` and `[[target#section]]` were never SCANNED — a caller
// who chose --links=gate as a wall got a whole common link class waved
// through silently, which is the exact wave-through this file argues against.
var wikilinkRe = regexp.MustCompile(`\[\[([^\[\]]+?)\]\]`)

// mdLinkRe matches the general inline-link destination `](dest)`; linkTarget
// below decides what is in scope. The regex this replaced was
// `\]\(([^)#?:]+\.md)\)`, whose character class excluded '#' and '?' from the
// WHOLE target — so `](gone.md#top)` never matched at all and a dangling
// anchored link passed the coverage wall green, exit 0, on a planted fault.
// Newlines stay excluded because a link destination never spans a line, and
// allowing them lets a stray `](` in prose swallow paragraphs.
var mdLinkRe = regexp.MustCompile(`\]\(([^)\n]+)\)`)

// schemeRe recognizes an absolute URI scheme, the same shape
// internal/check/links.go uses. Kept here rather than shared because
// memindex does no I/O beyond the fs.FS it is handed and depends on nothing.
var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// linkTarget reduces one inline-link destination to the relative .md path
// that must resolve, or reports that the link is out of scope. It follows
// internal/check/links.go:247-250: fragment-only, protocol-relative, and
// scheme-carrying targets are skipped, and the anchor is cut before the path
// is tested. Absolute ('/'-rooted) targets are skipped too — the spec bullet
// promises every RELATIVE .md link resolves, and resolving a repo-root-
// relative path against a corpus root the caller may have pointed anywhere
// below the repo would gate on false positives.
func linkTarget(dest string) (string, bool) {
	t := strings.TrimSpace(dest)
	// `](<dest with spaces>)` is the angle-bracket form; `](dest "title")`
	// puts a title after the destination. Neither is part of the path.
	if strings.HasPrefix(t, "<") {
		if end := strings.IndexByte(t, '>'); end >= 0 {
			t = t[1:end]
		}
	} else if i := strings.IndexAny(t, " \t"); i >= 0 {
		t = t[:i]
	}
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") ||
		strings.HasPrefix(t, "/") || schemeRe.MatchString(t) {
		return "", false
	}
	if i := strings.IndexAny(t, "#?"); i >= 0 {
		t = t[:i]
	}
	if !strings.HasSuffix(t, ".md") {
		return "", false
	}
	return t, true
}

// wikilinkTarget is the half of a wikilink body that has to resolve:
// everything before the first '|' (an alias) or '#' (a heading). A body that
// is only a heading (`[[#section]]`) reduces to nothing and is not a corpus
// reference at all.
func wikilinkTarget(body string) string {
	if i := strings.IndexAny(body, "|#"); i >= 0 {
		body = body[:i]
	}
	return strings.TrimSpace(body)
}

// Coverage checks one A:B pair — every file matching glob A must be named (by
// stem) in at least one file matching glob B, and every relative .md link
// inside the B files must point at a file that exists. That is the generic
// form of "every memory file has an index line, and every index line points
// at a real file"; the globs carry the layout, so the tool assumes none.
//
// An empty side is an error, never a pass: a coverage check whose A side
// matched nothing has not verified anything.
func Coverage(fsys fs.FS, globA, globB string) ([]Finding, error) {
	aFiles, err := fs.Glob(fsys, globA)
	if err != nil {
		return nil, fmt.Errorf("bad glob %q: %w", globA, err)
	}
	bFiles, err := fs.Glob(fsys, globB)
	if err != nil {
		return nil, fmt.Errorf("bad glob %q: %w", globB, err)
	}
	if len(aFiles) == 0 || len(bFiles) == 0 {
		return nil, fmt.Errorf("coverage %s:%s matched %d and %d files — an empty side is a broken check, not a pass",
			globA, globB, len(aFiles), len(bFiles))
	}
	sort.Strings(aFiles)
	sort.Strings(bFiles)

	// The B side, concatenated once; stems are matched against it.
	var bContent strings.Builder
	bSet := map[string]bool{}
	for _, b := range bFiles {
		raw, err := fs.ReadFile(fsys, b)
		if err != nil {
			return nil, err
		}
		bContent.WriteString(string(raw))
		bContent.WriteByte('\n')
		bSet[b] = true
	}
	bAll := bContent.String()

	var out []Finding
	for _, a := range aFiles {
		if bSet[a] {
			continue // an index file need not index itself
		}
		stem := strings.TrimSuffix(path.Base(a), ".md")
		if !strings.Contains(bAll, stem) {
			out = append(out, Finding{Kind: "coverage",
				Detail: fmt.Sprintf("%s: stem %q appears in no file matching %s", a, stem, globB)})
		}
	}
	// Backward: every relative .md link in B resolves.
	for _, b := range bFiles {
		raw, err := fs.ReadFile(fsys, b)
		if err != nil {
			return nil, err
		}
		dir := path.Dir(b)
		for _, m := range mdLinkRe.FindAllStringSubmatch(string(raw), -1) {
			target, ok := linkTarget(m[1])
			if !ok {
				continue
			}
			resolved := path.Clean(path.Join(dir, target))
			if _, err := fs.Stat(fsys, resolved); err != nil {
				// The destination is quoted AS WRITTEN so the reader can find
				// the line; resolved names what was actually stat'd.
				out = append(out, Finding{Kind: "backlink",
					Detail: fmt.Sprintf("%s links %s which does not exist (resolved %s)", b, strings.TrimSpace(m[1]), resolved)})
			}
		}
	}
	return out, nil
}

// Wikilinks reports every [[stem]] in the corpus that resolves to neither a
// file stem nor a frontmatter name. The aliased form [[stem|shown text]] and
// the heading form [[stem#section]] are scanned by their target half — a link
// whose alias is what the reader sees is still a link, and skipping the two
// commonest shapes made --links=gate a wall with a hole in it.
// Both resolution targets matter: a corpus
// that names files after their frontmatter slug has nothing enforcing it, and
// a link that reaches the content either way is not broken.
//
// Whether these findings gate is the caller's statement, never a default
// here: some corpora hold links open on purpose — a [[name]] that matches
// nothing yet marks something worth writing — and a gate at a high
// false-positive rate trains a reader to wave findings through.
func Wikilinks(fsys fs.FS, c *Corpus) ([]Finding, error) {
	stems := map[string]bool{}
	for _, f := range c.Files {
		stems[strings.TrimSuffix(path.Base(f), ".md")] = true
	}
	for i := range c.Chunks {
		if n := c.Chunks[i].FMName; n != "" {
			stems[n] = true
		}
	}
	unresolved := map[string][]string{} // stem -> referencing files
	for _, f := range c.Files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		for _, m := range wikilinkRe.FindAllStringSubmatch(string(raw), -1) {
			stem := wikilinkTarget(m[1])
			if stem == "" {
				continue
			}
			if !stems[stem] {
				refs := unresolved[stem]
				if len(refs) < 3 { // cap the listing; the count stays honest via the corpus
					unresolved[stem] = append(refs, f)
				} else {
					unresolved[stem] = refs
				}
			}
		}
	}
	keys := make([]string, 0, len(unresolved))
	for k := range unresolved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Finding
	for _, k := range keys {
		out = append(out, Finding{Kind: "wikilink",
			Detail: fmt.Sprintf("[[%s]] resolves to no file (e.g. from %s)", k, strings.Join(unresolved[k], ", "))})
	}
	return out, nil
}

// FrontmatterPresent reports files matching the glob whose frontmatter is
// missing a name:. exempt holds basename PREFIXES the caller declares are
// listings rather than entries — an index page is not an entry and would
// otherwise fire forever, and a gate that fires forever on known-good files
// trains wave-through.
//
// Nothing is exempt by default. The tool this was ported from hardcoded one
// filename prefix from its own corpus, which is a guess about someone else's
// layout; here the prefix is the caller's, stated per run, the same posture
// nova-self-talk's --skip took as the condition of its own promotion.
func FrontmatterPresent(fsys fs.FS, glob string, exempt []string) ([]Finding, error) {
	files, err := fs.Glob(fsys, glob)
	if err != nil {
		return nil, fmt.Errorf("bad glob %q: %w", glob, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("frontmatter glob %s matched nothing — a broken check, not a pass", glob)
	}
	sort.Strings(files)
	var out []Finding
	for _, f := range files {
		base := path.Base(f)
		skip := false
		for _, e := range exempt {
			if strings.HasPrefix(base, e) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		if name, _ := frontmatter(string(raw)); name == "" {
			out = append(out, Finding{Kind: "frontmatter",
				Detail: fmt.Sprintf("%s: no name: in frontmatter", f)})
		}
	}
	return out, nil
}
