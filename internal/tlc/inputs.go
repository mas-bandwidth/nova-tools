package tlc

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Input is one thing a TLC run of a case reads: a file under tla/, the case's
// own row of the plan, or one of the runner's files. Path names it the way the
// fingerprint does (tla/<file>, tla/CASES.tsv#<config>, internal/tlc/<file>),
// and SHA256 is the hex digest of its bytes.
type Input struct {
	Path   string
	SHA256 string
}

// CasesRowPath is the Path of a case's row of the plan.
func CasesRowPath(config string) string { return "tla/" + CasesFile + "#" + config }

// Source is where the inputs of a case are read from: the directory that holds
// its configuration and modules, the bytes of the case plan and the runner's
// files by path under the checkout root. Reading them apart lets a suite hold
// the private copy of the models to the fingerprint of the checkout's, and lets
// internal/ci hold the runner's embedded bytes to the ones on disk.
type Source struct {
	TLADir string            // root/tla, or a copy of it
	Plan   []byte            // the bytes of tla/CASES.tsv
	Runner map[string][]byte // the runner's files by path under the checkout root
}

// SourceAt reads the case plan of root/tla and takes the runner from the bytes
// this binary was built with.
func SourceAt(root string) (Source, error) {
	plan, err := os.ReadFile(filepath.Join(root, "tla", CasesFile))
	if err != nil {
		return Source{}, fmt.Errorf("cannot read %s: %v", filepath.Join(root, "tla", CasesFile), err)
	}
	runner, err := RunnerFiles()
	if err != nil {
		return Source{}, err
	}
	return Source{TLADir: filepath.Join(root, "tla"), Plan: plan, Runner: runner}, nil
}

// standardModules are the modules the pinned TLC jar bundles as TLA+ modules
// (the .tla files of its tla2sany/StandardModules). A module that names one and
// has no file of its own under tla/ reads nothing from the tree. The list is
// bookkeeping: adding a name changes no fingerprint. A module that is not here
// and not a file under tla/ refuses the case.
var standardModules = map[string]bool{
	"Bags": true, "FiniteSets": true, "Integers": true, "Naturals": true, "Randomization": true,
	"RealTime": true, "Reals": true, "Sequences": true, "TLC": true, "Toolbox": true,
}

// Inputs is exactly what a TLC run of the case reads, sorted by path: its
// configuration; the module the plan names for it and, transitively, every
// module that one extends or instantiates (a name with no file under tla/ must
// be one of TLC's standard modules); the case's own row of the plan under the
// plan's header; and the runner's result files (ResultFiles). It is an error when the case is not
// in the plan, when the configuration or a module cannot be read, or when a
// module names one that is neither a file nor a standard module.
//
// The jar is not an input: a record names it in its own column.
func (s Source) Inputs(config string) ([]Input, error) {
	head, rowText, module, err := planRow(s.Plan, config)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	read := func(name string) error {
		if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
			return fmt.Errorf("%q is not a file name in tla/", name)
		}
		raw, err := os.ReadFile(filepath.Join(s.TLADir, name))
		if err != nil {
			return err
		}
		files["tla/"+name] = raw
		return nil
	}
	if err := read(config); err != nil {
		return nil, fmt.Errorf("case %s: its configuration cannot be read: %v", config, err)
	}
	if !strings.HasSuffix(module, ".tla") {
		return nil, fmt.Errorf("case %s: its module %q is not a .tla file", config, module)
	}
	// A breadth-first walk over the modules a module names.
	type pending struct{ file, by string }
	queue := []pending{{module, ""}}
	seen := map[string]bool{}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next.file] {
			continue
		}
		seen[next.file] = true
		if err := read(next.file); err != nil {
			if next.by == "" {
				return nil, fmt.Errorf("case %s: its module %s cannot be read: %v", config, next.file, err)
			}
			return nil, fmt.Errorf("case %s: module %s, named by %s, cannot be read: %v", config, next.file, next.by, err)
		}
		for _, name := range ModuleReferences(files["tla/"+next.file]) {
			file := name + ".tla"
			if _, err := os.Stat(filepath.Join(s.TLADir, file)); err != nil {
				if standardModules[name] {
					continue
				}
				return nil, fmt.Errorf("case %s: module %s names %s, which is neither tla/%s nor one of TLC's standard modules; add the module file tla/%s, or add the name to standardModules in internal/tlc/inputs.go if the TLC jar bundles it", config, next.file, name, file, file)
			}
			queue = append(queue, pending{file, next.file})
		}
	}
	files[CasesRowPath(config)] = []byte(head + "\n" + rowText + "\n")
	for path, raw := range s.Runner {
		files[path] = raw
	}
	out := make([]Input, 0, len(files))
	for path, raw := range files {
		sum := sha256.Sum256(raw)
		out = append(out, Input{Path: path, SHA256: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Digest is the fingerprint of a list of inputs: SHA-256 over, for each input
// in path order, its path, a NUL, the hex digest of its bytes and a newline.
// It holds no time, no host and no absolute path, so the same inputs give the
// same digest wherever they are read.
func Digest(inputs []Input) string {
	sorted := append([]Input(nil), inputs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	for _, in := range sorted {
		h.Write([]byte(in.Path))
		h.Write([]byte{0})
		h.Write([]byte(in.SHA256))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Fingerprint is the digest of a case's inputs and the number of files it
// covers.
func (s Source) Fingerprint(config string) (string, int, error) {
	inputs, err := s.Inputs(config)
	if err != nil {
		return "", 0, err
	}
	return Digest(inputs), len(inputs), nil
}

// planRow finds the row of config in the plan. It returns the header and the
// row as the tab-joined text of their fields (so the quoting or the line ending
// of the file never changes a fingerprint) and the module the row names.
func planRow(plan []byte, config string) (head, row, module string, err error) {
	cr := csv.NewReader(bytes.NewReader(plan))
	cr.Comma = '\t'
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return "", "", "", fmt.Errorf("%s is not tab-separated text: %v", CasesFile, err)
	}
	if len(rows) == 0 {
		return "", "", "", errors.New(CasesFile + " is empty")
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	moduleCol, ok := col["module"]
	if _, has := col["config"]; !ok || !has || col["config"] != 0 {
		return "", "", "", fmt.Errorf("%s has no config column first and a module column", CasesFile)
	}
	var found []string
	for _, r := range rows[1:] {
		if len(r) > 0 && r[0] == config {
			if found != nil {
				return "", "", "", fmt.Errorf("%s names %s twice", CasesFile, config)
			}
			found = r
		}
	}
	if found == nil {
		return "", "", "", fmt.Errorf("%s declares no case %s", CasesFile, config)
	}
	if len(found) != len(rows[0]) {
		return "", "", "", fmt.Errorf("%s row of %s has %d fields, want %d", CasesFile, config, len(found), len(rows[0]))
	}
	return strings.Join(rows[0], "\t"), strings.Join(found, "\t"), found[moduleCol], nil
}

var (
	extendsRE  = regexp.MustCompile(`\bEXTENDS\b\s*([A-Za-z0-9_]+(?:\s*,\s*[A-Za-z0-9_]+)*)`)
	instanceRE = regexp.MustCompile(`\bINSTANCE\s+([A-Za-z0-9_]+)`)
	moduleOpen = regexp.MustCompile(`^\s*-{4,}\s*MODULE\s+([A-Za-z0-9_]+)\s*-{4,}`)
	moduleEnd  = regexp.MustCompile(`^\s*={4,}`)
)

// ModuleReferences returns the names of the modules a module's text extends or
// instantiates (`EXTENDS A, B`, `INSTANCE M`, `LOCAL INSTANCE M`,
// `F(x) == INSTANCE M WITH ...`), each once, in order of appearance, at every
// depth of nesting: a module may hold modules (`---- MODULE X ----` opens one
// and a line of `=` signs closes it), and each of them names modules of its
// own. A name that a module of the same text declares is not a file and is left
// out. Comments and strings are not read, neither is text before the first
// module header, nor anything after the line that closes the outermost module.
// A text with no module header is read up to its first closing line.
func ModuleReferences(text []byte) []string {
	lines := strings.Split(string(stripComments(text)), "\n")
	first := -1
	for i, line := range lines {
		if moduleOpen.MatchString(line) {
			first = i
			break
		}
	}
	declared := map[string]bool{}
	var body strings.Builder
	depth := 0
	for i := max(first, 0); i < len(lines); i++ {
		line := lines[i]
		switch m := moduleOpen.FindStringSubmatch(line); {
		case m != nil:
			depth++
			declared[m[1]] = true
			body.WriteByte('\n')
		case moduleEnd.MatchString(line):
			depth--
			body.WriteByte('\n')
			if depth <= 0 {
				i = len(lines)
			}
		default:
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	clean := []byte(body.String())
	type hit struct {
		at    int
		names []string
	}
	var hits []hit
	for _, m := range extendsRE.FindAllSubmatchIndex(clean, -1) {
		var names []string
		for _, n := range strings.Split(string(clean[m[2]:m[3]]), ",") {
			names = append(names, strings.TrimSpace(n))
		}
		hits = append(hits, hit{m[0], names})
	}
	for _, m := range instanceRE.FindAllSubmatchIndex(clean, -1) {
		hits = append(hits, hit{m[0], []string{string(clean[m[2]:m[3]])}})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	var out []string
	seen := map[string]bool{}
	for _, h := range hits {
		for _, n := range h.names {
			if !seen[n] && !declared[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// stripComments blanks the comments (`\* ...` to the end of the line and nested
// `(* ... *)`) and the contents of strings, keeping the length and the line
// breaks.
func stripComments(text []byte) []byte {
	out := make([]byte, len(text))
	copy(out, text)
	blank := func(from, to int) {
		for k := from; k < to; k++ {
			if out[k] != '\n' {
				out[k] = ' '
			}
		}
	}
	for i := 0; i < len(text); {
		switch {
		case text[i] == '"':
			j := i + 1
			for j < len(text) && text[j] != '"' && text[j] != '\n' {
				if text[j] == '\\' && j+1 < len(text) {
					j++
				}
				j++
			}
			if j < len(text) && text[j] == '"' {
				j++
			}
			blank(i, j)
			i = j
		case text[i] == '(' && i+1 < len(text) && text[i+1] == '*':
			depth, j := 1, i+2
			for j < len(text) && depth > 0 {
				switch {
				case text[j] == '(' && j+1 < len(text) && text[j+1] == '*':
					depth++
					j += 2
				case text[j] == '*' && j+1 < len(text) && text[j+1] == ')':
					depth--
					j += 2
				default:
					j++
				}
			}
			blank(i, j)
			i = j
		case text[i] == '\\' && i+1 < len(text) && text[i+1] == '*':
			j := i
			for j < len(text) && text[j] != '\n' {
				j++
			}
			blank(i, j)
			i = j
		default:
			i++
		}
	}
	return out
}
