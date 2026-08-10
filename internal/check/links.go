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

// Links walks dir for .md files (skipping .git) and verifies that every
// relative inline link target resolves to an existing file or directory
// inside the tree. It returns the number of markdown files seen, the number
// of relative links checked, and every broken link found.
func Links(dir string) (mdFiles, checked int, broken []BrokenLink, err error) {
	info, err := os.Stat(dir)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("dir: %w", err)
	}
	if !info.IsDir() {
		return 0, 0, nil, fmt.Errorf("dir %q is not a directory", dir)
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
		mdFiles++
		n, b := checkFileLinks(dir, path)
		checked += n
		broken = append(broken, b...)
		return nil
	})
	if err != nil {
		return 0, 0, nil, err
	}
	return mdFiles, checked, broken, nil
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
func checkFileLinks(root, mdPath string) (checked int, broken []BrokenLink) {
	relFile, relErr := filepath.Rel(root, mdPath)
	if relErr != nil {
		relFile = mdPath
	}
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return 0, []BrokenLink{{File: relFile, Reason: fmt.Sprintf("unreadable (%v)", readCause(err))}}
	}

	inFence := false
	fenceMarker := "" // the marker that opened the fence; only it can close it
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if inFence {
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			inFence, fenceMarker = true, "```"
			continue
		}
		if strings.HasPrefix(trimmed, "~~~") {
			inFence, fenceMarker = true, "~~~"
			continue
		}
		line = codeSpanRE.ReplaceAllString(line, "")
		for _, target := range extractLinkTargets(line) {
			resolved, skip, reason := resolveTarget(root, mdPath, target)
			if skip {
				continue
			}
			checked++
			if reason == "" {
				if _, statErr := os.Stat(resolved); statErr != nil {
					reason = "does not exist"
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
	var targets []string
	for i := 0; i < len(line); i++ {
		if line[i] != '[' {
			continue
		}
		textEnd := matchBrackets(line, i)
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

// matchBrackets returns the index of the ']' closing the '[' at open,
// tracking nesting, or -1 if it never closes on this line.
func matchBrackets(line string, open int) int {
	depth := 0
	for i := open; i < len(line); i++ {
		switch line[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
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
func resolveTarget(root, mdPath, target string) (resolved string, skip bool, reason string) {
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") || schemeRE.MatchString(target) {
		return "", true, ""
	}
	if i := strings.Index(target, "#"); i >= 0 {
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
	return resolved, false, ""
}
