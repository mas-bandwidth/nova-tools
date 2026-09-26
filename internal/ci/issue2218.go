package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// CILegsFromYAML reads the ci.yml workflow text and returns the GOOS values
// for which it runs legs. A leg is declared by the value of a `runs-on:` key
// or of a matrix key that feeds one (`os:`, `runner:`, `labels:`), because a
// darwin leg is usually a matrix value behind a `runs-on: ${{ ... }}`
// expression. The labels are self-hosted linux -> "linux", self-hosted macOS
// -> "darwin", and the GitHub-hosted ubuntu-latest -> "linux" and
// macos-latest -> "darwin". A comment never declares a leg. There is no
// native Windows leg since 2026-09-18.
func CILegsFromYAML(yaml string) map[string]bool {
	legs := make(map[string]bool)
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimPrefix(strings.TrimSpace(line), "- ")
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		switch key {
		case "runs-on", "os", "runner", "labels":
		default:
			continue
		}
		value, _, _ = strings.Cut(value, " #")
		for _, label := range strings.FieldsFunc(value, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
		}) {
			switch label {
			case "linux", "ubuntu-latest":
				legs["linux"] = true
			case "macOS", "macos-latest":
				legs["darwin"] = true
			}
		}
	}
	return legs
}

// PlatformLinesFromTESTSmd reads docs/TESTS.md and returns the GOOS values
// named by `Platform:` lines that appear within `## nova-*` sections.
// A platform line may be written as `Platform: darwin` or
// `Platform: recorded on macOS (darwin) — prose...`; in either case every
// GOOS the line names as a whole word, in any case ("Linux" names linux), is
// extracted. A Platform line in a `## nova-*` section that names no
// recognised GOOS is an error naming its line number, so a typo or an
// unsupported platform is never silently left out of the CI-leg check.
func PlatformLinesFromTESTSmd(md string) ([]string, error) {
	var platforms, unnamed []string
	inTool := false
	for i, line := range strings.Split(md, "\n") {
		if heading, ok := strings.CutPrefix(line, "## "); ok {
			fields := strings.Fields(heading)
			inTool = len(fields) > 0 && strings.HasPrefix(fields[0], "nova-")
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !inTool || !strings.HasPrefix(trimmed, "Platform:") {
			continue
		}
		goos := goosValues(trimmed)
		if len(goos) == 0 {
			unnamed = append(unnamed, fmt.Sprintf("line %d: %q", i+1, trimmed))
			continue
		}
		platforms = append(platforms, goos...)
	}
	if len(unnamed) > 0 {
		return platforms, fmt.Errorf("docs/TESTS.md has %d Platform line(s) in a `## nova-*` section naming no recognised GOOS (known: %s); name the GOOS the block was recorded on:\n%s", len(unnamed), strings.Join(knownGOOS, ", "), strings.Join(unnamed, "\n"))
	}
	return platforms, nil
}

var knownGOOS = []string{"linux", "darwin", "windows", "freebsd", "netbsd", "openbsd", "plan9", "solaris", "aix", "android", "illumos", "ios", "js", "wasip1"}

// goosValues returns the known GOOS values the line names as whole words,
// in any case: "(darwin)" and "Linux" name darwin and linux, "json" does not
// name js and "ratios" does not name ios.
func goosValues(line string) []string {
	words := make(map[string]bool)
	for _, w := range strings.FieldsFunc(line, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		words[strings.ToLower(w)] = true
	}
	var vals []string
	for _, g := range knownGOOS {
		if words[g] {
			vals = append(vals, g)
		}
	}
	return vals
}

// PastedDocs are the documents a stranger pastes from, per SPEC-TOOLWORK §7
// rule 7, relative to the repo root.
var PastedDocs = []string{
	"README.md",
	filepath.Join("docs", "USAGE.md"),
	filepath.Join("docs", "CLI.md"),
	filepath.Join("docs", "nova-swarm-quickstart.md"),
}

// DocExample is one pasted example and the doc it is pasted in.
type DocExample struct {
	Doc  string // relative to the repo root, as in PastedDocs
	Line string // "$ ..." or "example: ..."
}

// PastedDocExamples returns every pasted example of the PastedDocs: each $
// line inside a fenced code block, and each line of a help banner's
// `example:` block pasted inside one (as "example: <line>"). A missing doc is
// an error naming it, so the scan never silently covers fewer documents.
func PastedDocExamples(root string) ([]string, error) {
	docExamples, err := PastedDocExamplesByDoc(root)
	if err != nil {
		return nil, err
	}
	examples := make([]string, 0, len(docExamples))
	for _, e := range docExamples {
		examples = append(examples, e.Line)
	}
	return examples, nil
}

// PastedDocExamplesByDoc is PastedDocExamples with the doc each example is
// pasted in.
func PastedDocExamplesByDoc(root string) ([]DocExample, error) {
	var examples []DocExample
	for _, f := range PastedDocs {
		raw, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return nil, fmt.Errorf("pasted-example doc %s: %w", filepath.ToSlash(f), err)
		}
		lines, err := pastedLinesInFencedBlocks(string(raw))
		if err != nil {
			return nil, fmt.Errorf("pasted-example doc %s: %w", filepath.ToSlash(f), err)
		}
		for _, l := range lines {
			examples = append(examples, DocExample{Doc: filepath.ToSlash(f), Line: l})
		}
	}
	return examples, nil
}

func shellLinesInFencedBlocks(md string) []string {
	var examples []string
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence && strings.HasPrefix(trimmed, "$ ") {
			examples = append(examples, trimmed)
		}
	}
	return examples
}

// pastedLinesInFencedBlocks returns the $ lines of every fenced block and, for
// a block that is a pasted help banner, its `example:` lines through
// HelpExampleLines.
func pastedLinesInFencedBlocks(md string) ([]string, error) {
	examples := shellLinesInFencedBlocks(md)
	var block []string
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				lines, err := blockHelpExamples(strings.Join(block, "\n"))
				if err != nil {
					return nil, err
				}
				examples = append(examples, lines...)
				block = block[:0]
			}
			inFence = !inFence
			continue
		}
		if inFence {
			block = append(block, line)
		}
	}
	return examples, nil
}

// blockHelpExamples returns a fenced block's `example:` lines, prefixed
// "example: ", or none when the block carries no `example:` heading. The tool
// is the first word of the first line under the heading.
func blockHelpExamples(block string) ([]string, error) {
	banner := "\n" + block + "\n"
	_, tail, found := strings.Cut(banner, "\nexample:\n")
	if !found {
		return nil, nil
	}
	first, _, _ := strings.Cut(strings.TrimSpace(tail), "\n")
	fields := strings.Fields(first)
	if len(fields) == 0 {
		return nil, fmt.Errorf("an `example:` block with no command under it")
	}
	lines, err := HelpExampleLines(banner, fields[0])
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, "example: "+l)
	}
	return out, nil
}

// HelpExampleLines wraps onboarding.ExampleLines: the `example:` lines of a
// help banner pasted in one of the PastedDocs are pasted examples too.
func HelpExampleLines(usage, tool string) ([]string, error) {
	return onboarding.ExampleLines(usage, tool)
}

// BannerExampleLines returns every line of a help banner's `example:` block,
// whatever program leads it: the block is the run of non-blank indented lines
// directly under the heading, and it ends at the first blank or unindented
// line (the prose that follows). A line after one ending in ` \\` continues
// that command and is not an example of its own, so a command is one row,
// its first physical line, as the $ lines of the docs are. Whitespace inside a
// line is collapsed, so a banner may align its flags. onboarding.ExampleLines is the first-run reader
// and stops at the first line its tool does not lead; counting with it cut
// cmd/nova-redis's block short at its second line (`nova-secrets exec ... --
// nova-redis serve ...`), and the two nova-redis lines under it went uncounted.
// A block with no indented line under the heading is an error.
func BannerExampleLines(banner string) ([]string, error) {
	_, tail, found := strings.Cut(banner, onboarding.ExampleHeading)
	if !found {
		return nil, fmt.Errorf("the banner has no `example:` block")
	}
	var out []string
	continued := false
	for _, line := range strings.Split(tail, "\n") {
		if strings.TrimSpace(line) == "" || (line[0] != ' ' && line[0] != '\t') {
			break
		}
		if !continued {
			out = append(out, strings.Join(strings.Fields(line), " "))
		}
		continued = strings.HasSuffix(strings.TrimRight(line, " \t"), "\\")
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the `example:` block holds no indented command line")
	}
	return out, nil
}

// HelpBannerExamples returns every `example:` line of every `help` banner a
// tool under root/cmd carries, keyed "example: <line>" and mapped to the
// source file that carries it, per SPEC-TOOLWORK §7 rule 7 ("every `example:`
// line of every `help`"). A banner is a string literal (or a `+` chain of
// them) in a non-test .go file of cmd/<tool>/ holding the `\nexample:\n`
// heading; its lines are read through BannerExampleLines, every line of the
// block whatever tool leads it. A literal that is only the heading (a splice
// point such as nova-sprint's registry.go) carries no lines; any other banner
// whose example block holds no command is an error naming its file, so a
// banner is never silently left out of the count.
func HelpBannerExamples(root string) (map[string]string, error) {
	dirs, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		return nil, fmt.Errorf("help banners: %w", err)
	}
	out := make(map[string]string)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		tool := d.Name()
		files, err := filepath.Glob(filepath.Join(root, "cmd", tool, "*.go"))
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			rel := filepath.ToSlash(strings.TrimPrefix(f, root+string(filepath.Separator)))
			src, err := readSourceFile(f)
			if err != nil {
				return nil, fmt.Errorf("help banners: %w", err)
			}
			_, file, err := parseSource(f, src, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("help banners: %w", err)
			}
			for _, banner := range stringConstants(file) {
				_, tail, found := strings.Cut(banner, onboarding.ExampleHeading)
				if !found || strings.TrimSpace(tail) == "" {
					continue
				}
				lines, err := BannerExampleLines(banner)
				if err != nil {
					return nil, fmt.Errorf("help banner in %s: %w", rel, err)
				}
				for _, l := range lines {
					if _, dup := out["example: "+l]; !dup {
						out["example: "+l] = rel
					}
				}
			}
		}
	}
	return out, nil
}

// stringConstants returns every string literal of a file and every `+` chain
// made only of string literals, folded, so a banner split across lines with +
// is read whole.
func stringConstants(file *ast.File) []string {
	var out []string
	var fold func(e ast.Expr) (string, bool)
	fold = func(e ast.Expr) (string, bool) {
		switch x := e.(type) {
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return "", false
			}
			s, err := strconv.Unquote(x.Value)
			return s, err == nil
		case *ast.ParenExpr:
			return fold(x.X)
		case *ast.BinaryExpr:
			if x.Op != token.ADD {
				return "", false
			}
			l, ok := fold(x.X)
			if !ok {
				return "", false
			}
			r, ok := fold(x.Y)
			return l + r, ok
		}
		return "", false
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit, *ast.BinaryExpr:
			if s, ok := fold(x.(ast.Expr)); ok {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// ListRows returns the entries of a one-entry-per-line list (blank lines and
// '#' lines skipped, each entry trimmed), in order, without repeats.
func ListRows(list string) []string {
	var rows []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || seen[line] {
			continue
		}
		seen[line] = true
		rows = append(rows, line)
	}
	return rows
}

// AddedListRows returns the rows of head that base does not carry: for a
// shrink-only list, every one is a row the change adds, and each fails the
// class test (SPEC-TOOLWORK §7 rule 7, "a new unexecuted example fails the
// class test on the PR that adds it").
func AddedListRows(base, head string) []string {
	had := make(map[string]bool)
	for _, r := range ListRows(base) {
		had[r] = true
	}
	var added []string
	for _, r := range ListRows(head) {
		if !had[r] {
			added = append(added, r)
		}
	}
	return added
}

// UnexecutedListPath is the shrink-only list, relative to the repo root.
const UnexecutedListPath = "internal/ci/testdata/unexecuted_examples.txt"

// ChangeBase returns the commit a change is compared against: in a GitHub
// Actions run, the event's own base (pull_request.base.sha, merge_group's
// base_sha, a push's before); otherwise the merge base of HEAD and
// origin/<GITHUB_BASE_REF> (dev when unset), falling back to the local
// branch of that name. The commit is fetched once from origin when the clone
// lacks it (a --depth=1 checkout).
func ChangeBase(root string, getenv func(string) string) (string, error) {
	sha := ""
	if p := getenv("GITHUB_EVENT_PATH"); p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			var ev struct {
				PullRequest struct {
					Base struct {
						SHA string `json:"sha"`
					} `json:"base"`
				} `json:"pull_request"`
				MergeGroup struct {
					BaseSHA string `json:"base_sha"`
				} `json:"merge_group"`
				Before string `json:"before"`
			}
			if json.Unmarshal(raw, &ev) == nil {
				for _, s := range []string{ev.PullRequest.Base.SHA, ev.MergeGroup.BaseSHA, ev.Before} {
					if s != "" && strings.Trim(s, "0") != "" {
						sha = s
						break
					}
				}
			}
		}
	}
	if sha == "" {
		ref := getenv("GITHUB_BASE_REF")
		if ref == "" {
			ref = "dev"
		}
		var errs []string
		for _, r := range []string{"origin/" + ref, ref} {
			out, err := gitOut(root, "merge-base", "HEAD", r)
			if err == nil {
				sha = strings.TrimSpace(out)
				break
			}
			errs = append(errs, fmt.Sprintf("merge-base HEAD %s: %v", r, err))
		}
		if sha == "" {
			return "", fmt.Errorf("no base commit: %s", strings.Join(errs, "; "))
		}
	}
	if _, err := gitOut(root, "cat-file", "-e", sha+"^{commit}"); err != nil {
		if _, ferr := gitOut(root, "fetch", "--no-tags", "--depth=1", "origin", sha); ferr != nil {
			return "", fmt.Errorf("base %s is not in the clone and could not be fetched: %v", sha, ferr)
		}
	}
	return sha, nil
}

// ListAtCommit returns the file at rel as it stands at commit, and false when
// the commit does not carry it (the change introduces it).
func ListAtCommit(root, commit, rel string) (string, bool, error) {
	out, err := gitOut(root, "ls-tree", "--name-only", commit, "--", rel)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(out) == "" {
		return "", false, nil
	}
	body, err := gitOut(root, "show", commit+":"+rel)
	if err != nil {
		return "", false, err
	}
	return body, true, nil
}

func gitOut(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// ComparedEntry is one entry of testdata/compared_examples.txt: the test that
// executes a pasted example through the comparator.
type ComparedEntry struct {
	File string // test file, relative to the repo root
	Test string // test function name
	Ex   string // the example, exactly as listed: "$ ..." or "example: ..."
}

// testReach is what a test reaches: its own body and, transitively, every
// function declared in a _test.go file of its package that it calls or
// passes. Production code is not followed, so a usage banner the tool
// carries never counts as the test naming an example.
type testReach struct {
	comparator  bool     // calls onboarding.Compare, Execute or ExecuteWith
	literals    []string // every string literal
	transcripts [][2]string
}

func reachOf(pkgDir, test string) (testReach, bool, error) {
	var r testReach
	files, err := filepath.Glob(filepath.Join(pkgDir, "*_test.go"))
	if err != nil {
		return r, false, err
	}
	funcs := make(map[string]*ast.FuncDecl)
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			return r, false, err
		}
		for _, d := range file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Body != nil {
				funcs[fd.Name.Name] = fd
			}
		}
	}
	if _, ok := funcs[test]; !ok {
		return r, false, nil
	}
	seen := map[string]bool{test: true}
	queue := []string{test}
	for len(queue) > 0 {
		fd := funcs[queue[0]]
		queue = queue[1:]
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					if s, err := strconv.Unquote(x.Value); err == nil {
						r.literals = append(r.literals, s)
					}
				}
			case *ast.Ident:
				if _, ok := funcs[x.Name]; ok && !seen[x.Name] {
					seen[x.Name] = true
					queue = append(queue, x.Name)
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "onboarding" {
					return true
				}
				switch sel.Sel.Name {
				case "Compare", "Execute", "ExecuteWith":
					r.comparator = true
				case "FirstRun", "Transcript":
					if len(x.Args) < 2 {
						return true
					}
					tool, ok := x.Args[1].(*ast.BasicLit)
					if !ok || tool.Kind != token.STRING {
						return true
					}
					t, _ := strconv.Unquote(tool.Value)
					heading := strings.TrimPrefix(onboarding.FirstRunHeading, "### ")
					if sel.Sel.Name == "Transcript" {
						if len(x.Args) < 3 {
							return true
						}
						h, ok := x.Args[2].(*ast.BasicLit)
						if !ok || h.Kind != token.STRING {
							return true
						}
						heading, _ = strconv.Unquote(h.Value)
					}
					r.transcripts = append(r.transcripts, [2]string{t, heading})
				}
			}
			return true
		})
	}
	return r, true, nil
}

// commandText is an example with its "$ " or "example: " marker removed and
// its whitespace collapsed.
func commandText(ex string) string {
	for _, p := range []string{"$ ", "example: "} {
		if rest, ok := strings.CutPrefix(ex, p); ok {
			ex = rest
			break
		}
	}
	return strings.Join(strings.Fields(ex), " ")
}

// ComparedEntryProblem says why a compared_examples.txt entry is not a
// comparator test FOR ITS EXAMPLE, or "" when it is. docs are the docs the
// example is pasted in (none for a help-only example). The test must:
//   - be declared in a _test.go file of the example's tool's package, cmd/<tool>/;
//   - reach the comparator (onboarding.Compare, Execute or ExecuteWith) from its
//     own body or a test helper it calls;
//   - carry the example's command text: a string literal naming at least the
//     tool and its verb that the example is, word for word, or begins with
//     (`"$ nova-wake presence "` names `$ nova-wake presence --store ...`), or,
//     for a $ line, an onboarding.FirstRun/Transcript of that tool whose
//     transcript in a doc the test reads holds that exact line;
//   - for an example pasted in a doc, read one of those docs.
func ComparedEntryProblem(root string, c ComparedEntry, docs []string) string {
	if !strings.HasSuffix(c.File, "_test.go") {
		return fmt.Sprintf("%s is not a _test.go file", c.File)
	}
	cmdText := commandText(c.Ex)
	fields := strings.Fields(cmdText)
	if len(fields) == 0 {
		return fmt.Sprintf("%q names no command", c.Ex)
	}
	tool := fields[0]
	if dir := "cmd/" + tool + "/"; !strings.HasPrefix(c.File, dir) || strings.Contains(strings.TrimPrefix(c.File, dir), "/") {
		return fmt.Sprintf("%q runs %s, but %s is not in %s, so %s cannot be the test that executes it", c.Ex, tool, c.File, dir, c.Test)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(c.File))); err != nil {
		return fmt.Sprintf("cannot read %s: %v", c.File, err)
	}
	reach, found, err := reachOf(filepath.Join(root, "cmd", tool), c.Test)
	if err != nil {
		return fmt.Sprintf("cannot parse cmd/%s's tests: %v", tool, err)
	}
	if !found {
		return fmt.Sprintf("cmd/%s declares no func %s", tool, c.Test)
	}
	if !reach.comparator {
		return fmt.Sprintf("%s never reaches onboarding.Compare or onboarding.Execute, so it is not a comparator test", c.Test)
	}
	readsDoc := func(doc string) bool {
		for _, l := range reach.literals {
			if l == path.Base(doc) || l == doc {
				return true
			}
		}
		return false
	}
	var readDocs []string
	for _, d := range docs {
		if readsDoc(d) {
			readDocs = append(readDocs, d)
		}
	}
	if len(docs) > 0 && len(readDocs) == 0 {
		return fmt.Sprintf("%s reads none of %v, the docs %q is pasted in", c.Test, docs, c.Ex)
	}
	for _, l := range reach.literals {
		name := commandText(strings.TrimSpace(l))
		nf := strings.Fields(name)
		if len(nf) >= 2 && nf[0] == tool && (cmdText == name || strings.HasPrefix(cmdText, name+" ")) {
			return ""
		}
	}
	if strings.HasPrefix(c.Ex, "$ ") {
		for _, tr := range reach.transcripts {
			if tr[0] != tool {
				continue
			}
			for _, d := range readDocs {
				raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(d)))
				if err != nil {
					continue
				}
				lines, err := onboarding.Transcript(string(raw), tool, tr[1])
				if err != nil {
					continue
				}
				for _, l := range lines {
					if strings.TrimSpace(l) == c.Ex {
						return ""
					}
				}
			}
		}
	}
	return fmt.Sprintf("%s never names %q: no string literal it reaches is that command (tool and verb at least), and no transcript it runs holds the line", c.Test, c.Ex)
}
