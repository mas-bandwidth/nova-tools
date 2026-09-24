// Package ctxindex is the per-repo context index a card cutter reads so a worker
// starts from the spec paragraph, the guarding test and the file list instead of
// exploring for them (nova-tools#2498 S2: an OK card cost 39k-108k input tokens,
// almost all exploration).
//
// Three indices, each a directory of hash buckets, so a lookup reads the one bucket
// its key hashes to and never scans (Glenn 2026-09-23: "no linear scans"):
//
//	<head>/spec/<b>.json      spec ID -> the paragraph it names, where, and its guarding tests
//	<head>/test/<b>.json      test (<dir>.<TestName>) -> where it is and the files it covers
//	<head>/symbol/<b>.json    symbol (<dir>.<Name> or <dir>.<Type>.<Method>) -> definition and guarding tests
//
// The bucket count per index is sized at build time to keep a bucket near BucketSize
// entries and is written into <head>/HEAD, so a bucket stays small however the repo grows. One
// file per key was measured first and rejected: 30k files took minutes to write on a
// loaded Studio, where the buckets are a few hundred files.
//
// A spec ID is the backticked kebab-case name at a numbered item of a tracked Markdown
// file (6. `cut-line1-is-contract`: every card ...), and a test guards it when its doc
// comment opens with that name and a colon (// cut-line1-is-contract: ...), the
// convention the repo's specs and tests already follow.
//
// Build is run at each landing, on a clone at the landed tip. Each head is built into
// its own directory <out>/<head>/, every bucket written whole (temp file then rename),
// and the one-line <out>/CURRENT file naming the head is renamed into place last. That
// rename is the swap: a reader opens CURRENT and reads only that head's directory, so a
// cut during a build, or after a build that failed partway, reads the previous complete
// index whole, never a mix. Build never deletes: older head directories stay until the
// caller prunes them. A bucket that is missing or carries another head inside the
// CURRENT directory is a damaged index, and the lookup returns an error so the cut is
// refused instead of carrying no CONTEXT.
package ctxindex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"hash/fnv"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Spec is one spec ID's entry.
type Spec struct {
	ID        string   `json:"id"`
	File      string   `json:"file"`
	Line      int      `json:"line"`
	Paragraph string   `json:"paragraph"`
	Tests     []string `json:"tests"` // test keys, sorted
}

// Test is one test function's entry.
type Test struct {
	Key    string   `json:"key"` // <dir>.<TestName>
	File   string   `json:"file"`
	Line   int      `json:"line"`
	Specs  []string `json:"specs,omitempty"`
	Covers []string `json:"covers"` // non-test files of the package whose symbols the test names
}

// Symbol is one top-level definition's entry.
type Symbol struct {
	Key   string   `json:"key"` // <dir>.<Name> or <dir>.<Type>.<Method>
	File  string   `json:"file"`
	Line  int      `json:"line"`
	Tests []string `json:"tests"` // test keys whose body names it, sorted, capped
}

// Stats is what one Build wrote.
type Stats struct {
	Head    string
	Specs   int
	Tests   int
	Symbols int
	Guarded int // spec IDs with at least one guarding test
}

// MaxParagraph caps one spec paragraph; a numbered item longer than this is cut.
const MaxParagraph = 2000

// BucketSize is the entry count a bucket is sized for; an index of n entries gets the
// power of two at or above n/BucketSize buckets.
var BucketSize = 64

// MaxSymbolTests caps the guarding tests one symbol entry lists.
const MaxSymbolTests = 20

var (
	specItemRE = regexp.MustCompile("^\\s*[0-9]+\\.\\s+`([a-z][a-z0-9]*(?:-[a-z0-9]+)+)`:\\s*(.*)$")
	numItemRE  = regexp.MustCompile(`^\s*[0-9]+\.\s`)
	testTagRE  = regexp.MustCompile(`^([a-z][a-z0-9]*(?:-[a-z0-9]+)+):`)
	specIDRE   = regexp.MustCompile(`[a-z][a-z0-9]*(?:-[a-z0-9]+)+`)
	headRE     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Build indexes the tracked files of the git clone at repo into out.
func Build(repo, out string) (Stats, error) {
	head, err := git(repo, "rev-parse", "HEAD")
	if err != nil {
		return Stats{}, err
	}
	head = strings.TrimSpace(head)
	if !headRE.MatchString(head) {
		return Stats{}, fmt.Errorf("git rev-parse HEAD in %s printed %q, not a sha", repo, head)
	}
	listed, err := git(repo, "ls-files", "-z")
	if err != nil {
		return Stats{}, err
	}
	var mds []string
	goDirs := map[string][]string{}
	for _, f := range strings.Split(listed, "\x00") {
		switch {
		case f == "":
		case strings.HasSuffix(f, ".md"):
			mds = append(mds, f)
		case strings.HasSuffix(f, ".go") && !skippedGoPath(f):
			d := path.Dir(f)
			goDirs[d] = append(goDirs[d], f)
		}
	}
	specs, err := indexSpecs(repo, mds)
	if err != nil {
		return Stats{}, err
	}
	tests, symbols, err := indexGo(repo, goDirs)
	if err != nil {
		return Stats{}, err
	}
	for _, k := range sortedKeys(tests) {
		t := tests[k]
		for _, id := range t.Specs {
			if s, ok := specs[id]; ok {
				s.Tests = append(s.Tests, k)
			}
		}
	}
	st := Stats{Head: head, Specs: len(specs), Tests: len(tests), Symbols: len(symbols)}
	for _, s := range specs {
		if len(s.Tests) > 0 {
			st.Guarded++
		}
	}
	dir := filepath.Join(out, head)
	counts := map[string]int{}
	for _, kind := range []struct {
		name    string
		entries map[string]any
	}{
		{"spec", asAny(specs)}, {"test", asAny(tests)}, {"symbol", asAny(symbols)},
	} {
		n, err := writeBuckets(dir, kind.name, head, kind.entries)
		if err != nil {
			return Stats{}, err
		}
		if err := afterBuckets(kind.name); err != nil {
			return Stats{}, err
		}
		counts[kind.name] = n
	}
	headLine := fmt.Sprintf("%s spec=%d test=%d symbol=%d\n", head, counts["spec"], counts["test"], counts["symbol"])
	if err := writeAtomic(filepath.Join(dir, "HEAD"), []byte(headLine)); err != nil {
		return Stats{}, err
	}
	// The swap: CURRENT names the head only once its directory is complete.
	if err := writeAtomic(filepath.Join(out, "CURRENT"), []byte(head+"\n")); err != nil {
		return Stats{}, err
	}
	return st, nil
}

// afterBuckets runs after each index's buckets are written; a test makes it fail to
// stand for a build that stops partway.
var afterBuckets = func(kind string) error { return nil }

// skippedGoPath is a Go file the index does not read: fixtures and vendored code.
func skippedGoPath(f string) bool {
	for _, part := range strings.Split(f, "/") {
		if part == "testdata" || part == "vendor" {
			return true
		}
	}
	return false
}

// indexSpecs reads every numbered spec item. docs/SPEC-*.md are read first so the
// normative text wins over a copy of it elsewhere; the first occurrence of an ID wins.
func indexSpecs(repo string, mds []string) (map[string]*Spec, error) {
	sort.SliceStable(mds, func(i, j int) bool {
		pi, pj := specRank(mds[i]), specRank(mds[j])
		if pi != pj {
			return pi < pj
		}
		return mds[i] < mds[j]
	})
	specs := map[string]*Spec{}
	for _, f := range mds {
		raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(f)))
		if err != nil {
			return nil, err
		}
		lines := strings.Split(string(raw), "\n")
		for i := 0; i < len(lines); i++ {
			m := specItemRE.FindStringSubmatch(lines[i])
			if len(m) != 3 {
				continue
			}
			id := m[1]
			if _, seen := specs[id]; seen {
				continue
			}
			para := []string{strings.TrimSpace(lines[i])}
			for j := i + 1; j < len(lines); j++ {
				l := lines[j]
				if strings.TrimSpace(l) == "" || numItemRE.MatchString(l) || strings.HasPrefix(l, "#") {
					break
				}
				para = append(para, strings.TrimSpace(l))
			}
			text := strings.Join(para, " ")
			if len(text) > MaxParagraph {
				text = text[:MaxParagraph] + " ..."
			}
			specs[id] = &Spec{ID: id, File: f, Line: i + 1, Paragraph: text}
		}
	}
	return specs, nil
}

func specRank(f string) int {
	base := path.Base(f)
	switch {
	case path.Dir(f) == "docs" && strings.HasPrefix(base, "SPEC-"):
		return 0
	case strings.HasPrefix(f, "docs/"):
		return 1
	}
	return 2
}

type pkgDef struct {
	key  string
	file string
}

// indexGo parses each directory's Go files once: its top-level definitions, then each
// Test function's doc tag and the definitions its body names.
func indexGo(repo string, goDirs map[string][]string) (map[string]*Test, map[string]*Symbol, error) {
	tests := map[string]*Test{}
	symbols := map[string]*Symbol{}
	fset := token.NewFileSet()
	for _, dir := range sortedKeys(goDirs) {
		files := goDirs[dir]
		sort.Strings(files)
		type parsed struct {
			rel string
			f   *ast.File
		}
		var src, tst []parsed
		for _, rel := range files {
			mode := parser.SkipObjectResolution
			isTest := strings.HasSuffix(rel, "_test.go")
			if isTest {
				mode |= parser.ParseComments
			}
			f, err := parser.ParseFile(fset, filepath.Join(repo, filepath.FromSlash(rel)), nil, mode)
			if err != nil {
				continue // a file that does not parse is not indexed; the build catches it
			}
			if isTest {
				tst = append(tst, parsed{rel, f})
			} else {
				src = append(src, parsed{rel, f})
			}
		}
		byName := map[string][]pkgDef{}
		for _, p := range src {
			for _, d := range p.f.Decls {
				for _, def := range topLevel(d) {
					key := dir + "." + def.key
					line := fset.Position(def.pos).Line
					symbols[key] = &Symbol{Key: key, File: p.rel, Line: line, Tests: []string{}}
					byName[def.name] = append(byName[def.name], pkgDef{key: key, file: p.rel})
				}
			}
		}
		for _, p := range tst {
			for _, d := range p.f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Body == nil {
					continue
				}
				key := dir + "." + fn.Name.Name
				t := &Test{Key: key, File: p.rel, Line: fset.Position(fn.Pos()).Line, Covers: []string{}}
				if fn.Doc != nil {
					first, _, _ := strings.Cut(fn.Doc.Text(), "\n")
					if m := testTagRE.FindStringSubmatch(first); len(m) == 2 {
						t.Specs = []string{m[1]}
					}
				}
				covers := map[string]bool{}
				named := map[string]bool{}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok {
						named[id.Name] = true
					}
					return true
				})
				for name := range named {
					for _, def := range byName[name] {
						covers[def.file] = true
						if s := symbols[def.key]; len(s.Tests) < MaxSymbolTests {
							s.Tests = append(s.Tests, key)
						}
					}
				}
				for f := range covers {
					t.Covers = append(t.Covers, f)
				}
				sort.Strings(t.Covers)
				tests[key] = t
			}
		}
	}
	for _, s := range symbols {
		sort.Strings(s.Tests)
	}
	return tests, symbols, nil
}

type topDef struct {
	name string // the identifier a caller writes
	key  string // Name, or Type.Method
	pos  token.Pos
}

func topLevel(d ast.Decl) []topDef {
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil {
			return []topDef{{d.Name.Name, d.Name.Name, d.Pos()}}
		}
		if len(d.Recv.List) == 1 {
			if recv := recvType(d.Recv.List[0].Type); recv != "" {
				return []topDef{{d.Name.Name, recv + "." + d.Name.Name, d.Pos()}}
			}
		}
	case *ast.GenDecl:
		if d.Tok != token.TYPE {
			return nil
		}
		var out []topDef
		for _, s := range d.Specs {
			if ts, ok := s.(*ast.TypeSpec); ok {
				out = append(out, topDef{ts.Name.Name, ts.Name.Name, ts.Pos()})
			}
		}
		return out
	}
	return nil
}

func recvType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvType(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvType(t.X)
	case *ast.IndexListExpr:
		return recvType(t.X)
	}
	return ""
}

// Index is an opened index: the head CURRENT names and each index's bucket count.
type Index struct {
	dir     string // <out>/<head>
	Head    string
	buckets map[string]int
}

// ErrNoIndex is an index directory with no CURRENT: no build ever completed.
var ErrNoIndex = errors.New("no CURRENT: the index was never built (run nova-pulse index --repo <clone> --out <dir>)")

// Open reads CURRENT, the head of the last complete build, then that head's HEAD line:
// the sha and each index's bucket count.
func Open(out string) (*Index, error) {
	cur, err := os.ReadFile(filepath.Join(out, "CURRENT"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", out, ErrNoIndex)
	}
	if err != nil {
		return nil, err
	}
	head := strings.TrimSpace(string(cur))
	if !headRE.MatchString(head) {
		return nil, fmt.Errorf("%s/CURRENT holds %q, not a sha", out, head)
	}
	dir := filepath.Join(out, head)
	raw, err := os.ReadFile(filepath.Join(dir, "HEAD"))
	if err != nil {
		return nil, fmt.Errorf("%s/CURRENT names %s but its HEAD is unreadable: %w", out, head[:12], err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 4 || fields[0] != head {
		return nil, fmt.Errorf("%s/HEAD holds %q, not %s spec=<n> test=<n> symbol=<n>", dir, strings.TrimSpace(string(raw)), head)
	}
	ix := &Index{dir: dir, Head: head, buckets: map[string]int{}}
	for _, f := range fields[1:] {
		name, val, _ := strings.Cut(f, "=")
		var n int
		if _, err := fmt.Sscanf(val, "%d", &n); err != nil || n < 1 || n&(n-1) != 0 {
			return nil, fmt.Errorf("%s/HEAD: %q is not <index>=<power of two>", dir, f)
		}
		ix.buckets[name] = n
	}
	for _, kind := range []string{"spec", "test", "symbol"} {
		if ix.buckets[kind] == 0 {
			return nil, fmt.Errorf("%s/HEAD names no %s bucket count", dir, kind)
		}
	}
	return ix, nil
}

// Spec looks one spec ID up.
func (ix *Index) Spec(id string) (Spec, bool, error) {
	var s Spec
	ok, err := ix.read("spec", id, &s)
	return s, ok, err
}

// Test looks one test key (<dir>.<TestName>) up.
func (ix *Index) Test(key string) (Test, bool, error) {
	var t Test
	ok, err := ix.read("test", key, &t)
	return t, ok, err
}

// Symbol looks one symbol key (<dir>.<Name> or <dir>.<Type>.<Method>) up.
func (ix *Index) Symbol(key string) (Symbol, bool, error) {
	var s Symbol
	ok, err := ix.read("symbol", key, &s)
	return s, ok, err
}

// bucketFile is the one file a key of an index of n buckets lives in.
type bucketFile struct {
	Head    string                     `json:"head"`
	Entries map[string]json.RawMessage `json:"entries"`
}

func bucketOf(key string, n int) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & uint32(n-1))
}

func bucketName(b int) string { return fmt.Sprintf("%04x.json", b) }

func (ix *Index) read(kind, key string, v any) (bool, error) {
	p := filepath.Join(ix.dir, kind, bucketName(bucketOf(key, ix.buckets[kind])))
	// Every bucket of a complete build exists and carries its head, so a missing or
	// foreign bucket is a damaged index: an error, never a silent miss.
	raw, err := os.ReadFile(p)
	if err != nil {
		return false, fmt.Errorf("index at %s damaged: %w (rebuild with nova-pulse index)", ix.Head[:12], err)
	}
	var bf bucketFile
	if err := json.Unmarshal(raw, &bf); err != nil {
		return false, fmt.Errorf("%s: %w", p, err)
	}
	if bf.Head != ix.Head {
		return false, fmt.Errorf("index at %s damaged: %s was built at %.12s (rebuild with nova-pulse index)", ix.Head[:12], p, bf.Head)
	}
	entry, ok := bf.Entries[key]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(entry, v); err != nil {
		return false, fmt.Errorf("%s: %s: %w", p, key, err)
	}
	return true, nil
}

// bucketCount is the power of two at or above n/BucketSize, at least one.
func bucketCount(n int) int {
	size := BucketSize
	if size < 1 {
		size = 1
	}
	k := 1
	for k*size < n {
		k *= 2
	}
	return k
}

// writeBuckets writes every bucket of one index, empty ones included, and returns the
// bucket count.
func writeBuckets(out, kind, head string, entries map[string]any) (int, error) {
	if err := os.MkdirAll(filepath.Join(out, kind), 0o755); err != nil {
		return 0, err
	}
	n := bucketCount(len(entries))
	buckets := make([]bucketFile, n)
	for i := range buckets {
		buckets[i] = bucketFile{Head: head, Entries: map[string]json.RawMessage{}}
	}
	for key, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		buckets[bucketOf(key, n)].Entries[key] = raw
	}
	for i, bf := range buckets {
		raw, err := json.Marshal(bf)
		if err != nil {
			return 0, err
		}
		if err := writeAtomic(filepath.Join(out, kind, bucketName(i)), append(raw, '\n')); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func asAny[V any](m map[string]V) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// SpecIDs are the spec-ID-shaped words of text, in first-seen order, each once. The
// caller looks each up; a word that is not an ID simply misses.
func SpecIDs(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range specIDRE.FindAllString(text, -1) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func writeAtomic(p string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func git(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(firstLine(errb.String()))
		return "", fmt.Errorf("git %s in %s: %v: %s", strings.Join(args, " "), repo, err, msg)
	}
	return out.String(), nil
}

func firstLine(s string) string {
	sc := bufio.NewScanner(strings.NewReader(s))
	if sc.Scan() {
		return sc.Text()
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MaxCardSpecs caps the spec IDs one card carries context for.
const MaxCardSpecs = 5

// Context is the block a card carries for the spec IDs text names: per ID its paragraph,
// its guarding tests, and the files those tests cover. It is empty when text names none.
func (ix *Index) Context(text string) (string, error) {
	var b strings.Builder
	n := 0
	for _, id := range SpecIDs(text) {
		if n == MaxCardSpecs {
			break
		}
		s, ok, err := ix.Spec(id)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		n++
		fmt.Fprintf(&b, "SPEC %s (%s:%d): %s\n", s.ID, s.File, s.Line, s.Paragraph)
		files := map[string]bool{}
		guarded := false
		for _, key := range s.Tests {
			t, ok, err := ix.Test(key)
			if err != nil {
				return "", err
			}
			if !ok {
				continue
			}
			guarded = true
			fmt.Fprintf(&b, "GUARDING TEST: %s (%s:%d)\n", t.Key, t.File, t.Line)
			files[t.File] = true
			for _, f := range t.Covers {
				files[f] = true
			}
		}
		if !guarded {
			fmt.Fprintf(&b, "GUARDING TEST: none indexed; write it red first with its doc comment opening `%s:`\n", s.ID)
			continue
		}
		fmt.Fprintf(&b, "FILES: %s\n", strings.Join(sortedKeys(files), ", "))
	}
	if n == 0 {
		return "", nil
	}
	head := ix.Head
	if len(head) > 12 {
		head = head[:12]
	}
	return fmt.Sprintf("CONTEXT: from the repo index at %s; start here, not from a search.\n", head) + b.String(), nil
}
