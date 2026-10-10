package sprint

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/hygiene"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// Paths admission (docs/SPEC-CARD-CONTRACT.md, what admission verifies; docs/SPEC-SPRINT.md
// section 11, the brief checks). nova-sprint add, brief and recut hold a brief that names
// PATHS, REPO and BASE against the BASE tip: a literal path must exist, a glob must match
// one file, and every func, type or verb STOP or START names with a file, and a TEST name
// the tree already holds, must occur inside a PATHS file. A miss names the nearest file
// that holds the identifier, so the author fixes PATHS in one edit.

// AdmissionTree is the BASE tip the check reads. Files are the repo-relative paths at
// that tip. Hold is a plain grep: the files whose text contains ident, anywhere in the
// tree. Missing is why the tip could not be read, and then the check says so once and
// invents no path miss. At is how a refusal names the tip.
type AdmissionTree struct {
	Files   []string
	Hold    func(ident string) ([]string, error)
	Missing string
	At      string
}

// namedIdent is one identifier a brief names together with a repository path.
type namedIdent struct {
	kind, name, file string
}

var (
	// admitIdentRE is the identifier, the kind then the name: func, type, verb, or a test.
	// A qualified name (sprint.AdmissionTree) is kept whole here; admitBareIdent
	// greps the last component. A file name such as file.go is not a qualifier.
	admitIdentRE = regexp.MustCompile(`(?i)\b(func|type|verb|test)\s+([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)`)
	// admitPathRE is a repository path in prose: segments, or one file name with an extension.
	admitPathRE = regexp.MustCompile(`[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.*-]+)+|[A-Za-z0-9_-]+\.[A-Za-z0-9]+`)
	// admitTestRE is a Go test name, the only name the kind "test" takes.
	admitTestRE = regexp.MustCompile(`^(Test|Example|Fuzz)[A-Za-z0-9_]*$`)
)

// PathsAdmission is the miss lines of brief against tree, none when the brief passes or
// names no PATHS, or no REPO, or no BASE. One line per miss. A tree that was not handed
// over is one MISSING line.
func PathsAdmission(brief string, tree AdmissionTree) []string {
	h := admitHeader(brief)
	paths := admitList(h["PATHS"])
	if len(paths) == 0 || strings.TrimSpace(h["REPO"]) == "" || strings.TrimSpace(h["BASE"]) == "" {
		return nil
	}
	at := tree.At
	if at == "" {
		at = "the base"
	}
	if tree.Missing != "" {
		return []string{"MISSING: " + strings.TrimPrefix(tree.Missing, "MISSING: ")}
	}
	news := admitList(h["NEW"])
	idents := admitIdents(h["START"] + "\n" + h["STOP"])
	if tl, why := cardhdr.ParseTest(h["TEST"]); why == "" && !tl.None && tl.Name != "" {
		pkg := strings.TrimPrefix(strings.TrimPrefix(tl.Package, "./"), "/")
		idents = append(idents, namedIdent{kind: "test", name: tl.Name, file: pkg})
	}
	skipExist := false
	if _, ok := member.CarryOf(brief); ok {
		// a widen's new paths may exist only at the carried head; the identifiers are still checked
		skipExist = true
	}
	var out []string
	hold := func(ident string) ([]string, error) {
		if tree.Hold == nil {
			return nil, nil
		}
		return tree.Hold(ident)
	}
	if !skipExist {
		for _, e := range paths {
			c := admitClean(e)
			if admitNewTestFile(c) || admitAnsweredByNew(c, news) || admitHits(tree.Files, c) {
				continue
			}
			near, err := admitNearestHolder(hold, idents, c, tree.Files)
			if err != nil {
				return append(out, "MISSING: "+err.Error())
			}
			out = append(out, admitExistLine(e, at, near))
		}
	}
	seen := map[string]bool{}
	for _, id := range idents {
		key := id.kind + " " + id.name
		if seen[key] {
			continue
		}
		seen[key] = true
		// a TEST name the tree does not hold is the new red test
		files, err := hold(id.name)
		if err != nil {
			return append(out, "MISSING: "+err.Error())
		}
		if len(files) == 0 {
			if id.kind == "test" {
				continue
			}
			out = append(out, fmt.Sprintf("PATHS do not hold %s %s named with %s: no file at %s holds it", id.kind, id.name, id.file, at))
			continue
		}
		if admitAnyCovered(paths, files) {
			continue
		}
		if near := nearestFile(files, id.file); near != "" {
			out = append(out, fmt.Sprintf("PATHS do not hold %s %s named with %s: it is in %s", id.kind, id.name, id.file, near))
			continue
		}
		out = append(out, fmt.Sprintf("PATHS do not hold %s %s named with %s: no file at %s holds it", id.kind, id.name, id.file, at))
	}
	return out
}

// admitExistLine is one PATHS entry that names nothing, with the nearest file when there is one.
func admitExistLine(entry, at, near string) string {
	kind := "names"
	if strings.ContainsAny(entry, "*?[") {
		kind = "matches"
	}
	if near == "" {
		return fmt.Sprintf("PATHS %s %s nothing at %s; no file at the base is near it", entry, kind, at)
	}
	return fmt.Sprintf("PATHS %s %s nothing at %s; the nearest file is %s", entry, kind, at, near)
}

// admitNearestHolder is the nearest file that holds an identifier named with entry, else
// the nearest file in the tree to entry.
func admitNearestHolder(hold func(string) ([]string, error), idents []namedIdent, entry string, files []string) (string, error) {
	for _, id := range idents {
		if id.file != entry && !strings.HasPrefix(id.file, entry+"/") {
			continue
		}
		got, err := hold(id.name)
		if err != nil {
			return "", err
		}
		if near := nearestFile(got, entry); near != "" {
			return near, nil
		}
	}
	return nearestFile(files, entry), nil
}

// admitHeader is the brief's header keys, the unbroken KEY: lines under line 1. A blank
// is skipped; the first prose line ends it.
func admitHeader(brief string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(brief, "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := cardhdr.KeyValue(line)
		if !ok {
			break
		}
		if _, seen := out[k]; !seen {
			out[k] = v
		}
	}
	return out
}

// admitList is a PATHS-shaped value's entries. none and - name nothing.
func admitList(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if p != "" && p != "none" && p != "-" {
			out = append(out, p)
		}
	}
	return out
}

// admitClean is an entry as the matcher reads it: no leading ./, no trailing /.
func admitClean(e string) string {
	return strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(e), "/"), "./")
}

// admitNewTestFile is a literal path whose base name is a test file the card may create.
func admitNewTestFile(p string) bool {
	if strings.ContainsAny(p, "*?[") {
		return false
	}
	b := path.Base(p)
	return strings.Contains(b, "_test.") || strings.HasPrefix(b, "test_")
}

// admitAnsweredByNew says a NEW: entry names e.
func admitAnsweredByNew(e string, news []string) bool {
	for _, n := range news {
		n = admitClean(n)
		if n == e || hygiene.MatchGlob(e, n) || hygiene.MatchGlob(n, e) {
			return true
		}
	}
	return false
}

// admitHits says e names a file, a directory, or a glob match in files.
func admitHits(files []string, e string) bool {
	for _, f := range files {
		if f == e || strings.HasPrefix(f, e+"/") || hygiene.MatchGlob(e, f) {
			return true
		}
	}
	return false
}

// admitAnyCovered says some file of files is covered by a PATHS entry.
func admitAnyCovered(paths, files []string) bool {
	for _, f := range files {
		for _, e := range paths {
			e = admitClean(e)
			if e == f || strings.HasPrefix(f, e+"/") || hygiene.MatchGlob(e, f) {
				return true
			}
		}
	}
	return false
}

// admitIdents is every func, type, verb or test name text names in the same clause as a
// repository path. A clause is a line, or a sentence, or a piece cut at a semicolon.
// Markdown code-span delimiters are not part of the name, and a qualified name is its
// last component, so a backticked sprint.cmdBrief is cmdBrief.
func admitIdents(text string) []namedIdent {
	var out []namedIdent
	for _, clause := range admitClauses(text) {
		clause = admitNormalize(clause)
		found := admitIdentRE.FindAllStringSubmatch(clause, -1)
		paths := admitPathsBesideIdents(admitPathRE.FindAllString(clause, -1), found)
		if len(paths) == 0 {
			continue
		}
		for _, m := range found {
			kind := strings.ToLower(m[1])
			name, _ := admitBareIdent(m[2])
			if kind == "test" && !admitTestRE.MatchString(name) {
				continue
			}
			out = append(out, namedIdent{kind: kind, name: name, file: admitClean(admitPathNear(clause, m[0], paths))})
		}
	}
	return out
}

// admitNormalize drops Markdown code-span backticks, so a backticked name is the
// word the tree holds. The backticks are not part of the identifier or the path.
func admitNormalize(s string) string {
	return strings.ReplaceAll(s, "`", "")
}

// admitBareIdent is the name a grep looks for. A qualified identifier's last component
// is that name (sprint.AdmissionTree is AdmissionTree). A trailing file extension
// (file.go) is not a qualifier: the name is the part before it, as a bare word.
func admitBareIdent(raw string) (name string, qualified bool) {
	i := strings.LastIndex(raw, ".")
	if i <= 0 || i == len(raw)-1 {
		return raw, false
	}
	last := raw[i+1:]
	if admitFileExt(last) {
		return raw[:i], false
	}
	return last, true
}

// admitFileExt reports a short suffix that is a repository file's extension, not a Go name.
func admitFileExt(s string) bool {
	switch s {
	case "go", "md", "txt", "sql", "yml", "yaml", "tla", "cfg", "tsv", "png",
		"js", "css", "log", "sum", "mod", "json", "html", "lock", "xml", "sh", "py", "toml":
		return true
	default:
		return false
	}
}

// admitPathsBesideIdents drops a path match that is only a qualified identifier.
// sprint.AdmissionTree matches the file-name pattern and is not a repository path.
func admitPathsBesideIdents(paths []string, idents [][]string) []string {
	drop := map[string]bool{}
	for _, m := range idents {
		if _, qualified := admitBareIdent(m[2]); qualified {
			drop[m[2]] = true
		}
	}
	if len(drop) == 0 {
		return paths
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !drop[p] {
			out = append(out, p)
		}
	}
	return out
}

// admitClauses splits text on a newline, a semicolon, or a full stop followed by a blank.
func admitClauses(text string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n', ';':
			flush()
		case '.':
			if i+1 < len(text) && text[i+1] == ' ' {
				flush()
				i++
				continue
			}
			b.WriteByte('.')
		default:
			b.WriteByte(text[i])
		}
	}
	flush()
	return out
}

// admitPathNear is the path in paths closest in clause to the identifier words.
func admitPathNear(clause, ident string, paths []string) string {
	at := strings.Index(clause, ident)
	best, bestD := paths[0], len(clause)+1
	for _, p := range paths {
		i := strings.Index(clause, p)
		if i < 0 {
			continue
		}
		d := i - at
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = p, d
		}
	}
	return best
}

// nearestFile is the file in files closest to want: the longer shared prefix, then fewer
// unmatched segments, then the name earlier in lexical order. "" when files is empty.
func nearestFile(files []string, want string) string {
	best := ""
	bestC, bestU := -1, 0
	for _, f := range files {
		c, u := pathParts(want, f)
		if best == "" || c > bestC || (c == bestC && u < bestU) || (c == bestC && u == bestU && f < best) {
			best, bestC, bestU = f, c, u
		}
	}
	return best
}

// pathParts is the shared prefix length of a and b, and the unmatched segments after it.
func pathParts(a, b string) (common, unmatched int) {
	as, bs := strings.Split(admitClean(a), "/"), strings.Split(admitClean(b), "/")
	i := 0
	for i < len(as) && i < len(bs) && as[i] == bs[i] {
		i++
	}
	return i, len(as) - i + len(bs) - i
}
