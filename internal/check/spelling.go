package check

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/client9/misspell"
	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// SpellingFinding is one misspelling finding in an input file or text.
type SpellingFinding struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
}

// SpellingResult accounts for a spelling check pass.
type SpellingResult struct {
	FilesScanned int
	Findings     []SpellingFinding
	Corrected    int // number of files corrected if write mode was enabled
	Excluded     int
}

// SpellingOptions controls spelling check behavior.
type SpellingOptions struct {
	Ignore   []string // words to ignore/allowlist, or @file paths
	Write    bool     // rewrite files with corrections applied
	Markdown bool     // force markdown mode (blanking code blocks/spans)
	Exclude  []string // path prefixes to exclude
}

// SpellingChecker wraps misspell.Replacer with allowlist and code-stripping rules.
type SpellingChecker struct {
	rep    *misspell.Replacer
	ignore []string
}

// NewSpellingChecker compiles a misspell Replacer with DictAmerican and removes ignored words.
func NewSpellingChecker(ignore []string) *SpellingChecker {
	r := misspell.New()
	r.AddRuleList(misspell.DictAmerican)
	words, _ := ParseIgnoreSpec(ignore)
	if len(words) > 0 {
		r.RemoveRule(words)
	}
	r.Compile()
	return &SpellingChecker{rep: r, ignore: words}
}

// ParseAllowlist parses allowlist content: one word per line, ignoring blank lines and # comments.
func ParseAllowlist(content string) []string {
	var words []string
	for _, line := range strings.Split(content, "\n") {
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = line[:idx]
		}
		w := strings.TrimSpace(line)
		if w != "" {
			words = append(words, strings.ToLower(w))
		}
	}
	return words
}

// LoadAllowlistFile loads an allowlist from a file.
func LoadAllowlistFile(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("allowlist file: %w", err)
	}
	return ParseAllowlist(string(b)), nil
}

// ParseIgnoreSpec parses ignore specifications, which may contain comma-separated words
// or "@path" referencing a file with one word per line. Words are lowercased and trimmed.
func ParseIgnoreSpec(specs []string) ([]string, error) {
	var words []string
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		if strings.HasPrefix(spec, "@") {
			path := strings.TrimPrefix(spec, "@")
			fileWords, err := LoadAllowlistFile(path)
			if err != nil {
				return nil, err
			}
			words = append(words, fileWords...)
		} else {
			for _, w := range strings.FieldsFunc(spec, func(r rune) bool {
				return r == ',' || r == '\n' || r == '\r'
			}) {
				if trimmed := strings.ToLower(strings.TrimSpace(w)); trimmed != "" {
					words = append(words, trimmed)
				}
			}
		}
	}
	return words, nil
}

// IsMarkdown reports whether the filename has a markdown extension.
func IsMarkdown(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".md" || ext == ".markdown" || ext == ".mdown"
}

// StripCode blanks fenced code blocks (```...``` or ~~~...~~~) and inline code spans (`...`)
// with spaces, preserving all newlines, byte lengths, and column positions.
func StripCode(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	var fenceChar byte
	var fenceLen int
	inFence := false
	rest := text
	for len(rest) > 0 {
		line, nl := rest, false
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			line, rest, nl = rest[:i], rest[i+1:], true
		} else {
			rest = ""
		}
		trimmed := strings.TrimLeft(line, " \t")
		if m := fenceRE.FindStringSubmatch(trimmed); m != nil {
			delim := m[1]
			if !inFence {
				inFence, fenceChar, fenceLen = true, delim[0], len(delim)
				blankLine(&b, line)
			} else if delim[0] == fenceChar && len(delim) >= fenceLen && strings.TrimSpace(m[2]) == "" {
				inFence = false
				blankLine(&b, line)
			} else {
				blankLine(&b, line)
			}
		} else if inFence {
			blankLine(&b, line)
		} else {
			stripSpans(&b, line)
		}
		if nl {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func blankLine(b *strings.Builder, line string) {
	b.WriteString(strings.Repeat(" ", len(line)))
}

func stripSpans(b *strings.Builder, line string) {
	for i := 0; i < len(line); {
		if line[i] != '`' {
			b.WriteByte(line[i])
			i++
			continue
		}
		open := runLen(line, i)
		end := findClose(line, i+open, open)
		if end < 0 {
			// No matching closing backtick run on this line: treat as literal text.
			b.WriteString(line[i : i+open])
			i += open
			continue
		}
		b.WriteString(strings.Repeat(" ", end+open-i))
		i = end + open
	}
}

func runLen(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

func findClose(s string, i, n int) int {
	for ; i < len(s); i++ {
		if s[i] != '`' {
			continue
		}
		run := runLen(s, i)
		if run == n {
			return i
		}
		i += run - 1
	}
	return -1
}

// applyDiffs applies spelling replacements to originalText based on diff line and column positions.
func applyDiffs(originalText string, diffs []misspell.Diff) string {
	if len(diffs) == 0 {
		return originalText
	}
	// Group diffs by 1-based line number.
	lineDiffs := make(map[int][]misspell.Diff)
	for _, d := range diffs {
		lineDiffs[d.Line] = append(lineDiffs[d.Line], d)
	}

	lines := strings.SplitAfter(originalText, "\n")
	var b strings.Builder
	b.Grow(len(originalText))

	for i, line := range lines {
		lineNum := i + 1
		ds, ok := lineDiffs[lineNum]
		if !ok || len(ds) == 0 {
			b.WriteString(line)
			continue
		}
		// Sort diffs in descending order of Column so earlier columns are not shifted.
		sort.Slice(ds, func(p, q int) bool {
			return ds[p].Column > ds[q].Column
		})
		cur := line
		for _, d := range ds {
			col := d.Column
			origLen := len(d.Original)
			if col >= 0 && col+origLen <= len(cur) && cur[col:col+origLen] == d.Original {
				cur = cur[:col] + d.Corrected + cur[col+origLen:]
			}
		}
		b.WriteString(cur)
	}
	return b.String()
}

// CheckText checks text content for misspellings.
// If isMarkdown is true, fenced code blocks and inline code spans are blanked before checking.
// It returns the findings and the corrected text.
func (c *SpellingChecker) CheckText(filename, text string, isMarkdown bool) ([]SpellingFinding, string) {
	checkInput := text
	if isMarkdown {
		checkInput = StripCode(text)
	}
	_, diffs := c.rep.Replace(checkInput)
	if len(diffs) == 0 {
		return nil, text
	}
	findings := make([]SpellingFinding, 0, len(diffs))
	for _, d := range diffs {
		findings = append(findings, SpellingFinding{
			File:        filename,
			Line:        d.Line,
			Column:      d.Column,
			Original:    d.Original,
			Replacement: d.Corrected,
		})
	}
	updated := applyDiffs(text, diffs)
	return findings, updated
}

// CheckSpellingText checks in-memory text directly.
func CheckSpellingText(filename, text string, opts SpellingOptions) ([]SpellingFinding, string, error) {
	checker := NewSpellingChecker(opts.Ignore)
	isMD := opts.Markdown || IsMarkdown(filename)
	findings, updated := checker.CheckText(filename, text, isMD)
	return findings, updated, nil
}

// CheckSpellingFile checks a single file for misspellings, optionally rewriting it if opts.Write is true.
func CheckSpellingFile(path string, opts SpellingOptions) ([]SpellingFinding, error) {
	checker := NewSpellingChecker(opts.Ignore)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("file %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("file %q is a directory", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", path, err)
	}
	text := string(data)
	isMD := opts.Markdown || IsMarkdown(path)
	findings, updated := checker.CheckText(path, text, isMD)
	if opts.Write && updated != text {
		writeErr := atomicfile.WriteFile(path, []byte(updated), info.Mode().Perm())
		if writeErr != nil {
			if writeErr = os.WriteFile(path, []byte(updated), info.Mode().Perm()); writeErr != nil {
				return findings, fmt.Errorf("writing %q: %w", path, writeErr)
			}
		}
	}
	return findings, nil
}

// CheckSpellingDir checks all markdown files in dir (and its subdirectories), skipping .git
// and paths matching opts.Exclude.
func CheckSpellingDir(dir string, opts SpellingOptions) (res SpellingResult, err error) {
	root, statErr := filepath.EvalSymlinks(dir)
	if statErr != nil {
		return res, fmt.Errorf("dir %q: %w", dir, statErr)
	}
	info, statErr := os.Stat(root)
	if statErr != nil {
		return res, fmt.Errorf("dir %q: %w", dir, statErr)
	}
	if !info.IsDir() {
		return res, fmt.Errorf("dir %q is not a directory", dir)
	}
	dir = root

	checker := NewSpellingChecker(opts.Ignore)
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
		if !IsMarkdown(d.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if underExclude(rel, opts.Exclude) {
			res.Excluded++
			return nil
		}
		res.FilesScanned++
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %q: %w", path, readErr)
		}
		text := string(data)
		findings, updated := checker.CheckText(rel, text, true)
		res.Findings = append(res.Findings, findings...)
		if opts.Write && updated != text {
			fi, _ := d.Info()
			perm := os.FileMode(0o644)
			if fi != nil {
				perm = fi.Mode().Perm()
			}
			if writeErr := atomicfile.WriteFile(path, []byte(updated), perm); writeErr != nil {
				if writeErr = os.WriteFile(path, []byte(updated), perm); writeErr != nil {
					return fmt.Errorf("writing %q: %w", path, writeErr)
				}
			}
			res.Corrected++
		}
		return nil
	})
	return res, err
}

// CheckSpellingFiles checks the listed files relative to dir (or as given if dir is empty).
func CheckSpellingFiles(dir string, files []string, opts SpellingOptions) (res SpellingResult, err error) {
	if dir != "" {
		root, statErr := filepath.EvalSymlinks(dir)
		if statErr != nil {
			return res, fmt.Errorf("dir %q: %w", dir, statErr)
		}
		info, statErr := os.Stat(root)
		if statErr != nil {
			return res, fmt.Errorf("dir %q: %w", dir, statErr)
		}
		if !info.IsDir() {
			return res, fmt.Errorf("dir %q is not a directory", dir)
		}
		dir = root
	}

	checker := NewSpellingChecker(opts.Ignore)
	for _, f := range files {
		targetPath := f
		relPath := f
		if dir != "" && !filepath.IsAbs(f) {
			targetPath = filepath.Join(dir, filepath.FromSlash(f))
			relPath = filepath.ToSlash(f)
		} else {
			relPath = filepath.ToSlash(f)
		}

		if underExclude(relPath, opts.Exclude) {
			res.Excluded++
			continue
		}

		info, statErr := os.Stat(targetPath)
		if statErr != nil {
			return res, fmt.Errorf("file %q: %w", f, statErr)
		}
		if info.IsDir() {
			return res, fmt.Errorf("file %q is a directory", f)
		}

		res.FilesScanned++
		data, readErr := os.ReadFile(targetPath)
		if readErr != nil {
			return res, fmt.Errorf("reading %q: %w", f, readErr)
		}

		text := string(data)
		isMD := opts.Markdown || IsMarkdown(targetPath)
		findings, updated := checker.CheckText(relPath, text, isMD)
		res.Findings = append(res.Findings, findings...)
		if opts.Write && updated != text {
			perm := info.Mode().Perm()
			if writeErr := atomicfile.WriteFile(targetPath, []byte(updated), perm); writeErr != nil {
				if writeErr = os.WriteFile(targetPath, []byte(updated), perm); writeErr != nil {
					return res, fmt.Errorf("writing %q: %w", f, writeErr)
				}
			}
			res.Corrected++
		}
	}
	return res, nil
}

// CheckSpelling checks paths or glob patterns, expanding patterns as needed.
func CheckSpelling(targets []string, opts SpellingOptions) (res SpellingResult, err error) {
	var fileList []string
	seen := make(map[string]bool)

	for _, target := range targets {
		if strings.ContainsAny(target, "*?[") {
			matches, globErr := filepath.Glob(target)
			if globErr != nil {
				return res, fmt.Errorf("pattern %q: %w", target, globErr)
			}
			for _, m := range matches {
				fi, statErr := os.Stat(m)
				if statErr == nil && !fi.IsDir() && !seen[m] {
					seen[m] = true
					fileList = append(fileList, m)
				}
			}
			continue
		}

		fi, statErr := os.Stat(target)
		if statErr != nil {
			return res, fmt.Errorf("path %q: %w", target, statErr)
		}
		if fi.IsDir() {
			subRes, subErr := CheckSpellingDir(target, opts)
			if subErr != nil {
				return res, subErr
			}
			res.FilesScanned += subRes.FilesScanned
			res.Findings = append(res.Findings, subRes.Findings...)
			res.Corrected += subRes.Corrected
			res.Excluded += subRes.Excluded
			continue
		}
		if !seen[target] {
			seen[target] = true
			fileList = append(fileList, target)
		}
	}

	if len(fileList) > 0 {
		subRes, subErr := CheckSpellingFiles("", fileList, opts)
		if subErr != nil {
			return res, subErr
		}
		res.FilesScanned += subRes.FilesScanned
		res.Findings = append(res.Findings, subRes.Findings...)
		res.Corrected += subRes.Corrected
		res.Excluded += subRes.Excluded
	}

	return res, nil
}
