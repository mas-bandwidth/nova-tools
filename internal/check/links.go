package check

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/readregular"
)

// BrokenLink is one relative markdown link that does not resolve — or, when
// Line is 0 and Target is empty, one whole .md file that could not be read.
type BrokenLink struct {
	File   string // path of the containing .md file, relative to the scanned dir
	Line   int    // 1-based line number; 0 when the whole file is the finding
	Target string // the link target as written; empty when the whole file is the finding
	Reason string
}

var (
	// Inline code spans, stripped before link extraction.
	codeSpanRE = regexp.MustCompile("`[^`]*`")
	// A URL scheme prefix (https:, mailto:, ...): external, not checked.
	schemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
)

// LinksResult is the full accounting of a links walk: the markdown files
// scanned, the relative links checked, the whole files skipped under an
// explicit --exclude prefix, and every broken link found.
type LinksResult struct {
	MDFiles  int
	Checked  int
	Excluded int
	Broken   []BrokenLink
}

// resolveRoot resolves a --dir argument to a real directory before a walk.
//
// Resolve the root before walking. os.Stat FOLLOWS a symlink, so a --dir
// naming a link to the repo passed the directory check and then handed
// WalkDir a root it saw as a single non-directory entry — a clean pass
// over a tree never opened. On this platform /var is such a link.
func resolveRoot(dir string) (string, error) {
	root, statErr := filepath.EvalSymlinks(dir)
	if statErr != nil {
		return "", fmt.Errorf("dir %q: %w", dir, statErr)
	}
	info, statErr := os.Stat(root)
	if statErr != nil {
		return "", fmt.Errorf("dir %q: %w", dir, statErr)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("dir %q is not a directory", dir)
	}
	return root, nil
}

// LinksExcluding walks dir for .md files (skipping .git) and verifies that
// every relative inline link target resolves to an existing file or directory
// inside the tree, with an explicit set of path prefixes to leave unscanned:
// a file under an excluded prefix is not opened, and a link that resolves
// into an excluded prefix is skipped rather than checked or reported. The
// Excluded count is the number of .md files under those prefixes.
func LinksExcluding(dir string, exclude []string) (res LinksResult, err error) {
	dir, err = resolveRoot(dir)
	if err != nil {
		return res, err
	}

	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		if underExclude(rel, exclude) {
			res.Excluded++
			return nil
		}
		res.MDFiles++
		if !d.Type().IsRegular() {
			res.Broken = append(res.Broken, BrokenLink{
				File:   filepath.ToSlash(rel),
				Reason: "not a regular file",
			})
			return nil
		}
		n, b := checkFileLinks(dir, path, exclude)
		res.Checked += n
		res.Broken = append(res.Broken, b...)
		return nil
	})
	if err != nil {
		return res, err
	}
	return res, nil
}

// LinksFiles is the single-/few-file form of the walk: it checks exactly the
// listed markdown files and nothing else, so a two-file review does not
// expand to the whole tree. dir is still the resolution root — root-relative
// targets and the "escapes the tree" judgement resolve against it — and the
// reported paths stay repo-relative, exactly as the full walk reports them. A
// relative path is joined to dir; an absolute one is used as-is.
func LinksFiles(dir string, files []string, exclude []string) (res LinksResult, err error) {
	dir, err = resolveRoot(dir)
	if err != nil {
		return res, err
	}

	for _, f := range files {
		mdPath := f
		if !filepath.IsAbs(mdPath) {
			mdPath = filepath.Join(dir, filepath.FromSlash(f))
		}
		if !strings.EqualFold(filepath.Ext(mdPath), ".md") {
			return res, fmt.Errorf("file %q is not a markdown file", f)
		}
		res.MDFiles++
		n, b := checkFileLinks(dir, mdPath, exclude)
		res.Checked += n
		res.Broken = append(res.Broken, b...)
	}
	return res, nil
}

// underExclude reports whether a tree-relative path (forward-slashed) is at or
// below any of the given exclude prefixes. A prefix is matched as a path:
// "testdata" excludes "testdata" and everything under it, never a sibling like
// "testdata-set".
func underExclude(rel string, exclude []string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	rel = strings.TrimPrefix(rel, "./")
	rel = strings.TrimPrefix(rel, "/")
	for _, ex := range exclude {
		ex = filepath.ToSlash(filepath.Clean(ex))
		ex = strings.TrimPrefix(ex, "./")
		ex = strings.TrimPrefix(ex, "/")
		ex = strings.TrimSuffix(ex, "/")
		if ex == "" || ex == "." {
			continue
		}
		if rel == ex || strings.HasPrefix(rel, ex+"/") {
			return true
		}
	}
	return false
}

// checkFileLinks extracts and resolves the relative links in one markdown
// file. Fenced code blocks and inline code spans are stripped first so that
// examples do not count as links.
//
// A file that cannot be read (permissions, a dangling symlink) comes back as
// one whole-file BrokenLink — Line 0, Target empty, reason "unreadable (…)" —
// never as an error. This is the same posture attest takes with a manifested
// file that exists but cannot be read: a NAMED FAILURE, not a refusal. The
// walk continues, so one unreadable file cannot discard the findings from the
// rest of the tree; before this, it converted the whole run to exit 2 and
// threw the accumulated broken links away.
func checkFileLinks(root, mdPath string, exclude []string) (checked int, broken []BrokenLink) {
	relFile, relErr := filepath.Rel(root, mdPath)
	if relErr != nil {
		relFile = mdPath
	}
	// Reported paths are forward-slashed on every platform, as nocode already
	// does. A finding is something a reader copies into a shell or an issue, and
	// a backslashed spelling is not what any other output here uses. On the
	// Rel-error branch above this is an ABSOLUTE path rather than a repo-relative
	// one, so it is forward-slashed but not repo-relative; that branch is
	// unreachable in practice and the value is print-only, never re-joined.
	relFile = filepath.ToSlash(relFile)
	data, err := readregular.Read(mdPath, readregular.DefaultMax)
	if err != nil {
		return 0, []BrokenLink{{File: relFile, Reason: fmt.Sprintf("unreadable (%v)", readCause(err))}}
	}

	// Resolve the root the same way a target is resolved, before the prefix
	// test. On macOS the temp root is under /tmp or /var, each a symlink
	// (/tmp -> /private/tmp, /var -> /private/var), so a resolved target never
	// has the unresolved root as a prefix. A directory symlink that stays
	// inside the tree must not look like an escape. If the root cannot be
	// resolved, the unresolved root is kept.
	realRoot, rootErr := filepath.EvalSymlinks(root)
	if rootErr != nil {
		realRoot = root
	}

	// Fences use fenceRE, the same CommonMark rule the ledger parser uses: the
	// opening run records its character and length, and only a run of the SAME
	// character, at least as long and carrying nothing after it, closes it.
	var fenceChar byte
	var fenceLen int
	inFence := false
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && (trimmed[0] == '`' || trimmed[0] == '~') {
			if m := fenceRE.FindStringSubmatch(trimmed); m != nil {
				delim := m[1]
				if !inFence {
					inFence, fenceChar, fenceLen = true, delim[0], len(delim)
				} else if delim[0] == fenceChar && len(delim) >= fenceLen && strings.TrimSpace(m[2]) == "" {
					inFence = false
				}
				continue
			}
		}
		if inFence {
			continue
		}
		if strings.IndexByte(line, '`') >= 0 {
			line = codeSpanRE.ReplaceAllString(line, "")
		}
		if strings.IndexByte(line, '[') < 0 {
			continue
		}
		for _, target := range extractLinkTargets(line) {
			resolved, skip, fragment := resolveTarget(root, mdPath, target, exclude)
			if skip {
				continue
			}
			var reason string
			checked++
			if fragment != "" && strings.EqualFold(filepath.Ext(resolved), ".md") {
				if !fragmentExists(resolved, fragment) {
					reason = "missing anchor"
				}
			}
			if reason == "" {
				// The lexical check in resolveTarget rejects targets that leave
				// root with "..", but an intermediate directory symlink could
				// still escape the tree. Compare the resolved target to the
				// resolved root (as attest does in attest.go:120-123). A
				// non-existent target keeps "does not exist".
				realPath, evalErr := filepath.EvalSymlinks(resolved)
				if evalErr != nil {
					if errors.Is(evalErr, fs.ErrNotExist) || os.IsNotExist(evalErr) {
						reason = "does not exist"
					}
				} else if realPath != realRoot && !strings.HasPrefix(realPath, realRoot+string(filepath.Separator)) {
					reason = "escapes the tree through a symlink; cannot survive the repo travelling alone"
				}
				if reason == "" {
					if _, statErr := os.Stat(resolved); statErr != nil {
						reason = "does not exist"
					}
				}
			}
			if reason != "" {
				broken = append(broken, BrokenLink{
					File:   relFile,
					Line:   i + 1,
					Target: target,
					Reason: reason,
				})
			}
		}
	}
	return checked, broken
}

// readCause unwraps the OS-level cause ("permission denied", "no such file
// or directory") from a read error, so the whole-file FAIL line stays one
// short clause: the file's path is already the line's subject and the full
// *fs.PathError would repeat it.
func readCause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// extractLinkTargets returns every inline link or image destination in one
// line of markdown (code spans already stripped). It is a small scanner, not
// a CommonMark parser: it handles bracket nesting in link text (the badge
// pattern [![alt](img)](target) — both targets are found), angle-bracket
// destinations (<my file.md>), and titles in "double", 'single', or (paren)
// form. What it does not handle is listed in SPEC.md as deliberately
// not checked.
func extractLinkTargets(line string) []string {
	if strings.IndexByte(line, '[') < 0 {
		return nil
	}
	bracketMatches := matchingBracketCloses(line)
	var targets []string
	for i := 0; i < len(line); i++ {
		if line[i] != '[' {
			continue
		}
		textEnd := bracketMatches[i] - 1
		if textEnd < 0 || textEnd+1 >= len(line) || line[textEnd+1] != '(' {
			continue
		}
		if dest, ok := parseDestination(line[textEnd+1:]); ok {
			targets = append(targets, dest)
		}
		// Do not jump past the link: its text may hold a nested image or
		// link (a badge), which this loop will find at the inner '['.
	}
	return targets
}

// matchingBracketCloses records each '[' match in one pass, as links specifies
// for its nested inline-link text (docs/SPEC.md:422-484). A stored index is
// one-based so zero means that the '[' is unclosed; backslashes do not change
// the scanner's existing bracket behavior.
func matchingBracketCloses(line string) []int {
	matches := make([]int, len(line))
	stack := make([]int, 0)
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '[':
			stack = append(stack, i)
		case ']':
			if n := len(stack); n > 0 {
				open := stack[n-1]
				stack = stack[:n-1]
				matches[open] = i + 1
			}
		}
	}
	return matches
}

// parseDestination parses a parenthesized link destination at the start of
// s: (dest), (<dest>), or (dest "title") with the title in any of the three
// quote forms. ok is false when s is not well-formed — then it was never a
// link, not a broken one.
func parseDestination(s string) (dest string, ok bool) {
	i := skipSpaces(s, 1) // s[0] is '('
	if i < len(s) && s[i] == '<' {
		end := strings.IndexByte(s[i+1:], '>')
		if end < 0 {
			return "", false
		}
		dest = s[i+1 : i+1+end]
		i += 1 + end + 1
	} else {
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != ')' {
			i++
		}
		dest = s[start:i]
	}
	i = skipSpaces(s, i)
	if i >= len(s) {
		return "", false
	}
	if s[i] != ')' {
		// Optional title: "double", 'single', or (parenthesized).
		var closer byte
		switch s[i] {
		case '"':
			closer = '"'
		case '\'':
			closer = '\''
		case '(':
			closer = ')'
		default:
			return "", false
		}
		end := strings.IndexByte(s[i+1:], closer)
		if end < 0 {
			return "", false
		}
		i = skipSpaces(s, i+1+end+1)
		if i >= len(s) || s[i] != ')' {
			return "", false
		}
	}
	return dest, true
}

func skipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// resolveTarget classifies a link target. skip means the target is out of
// scope (external, fragment-only). A non-empty reason means it is broken
// before ever touching the disk (it escapes the tree).
func resolveTarget(root, mdPath, target string, exclude []string) (resolved string, skip bool, reason string) {
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") {
		return "", true, ""
	}
	if strings.IndexByte(target, ':') >= 0 && schemeRE.MatchString(target) {
		return "", true, ""
	}
	var fragment string
	if i := strings.Index(target, "#"); i >= 0 {
		fragment = target[i+1:]
		target = target[:i]
	}
	if target == "" {
		return "", true, ""
	}
	if unescaped, err := url.PathUnescape(target); err == nil {
		target = unescaped
	}
	if strings.HasPrefix(target, "/") {
		// Repo-root-relative, the GitHub convention.
		resolved = filepath.Join(root, filepath.FromSlash(target))
	} else {
		resolved = filepath.Join(filepath.Dir(mdPath), filepath.FromSlash(target))
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, "escapes the tree; cannot survive the repo travelling alone"
	}
	if rel != "." && underExclude(rel, exclude) {
		return "", true, ""
	}
	return resolved, false, fragment
}

// fragmentExists reports whether the markdown file at path contains a heading
// that anchors to the given fragment. GitHub's anchor rule is applied:
// lower-case, spaces to dashes, punctuation dropped.
func fragmentExists(path, fragment string) bool {
	data, err := readregular.Read(path, readregular.DefaultMax)
	if err != nil {
		return false
	}
	fragment = strings.ToLower(fragment)
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		heading := strings.TrimLeft(trimmed, "#")
		heading = strings.TrimSpace(heading)
		anchor := anchorFromHeading(heading)
		if anchor == fragment {
			return true
		}
	}
	return false
}

// anchorFromHeading converts a markdown heading to a GitHub-style anchor.
// GitHub's rule: lower-case, spaces to dashes, punctuation removed.
func anchorFromHeading(heading string) string {
	heading = strings.ToLower(heading)
	var b strings.Builder
	for _, r := range heading {
		if r == ' ' {
			b.WriteRune('-')
		} else if isAlphaNum(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
