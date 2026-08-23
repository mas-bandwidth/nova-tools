package check

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// codeExtsData is the floor deny-list, kept as data a reader can open and diff
// rather than as string literals inside a walk. See codeexts.txt for why it is
// embedded rather than demanded from the caller.
//
//go:embed codeexts.txt
var codeExtsData string

// codeNamesData is the floor NAME list: build and orchestration machinery
// identified by exact file name or by repo-relative location rather than by
// extension. It is a separate list because it answers a different question —
// an extension denotes a language, while a Makefile is machinery because make
// runs it, carrying no extension, no shebang and no executable bit. See
// codenames.txt for why .yml is not simply added to the extension list.
//
//go:embed codenames.txt
var codeNamesData string

// Deny-list provenance, reported with every finding so that neither a red nor
// a green hides the basis it was reached on.
const (
	DenyFloor    = "floor list"
	DenyReplaced = "--deny-ext"
	DenyExtended = "floor list + --deny-ext-add"
)

// FloorDenyExts returns the embedded floor deny-list, sorted.
//
// It returns an error rather than an empty list if the data is unreadable or
// empty: a guard that cannot determine what it forbids must refuse, not pass.
func FloorDenyExts() ([]string, error) {
	exts, err := parseExtLines(codeExtsData)
	if err != nil {
		return nil, fmt.Errorf("floor deny-list: %w", err)
	}
	if len(exts) == 0 {
		return nil, errors.New("floor deny-list is empty: the embedded list did not parse")
	}
	return exts, nil
}

// FloorDenyNames returns the embedded floor name list as an exact-basename set
// and an ordered list of path prefixes.
//
// It returns an error rather than empty results if the data is unreadable or
// empty, for the same reason FloorDenyExts does: a guard that cannot determine
// what it forbids must refuse, not pass.
func FloorDenyNames() (names map[string]bool, prefixes []string, err error) {
	names, prefixes, err = parseNameLines(codeNamesData)
	if err != nil {
		return nil, nil, fmt.Errorf("floor name list: %w", err)
	}
	if len(names) == 0 && len(prefixes) == 0 {
		return nil, nil, errors.New("floor name list is empty: the embedded data did not parse")
	}
	return names, prefixes, nil
}

// parseNameLines reads "name:<basename>" and "path:<prefix>/" entries.
//
// An unprefixed or unrecognized entry is an ERROR rather than a silently
// ignored line. A typo'd "nmae:Makefile" that parsed as nothing would leave a
// list that matches less than it says while still reporting a clean tree,
// which is the fail-open shape this whole check exists against.
func parseNameLines(s string) (map[string]bool, []string, error) {
	names := map[string]bool{}
	seenPrefix := map[string]bool{}
	var prefixes []string
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		line = strings.ToLower(line)
		switch {
		case strings.HasPrefix(line, "name:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			if err := validName(v, line); err != nil {
				return nil, nil, err
			}
			names[v] = true
		case strings.HasPrefix(line, "path:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "path:"))
			v = strings.Trim(filepath.ToSlash(v), "/")
			if err := validPrefix(v, line); err != nil {
				return nil, nil, err
			}
			if !seenPrefix[v] {
				seenPrefix[v] = true
				prefixes = append(prefixes, v)
			}
		default:
			return nil, nil, fmt.Errorf("floor name list: %q has no name: or path: prefix", line)
		}
	}
	sort.Strings(prefixes)
	return names, prefixes, nil
}

// validName and validPrefix hold the name list to the SAME standard validExt
// holds the extension list to, and they exist because the first version of this
// parser did not. It accepted globs, embedded whitespace, zero-width spaces and
// a leading "./" — every one of which builds an entry that matches nothing,
// survives the emptiness guard because other entries are fine, prints happily
// in --print-deny-list, and leaves a floor quietly forbidding less than it
// says. That is the exact fail-open this check exists against, written into the
// commit whose own argument condemns it. Found at the gate.
func validName(v, line string) error {
	switch {
	case v == "":
		return fmt.Errorf("floor name list: %q has an empty name", line)
	case strings.ContainsAny(v, "/\\"):
		return fmt.Errorf("floor name list: %q is a path, not a bare file name; use path: for a location", line)
	case strings.ContainsAny(v, "*?[]"):
		return fmt.Errorf("floor name list: %q looks like a glob; names are matched literally", line)
	case strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) >= 0:
		return fmt.Errorf("floor name list: %q contains whitespace or a non-printable character", line)
	}
	return nil
}

func validPrefix(v, line string) error {
	switch {
	case v == "":
		return fmt.Errorf("floor name list: %q has an empty path prefix", line)
	case strings.ContainsAny(v, "*?[]"):
		return fmt.Errorf("floor name list: %q looks like a glob; prefixes are matched literally", line)
	case strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) >= 0:
		return fmt.Errorf("floor name list: %q contains whitespace or a non-printable character", line)
	}
	// "." and ".." segments would each build a prefix that never matches a
	// cleaned relative path, so they are refused rather than normalized away:
	// a silent normalization hides which entry the author actually wrote.
	for _, seg := range strings.Split(v, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("floor name list: %q is not a clean path prefix (empty, . or .. segment)", line)
		}
	}
	return nil
}

// parseExtLines reads one extension per line, ignoring blanks and # comments.
//
// An entry that cannot be an extension is an ERROR, not a silently useless
// list member: "--deny-ext mylist.txt" (the missing @) would otherwise build a
// one-entry list of ".mylist.txt", match nothing, pass the emptiness guard,
// and report a clean tree. A guard that forbids nothing must refuse.
func parseExtLines(s string) ([]string, error) {
	seen := map[string]bool{}
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		line = strings.ToLower(line)
		if !strings.HasPrefix(line, ".") {
			line = "." + line
		}
		if err := validExt(line); err != nil {
			return nil, err
		}
		seen[line] = true
	}
	return sortedKeys(seen), nil
}

// validExt rejects anything that is not a bare file extension. The rejected
// shapes are the likely user errors, and each of them would otherwise produce
// a deny-list that matches nothing while reporting success.
func validExt(e string) error {
	body := strings.TrimPrefix(e, ".")
	switch {
	case body == "":
		return fmt.Errorf("deny-list entry %q is not an extension", e)
	case strings.ContainsAny(e, "/\\"):
		return fmt.Errorf("deny-list entry %q looks like a path; did you mean @%s?", e, body)
	case strings.ContainsAny(e, "*?[]"):
		return fmt.Errorf("deny-list entry %q looks like a glob; extensions are matched literally", e)
	case strings.IndexFunc(e, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) >= 0:
		// Not just " " and "\t": a zero-width space builds an entry that
		// matches nothing and would be accepted as a one-entry deny-list.
		return fmt.Errorf("deny-list entry %q contains whitespace or a non-printable character", e)
	case strings.Contains(body, "."):
		return fmt.Errorf("deny-list entry %q has more than one dot; did you mean @%s?", e, body)
	}
	return nil
}

// ParseDenyList reads a deny-list specification: either a comma-separated list
// of extensions, or "@path" naming a file with one extension per line.
func ParseDenyList(spec string) ([]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, errors.New("empty deny-list specification")
	}
	if strings.HasPrefix(spec, "@") {
		path := strings.TrimPrefix(spec, "@")
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("deny-list file: %w", err)
		}
		exts, err := parseExtLines(string(b))
		if err != nil {
			return nil, fmt.Errorf("deny-list file %q: %w", path, err)
		}
		if len(exts) == 0 {
			return nil, fmt.Errorf("deny-list file %q contains no extensions", path)
		}
		return exts, nil
	}
	exts, err := parseExtLines(strings.ReplaceAll(spec, ",", "\n"))
	if err != nil {
		return nil, err
	}
	if len(exts) == 0 {
		return nil, fmt.Errorf("deny-list %q contains no extensions", spec)
	}
	return exts, nil
}

// NoCodeOptions configures the self/machinery separation check.
//
// Allow starts EMPTY and stays empty unless the caller narrows scope. Every
// scope narrowing is the caller's, stated per run — the same law the skip and
// exempt lists elsewhere in this repo obey. The deny-list runs the other way:
// it is a floor that ships with the tool, because a narrowing that goes
// missing fails open and a floor that ships cannot.
type NoCodeOptions struct {
	Dir        string   // root of the tree being guarded (required)
	Allow      []string // path prefixes where machinery may live; empty by default
	DenyExt    []string // effective deny-list; empty means the floor list
	DenySource string   // provenance; required when DenyExt is set
}

// NoCode reports every file that is machinery living inside a prose-only tree.
//
// A file is flagged when its extension is on the effective deny-list, when it
// carries an executable bit, or when it begins with a shebang. The three catch
// different things: the extension is the auditable common case, the mode bit
// catches a chmod +x on anything at all, and the shebang is the tell that
// survives renaming — a script with no extension is still a script.
//
// A symlink is never dereferenced — a gate that can be walked out of the tree
// it guards is not a gate — but its own NAME is still classified, because a
// link called run.sh is machinery by the same argument that catches a file
// called run.sh, and reading its name requires no dereference.
//
// A file that cannot be read produces a finding rather than a pass. Making a
// file less readable must not make this gate greener.
func NoCode(opts NoCodeOptions) (scanned int, findings []Failure, err error) {
	deny := opts.DenyExt
	source := opts.DenySource
	if len(deny) == 0 {
		// No list supplied means the floor, and the floor is what gets
		// reported: a finding may never name a list that did not produce it.
		deny, err = FloorDenyExts()
		if err != nil {
			return 0, nil, err
		}
		source = DenyFloor
	} else if source == "" {
		return 0, nil, errors.New("DenyExt was set without DenySource: a finding may not name an unknown list")
	}
	denySet := make(map[string]bool, len(deny))
	for _, e := range deny {
		denySet[strings.ToLower(e)] = true
	}

	// The name and path floors are always the embedded floor, and deliberately
	// are NOT replaced by --deny-ext. That flag answers "which LANGUAGES does
	// this line legitimately keep inside its own self", which has nothing to
	// say about whether a CI workflow belongs in a prose tree. A line that
	// genuinely keeps build machinery declares WHERE with --allow, which is the
	// existing escape hatch and is narrower than switching a floor off.
	denyNames, denyPrefixes, err := FloorDenyNames()
	if err != nil {
		return 0, nil, err
	}

	allow := normalizeAllow(opts.Allow)

	// Resolve the root before walking. os.Stat FOLLOWS a symlink, so a --dir
	// naming a link to the repo passed the directory check and then handed
	// WalkDir a root it saw as a single non-directory entry — a clean pass
	// over a tree never opened. On this platform /var is such a link.
	root, statErr := filepath.EvalSymlinks(opts.Dir)
	if statErr != nil {
		return 0, nil, fmt.Errorf("dir: %w", statErr)
	}
	info, statErr := os.Stat(root)
	if statErr != nil {
		return 0, nil, fmt.Errorf("dir: %w", statErr)
	}
	if !info.IsDir() {
		return 0, nil, fmt.Errorf("dir %q is not a directory", opts.Dir)
	}
	opts.Dir = root

	err = filepath.WalkDir(opts.Dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(opts.Dir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			// .git is machinery by construction and is not repo content.
			if d.Name() == ".git" || isAllowed(rel, allow) {
				return filepath.SkipDir
			}
			return nil
		}
		if isAllowed(rel, allow) {
			return nil
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			scanned++
			findings = append(findings, Failure{rel, "unreadable: " + infoErr.Error() + " (cannot rule out machinery)"})
			return nil
		}
		isLink := fi.Mode()&os.ModeSymlink != 0
		if !isLink && !fi.Mode().IsRegular() {
			// FAIL CLOSED. A file that cannot be classified is a finding,
			// never a pass: a fifo named pipe.sh is not prose and cannot be
			// read, which is exactly what this check exists to refuse.
			scanned++
			findings = append(findings, Failure{rel, fmt.Sprintf("not a regular file (mode %v): cannot rule out machinery", fi.Mode().Type())})
			return nil
		}
		scanned++
		if reasons := classify(path, rel, fi, isLink, denySet, source, denyNames, denyPrefixes); len(reasons) > 0 {
			findings = append(findings, Failure{rel, strings.Join(reasons, "; ")})
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return scanned, findings, nil
}

// normalizeAllow trims each entry to a bare repo-relative directory prefix.
func normalizeAllow(allow []string) []string {
	out := make([]string, 0, len(allow))
	for _, a := range allow {
		a = filepath.ToSlash(strings.TrimSpace(a))
		a = strings.TrimPrefix(a, "./")
		a = strings.Trim(a, "/")
		if a != "" && a != "." {
			out = append(out, a)
		}
	}
	return out
}

// isAllowed reports whether rel is, or lies beneath, a declared allow prefix.
// A prefix genuinely covers everything beneath it, at any depth, and the same
// prefix covers everything beneath it at any depth.
func isAllowed(rel string, allow []string) bool {
	for _, a := range allow {
		if rel == a || strings.HasPrefix(rel, a+"/") {
			return true
		}
	}
	return false
}

// classify returns every reason the file is machinery, or nil if it is prose.
// All reasons are reported when more than one holds: a gate that says only
// "no" teaches nothing, and each reason is separately actionable.
func classify(fullPath, rel string, fi os.FileInfo, isLink bool, denySet map[string]bool, source string, denyNames map[string]bool, denyPrefixes []string) []string {
	var reasons []string
	// Name and location are checked FIRST, and before the symlink return
	// below, because both read only the path. A symlink called Makefile is
	// machinery by the same argument that catches a file called Makefile, and
	// deciding that requires no dereference.
	if base := strings.TrimSpace(strings.ToLower(filepath.Base(rel))); denyNames[base] {
		reasons = append(reasons, fmt.Sprintf("build machinery by name %s (floor name list)", base))
	}
	for _, pre := range denyPrefixes {
		if rel == pre || strings.HasPrefix(rel, pre+"/") {
			reasons = append(reasons, fmt.Sprintf("machinery by location %s/ (floor name list)", pre))
			break
		}
	}
	// Trailing whitespace is trimmed for MATCHING only: a file named "x.py "
	// has extension ".py " by Go's reckoning and would otherwise miss the list
	// while being every bit as much a script.
	if ext := strings.TrimSpace(strings.ToLower(filepath.Ext(rel))); denySet[ext] {
		reasons = append(reasons, fmt.Sprintf("code extension %s (%s)", ext, source))
	}
	if isLink {
		// Never dereferenced: the name is classified, the target is not read
		// and its mode is not consulted.
		if len(reasons) > 0 {
			reasons = append(reasons, "symlink (target not followed)")
		}
		return reasons
	}
	if fi.Mode().Perm()&0o111 != 0 {
		reasons = append(reasons, fmt.Sprintf("executable (mode %04o)", fi.Mode().Perm()))
	}
	shebang, err := hasShebang(fullPath)
	switch {
	case err != nil:
		reasons = append(reasons, "unreadable: "+err.Error()+" (cannot rule out machinery)")
	case shebang:
		reasons = append(reasons, "executable script (shebang)")
	}
	return reasons
}

// hasShebang reports whether a file begins with "#!".
//
// An unreadable file returns an error rather than false. Reporting "no
// shebang" for a file nobody could open would mean a chmod 000 makes this
// gate greener, which is the fail-open shape this whole check exists against.
func hasShebang(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	b, err := bufio.NewReader(f).Peek(2)
	if err != nil {
		// Short file: genuinely no shebang. Anything else is a read that
		// failed, and scoring that as "no shebang" is the fail-open shape.
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return string(b) == "#!", nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
