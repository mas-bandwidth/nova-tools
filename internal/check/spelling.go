package check

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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
	Dir      string   // root directory for relative paths and exclusions
}

// SpellingChecker wraps misspell.Replacer with allowlist and code-stripping rules.
type SpellingChecker struct {
	rep    *misspell.Replacer
	ignore []string
}

var (
	defaultOnce    sync.Once
	defaultChecker *SpellingChecker
	checkerCacheMu sync.RWMutex
	checkerCache   = make(map[string]*SpellingChecker)
)

func defaultSpellingChecker() *SpellingChecker {
	defaultOnce.Do(func() {
		r := misspell.New()
		r.AddRuleList(misspell.DictAmerican)
		r.Compile()
		defaultChecker = &SpellingChecker{rep: r}
	})
	return defaultChecker
}

// NewSpellingChecker compiles a misspell Replacer with DictAmerican and removes ignored words.
// It caches compiled checkers so repetitive compilation across tests and files is avoided.
func NewSpellingChecker(ignore []string) *SpellingChecker {
	words, err := ParseIgnoreSpec(ignore)
	if err != nil || len(words) == 0 {
		return defaultSpellingChecker()
	}

	sorted := make([]string, len(words))
	copy(sorted, words)
	sort.Strings(sorted)

	dedup := sorted[:0]
	for i, w := range sorted {
		if i == 0 || w != sorted[i-1] {
			dedup = append(dedup, w)
		}
	}
	cacheKey := strings.Join(dedup, ",")

	checkerCacheMu.RLock()
	c, ok := checkerCache[cacheKey]
	checkerCacheMu.RUnlock()
	if ok {
		return c
	}

	checkerCacheMu.Lock()
	defer checkerCacheMu.Unlock()
	if c, ok = checkerCache[cacheKey]; ok {
		return c
	}

	r := misspell.New()
	r.AddRuleList(misspell.DictAmerican)
	r.RemoveRule(dedup)
	r.Compile()
	c = &SpellingChecker{rep: r, ignore: dedup}
	checkerCache[cacheKey] = c
	return c
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

func stripBlockquotePrefix(s string) string {
	s = strings.TrimLeft(s, " \t")
	for strings.HasPrefix(s, ">") {
		s = strings.TrimPrefix(s, ">")
		s = strings.TrimLeft(s, " \t")
	}
	return strings.TrimRight(s, "\r")
}

func isBlankLine(s string) bool {
	return strings.TrimSpace(stripBlockquotePrefix(s)) == ""
}

type lineRange struct {
	start int
	end   int // index of '\n' or len(text)
}

func findLine(lines []lineRange, pos int) int {
	idx := sort.Search(len(lines), func(n int) bool {
		return lines[n].end >= pos
	})
	if idx < len(lines) && lines[idx].start <= pos {
		return idx
	}
	return -1
}

type containerKind int

const (
	containerBlockquote containerKind = iota
	containerList
)

type containerItem struct {
	kind   containerKind
	indent int // required indent for list item continuation
}

func matchListItemMarker(s string) (markerLen int, ok bool) {
	if len(s) == 0 {
		return 0, false
	}
	// Bullet list: -, +, *
	if s[0] == '-' || s[0] == '+' || s[0] == '*' {
		if len(s) == 1 || s[1] == ' ' || s[1] == '\t' || s[1] == '\r' || s[1] == '\n' {
			return 1, true
		}
		return 0, false
	}
	// Ordered list: 1-9 digits followed by . or )
	if s[0] >= '0' && s[0] <= '9' {
		d := 0
		for d < len(s) && d < 9 && s[d] >= '0' && s[d] <= '9' {
			d++
		}
		if d > 0 && d < len(s) && (s[d] == '.' || s[d] == ')') {
			after := d + 1
			if after == len(s) || s[after] == ' ' || s[after] == '\t' || s[after] == '\r' || s[after] == '\n' {
				return after, true
			}
		}
	}
	return 0, false
}

// StripCode blanks fenced code blocks (```...``` or ~~~...~~~) and inline code spans (`...`)
// with spaces, preserving all newlines, byte lengths, and column positions.
// CommonMark 0.31.2 container boundaries (block quotes, list items) are tracked so fences
// inside containers are recognized and close when their container ends.
// Inline code spans can span multiple lines without resetting at newline (CommonMark 0.31.2).
// Code spans cannot cross blank lines or fenced code block boundaries.
// Backtick characters escaped by an odd number of preceding backslashes outside a code span
// do not start or end a code span.
func StripCode(text string) string {
	if text == "" {
		return ""
	}
	out := []byte(text)

	var lines []lineRange
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == '\n' {
			lines = append(lines, lineRange{start: start, end: i})
			start = i + 1
		}
	}

	inFence := false
	var fenceChar byte
	var fenceLen int
	var fenceDepth int
	var openContainers []containerItem
	fencedLine := make([]bool, len(lines))

	for idx, lr := range lines {
		lineStr := text[lr.start:lr.end]
		lineStr = strings.TrimRight(lineStr, "\r")
		pos := 0

		// Step 1: Match existing open containers against the current line.
		matchedDepth := 0
		for k, c := range openContainers {
			if c.kind == containerBlockquote {
				// Blockquote requires 0-3 leading spaces, then '>'.
				sp := 0
				for sp <= 3 && pos+sp < len(lineStr) && lineStr[pos+sp] == ' ' {
					sp++
				}
				if sp <= 3 && pos+sp < len(lineStr) && lineStr[pos+sp] == '>' {
					pos += sp + 1
					if pos < len(lineStr) && (lineStr[pos] == ' ' || lineStr[pos] == '\t') {
						pos++
					}
					matchedDepth++
				} else {
					break
				}
			} else if c.kind == containerList {
				// Blank line inside a fence within a list item matches the container.
				if strings.TrimSpace(lineStr[pos:]) == "" {
					if inFence && fenceDepth > k {
						matchedDepth++
						continue
					}
					break
				}
				col := 0
				adv := 0
				for pos+adv < len(lineStr) {
					ch := lineStr[pos+adv]
					if ch == ' ' {
						col++
						adv++
					} else if ch == '\t' {
						col += 4 - (col % 4)
						adv++
					} else {
						break
					}
					if col >= c.indent {
						break
					}
				}
				if col >= c.indent {
					pos += adv
					matchedDepth++
				} else {
					break
				}
			}
		}

		// If any containers failed to match, close them.
		if matchedDepth < len(openContainers) {
			if inFence && fenceDepth > matchedDepth {
				inFence = false
			}
			openContainers = openContainers[:matchedDepth]
		}

		if inFence {
			// Line is within the container holding the fence. Check for closing fence.
			rem := lineStr[pos:]
			sp := 0
			for sp <= 3 && sp < len(rem) && rem[sp] == ' ' {
				sp++
			}
			isClosing := false
			if sp <= 3 && sp < len(rem) {
				fenceStr := rem[sp:]
				cnt := 0
				for cnt < len(fenceStr) && fenceStr[cnt] == fenceChar {
					cnt++
				}
				if cnt >= fenceLen && strings.TrimSpace(fenceStr[cnt:]) == "" {
					isClosing = true
				}
			}
			if isClosing {
				inFence = false
				fencedLine[idx] = true
			} else {
				fencedLine[idx] = true
			}
		} else {
			// Not currently in a fence: try opening new containers on this line.
			for pos < len(lineStr) {
				// Try opening blockquote:
				sp := 0
				for sp <= 3 && pos+sp < len(lineStr) && lineStr[pos+sp] == ' ' {
					sp++
				}
				if sp <= 3 && pos+sp < len(lineStr) && lineStr[pos+sp] == '>' {
					pos += sp + 1
					if pos < len(lineStr) && (lineStr[pos] == ' ' || lineStr[pos] == '\t') {
						pos++
					}
					openContainers = append(openContainers, containerItem{kind: containerBlockquote})
					continue
				}

				// Try opening list item:
				sp = 0
				for sp <= 3 && pos+sp < len(lineStr) && lineStr[pos+sp] == ' ' {
					sp++
				}
				if sp <= 3 && pos+sp < len(lineStr) {
					if markerLen, ok := matchListItemMarker(lineStr[pos+sp:]); ok {
						afterMarker := pos + sp + markerLen
						postSp := 0
						for afterMarker+postSp < len(lineStr) && (lineStr[afterMarker+postSp] == ' ' || lineStr[afterMarker+postSp] == '\t') {
							postSp++
						}
						indent := sp + markerLen + postSp
						if afterMarker+postSp == len(lineStr) {
							indent = sp + markerLen + 1
						}
						pos = afterMarker + postSp
						openContainers = append(openContainers, containerItem{kind: containerList, indent: indent})
						continue
					}
				}

				break
			}

			// Check if remainder of line opens a fenced code block.
			rem := lineStr[pos:]
			sp := 0
			for sp <= 3 && sp < len(rem) && rem[sp] == ' ' {
				sp++
			}
			if sp <= 3 && sp < len(rem) {
				fenceStr := rem[sp:]
				if m := fenceRE.FindStringSubmatch(fenceStr); m != nil {
					delim := m[1]
					info := m[2]
					if delim[0] != '`' || !strings.ContainsRune(info, '`') {
						inFence = true
						fenceChar = delim[0]
						fenceLen = len(delim)
						fenceDepth = len(openContainers)
						fencedLine[idx] = true
					}
				}
			}
		}
	}

	// Blank fenced code block lines in full, preserving newlines.
	for idx, lr := range lines {
		if fencedLine[idx] {
			for k := lr.start; k < lr.end; k++ {
				if out[k] != '\r' {
					out[k] = ' '
				}
			}
		}
	}

	// Step 2: Blank inline code spans in non-fenced lines.
	for i := 0; i < len(text); {
		lineIdx := findLine(lines, i)
		if lineIdx >= 0 && fencedLine[lineIdx] {
			i = lines[lineIdx].end + 1
			continue
		}

		if text[i] != '`' {
			i++
			continue
		}

		// Count preceding backslashes to check if this backtick is escaped.
		numBackslashes := 0
		for k := i - 1; k >= 0 && text[k] == '\\'; k-- {
			numBackslashes++
		}
		if numBackslashes%2 != 0 {
			i++
			continue
		}

		// Count opening backtick run length.
		openLen := 0
		for i+openLen < len(text) && text[i+openLen] == '`' {
			openLen++
		}

		// Search forward for a closing backtick run of exactly openLen length.
		matchEnd := -1
		curLine := lineIdx
		for j := i + openLen; j < len(text); {
			if text[j] == '\n' {
				nextLine := curLine + 1
				if nextLine < len(lines) {
					if fencedLine[nextLine] {
						break
					}
					nxtStr := text[lines[nextLine].start:lines[nextLine].end]
					if isBlankLine(nxtStr) {
						break
					}
					curLine = nextLine
				}
				j++
				continue
			}

			if text[j] == '`' {
				runLen := 0
				for j+runLen < len(text) && text[j+runLen] == '`' {
					runLen++
				}
				if runLen == openLen {
					matchEnd = j + runLen
					break
				}
				j += runLen
				continue
			}
			j++
		}

		if matchEnd >= 0 {
			for k := i; k < matchEnd; k++ {
				if out[k] != '\n' && out[k] != '\r' {
					out[k] = ' '
				}
			}
			i = matchEnd
		} else {
			i += openLen
		}
	}

	return string(out)
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
		cleanPath := filepath.Clean(path)
		if writeErr := atomicfile.WriteFile(cleanPath, []byte(updated), info.Mode().Perm()); writeErr != nil {
			return findings, fmt.Errorf("writing %q: %w", path, writeErr)
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

	absDir, _ := filepath.Abs(dir)
	absDir = filepath.Clean(absDir)

	absExclRoot := dir
	if opts.Dir != "" {
		excl := opts.Dir
		if abs, err := filepath.Abs(excl); err == nil {
			excl = filepath.Clean(abs)
		}
		if resolved, err := filepath.EvalSymlinks(excl); err == nil {
			absExclRoot = resolved
		} else {
			absExclRoot = excl
		}
	}

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
		rel, relErr := filepath.Rel(absExclRoot, path)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			rel, _ = filepath.Rel(absDir, path)
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
			cleanPath := filepath.Clean(path)
			if writeErr := atomicfile.WriteFile(cleanPath, []byte(updated), perm); writeErr != nil {
				return fmt.Errorf("writing %q: %w", path, writeErr)
			}
			res.Corrected++
		}
		return nil
	})
	return res, err
}

// CheckSpellingFiles checks the listed files relative to dir (or opts.Dir, or cwd if both empty).
func CheckSpellingFiles(dir string, files []string, opts SpellingOptions) (res SpellingResult, err error) {
	root := dir
	if root == "" {
		root = opts.Dir
	}
	var cleanRoot string
	var rootResolved string
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = filepath.Clean(abs)
		}
		resolved, statErr := filepath.EvalSymlinks(root)
		if statErr != nil {
			return res, fmt.Errorf("dir %q: %w", root, statErr)
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			return res, fmt.Errorf("dir %q: %w", root, statErr)
		}
		if !info.IsDir() {
			return res, fmt.Errorf("dir %q is not a directory", root)
		}
		cleanRoot = resolved
		rootResolved = resolved
	} else if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		if abs, err := filepath.Abs(cwd); err == nil {
			cwd = filepath.Clean(abs)
		}
		cleanRoot = cwd
		if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
			cleanRoot = resolved
			rootResolved = resolved
		} else {
			rootResolved = cwd
		}
	}

	checker := NewSpellingChecker(opts.Ignore)
	for _, f := range files {
		targetPath := f
		var relPath string

		if !filepath.IsAbs(f) {
			relPath = filepath.ToSlash(filepath.Clean(f))
			relPath = strings.TrimPrefix(relPath, "./")
			if cleanRoot != "" {
				joined := filepath.Join(cleanRoot, filepath.FromSlash(f))
				if _, err := os.Stat(joined); err != nil {
					if _, err2 := os.Stat(f); err2 == nil {
						if absF, absErr := filepath.Abs(f); absErr == nil {
							targetPath = absF
							if rel, relErr := filepath.Rel(cleanRoot, targetPath); relErr == nil && !strings.HasPrefix(rel, "..") {
								relPath = filepath.ToSlash(rel)
							}
						} else {
							targetPath = joined
						}
					} else {
						targetPath = joined
					}
				} else {
					targetPath = joined
				}
			} else {
				targetPath = filepath.Clean(f)
			}
		} else {
			targetPath = filepath.Clean(f)
			if cleanRoot != "" {
				if rel, relErr := filepath.Rel(cleanRoot, targetPath); relErr == nil && !strings.HasPrefix(rel, "..") {
					relPath = filepath.ToSlash(rel)
				} else if rootResolved != "" {
					targetResolved, _ := filepath.EvalSymlinks(targetPath)
					if rel2, relErr2 := filepath.Rel(rootResolved, targetResolved); relErr2 == nil && !strings.HasPrefix(rel2, "..") {
						relPath = filepath.ToSlash(rel2)
					} else {
						relPath = filepath.ToSlash(targetPath)
					}
				} else {
					relPath = filepath.ToSlash(targetPath)
				}
			} else {
				relPath = filepath.ToSlash(targetPath)
			}
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
			cleanTarget := filepath.Clean(targetPath)
			if writeErr := atomicfile.WriteFile(cleanTarget, []byte(updated), perm); writeErr != nil {
				return res, fmt.Errorf("writing %q: %w", f, writeErr)
			}
			res.Corrected++
		}
	}
	return res, nil
}

// CheckSpelling checks paths or glob patterns, expanding patterns as needed.
func CheckSpelling(targets []string, opts SpellingOptions) (res SpellingResult, err error) {
	root := opts.Dir
	if root == "" {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			root = cwd
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(abs)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	opts.Dir = root

	var fileList []string
	seen := make(map[string]bool)

	for _, target := range targets {
		if !filepath.IsAbs(target) {
			joined := filepath.Join(root, target)
			if strings.ContainsAny(target, "*?[") {
				mRoot, _ := filepath.Glob(joined)
				if len(mRoot) == 0 {
					mCwd, _ := filepath.Glob(target)
					if len(mCwd) > 0 {
						if abs, err := filepath.Abs(target); err == nil {
							target = abs
						} else {
							target = joined
						}
					} else {
						target = joined
					}
				} else {
					target = joined
				}
			} else {
				if _, err := os.Stat(joined); err != nil {
					if _, err2 := os.Stat(target); err2 == nil {
						if abs, err := filepath.Abs(target); err == nil {
							target = abs
						} else {
							target = joined
						}
					} else {
						target = joined
					}
				} else {
					target = joined
				}
			}
		}
		target = filepath.Clean(target)

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
		subRes, subErr := CheckSpellingFiles(opts.Dir, fileList, opts)
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
