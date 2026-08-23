package check

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// codeExtsData is the floor deny-list, kept as data a reader can open and diff
// rather than as string literals inside a walk. See codeexts.txt for why it is
// embedded rather than demanded from the caller.
//
//go:embed codeexts.txt
var codeExtsData string

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
	Staged     []string // if StagedSet, classify exactly these repo-relative paths
	StagedSet  bool     // distinguishes "not staged mode" from "staged, nothing staged"

	// StagedMayBeQuoted says the path list came from a newline-separated git
	// invocation, where core.quotePath may have wrapped names in quotes and
	// octal escapes. Under -z git never quotes, and decoding there would
	// corrupt a filename that legitimately begins and ends with a quote.
	StagedMayBeQuoted bool
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
	if len(denySet) == 0 {
		return 0, nil, errors.New("effective deny-list is empty: refusing rather than passing everything")
	}

	allow := normalizeAllow(opts.Allow)

	info, statErr := os.Stat(opts.Dir)
	if statErr != nil {
		return 0, nil, fmt.Errorf("dir: %w", statErr)
	}
	if !info.IsDir() {
		return 0, nil, fmt.Errorf("dir %q is not a directory", opts.Dir)
	}

	if opts.StagedSet {
		return noCodeStaged(opts, allow, denySet, source)
	}

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
			return nil // devices, sockets, fifos: not repo content
		}
		scanned++
		if reasons := classify(path, rel, fi, isLink, denySet, source); len(reasons) > 0 {
			findings = append(findings, Failure{rel, strings.Join(reasons, "; ")})
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return scanned, findings, nil
}

// noCodeStaged classifies an explicit path list instead of walking the tree.
//
// The distinction is deliberate: the gate refuses what is being ADDED, not
// what already exists. Gating the whole tree at commit time would make the
// next commit hostage to deleting every finished investigation in the repo,
// which is how a reasonable rule becomes a rule everybody disables.
//
// A path that no longer exists on disk — a staged deletion — is skipped
// rather than reported: taking machinery back out of the self is the
// direction this gate wants. EVERY OTHER Lstat failure is a finding. Those
// two cases look identical to the caller and are not the same thing: a
// quoted, mis-encoded, or unreachable path that is silently skipped turns
// this gate into one that reports a clean commit having classified nothing.
func noCodeStaged(opts NoCodeOptions, allow []string, denySet map[string]bool, source string) (int, []Failure, error) {
	var findings []Failure
	scanned := 0
	for _, raw := range opts.Staged {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		rel, err := gitPath(raw, opts.StagedMayBeQuoted)
		if err != nil {
			scanned++
			findings = append(findings, Failure{raw, err.Error()})
			continue
		}
		if rel == "." {
			continue
		}
		// Check the CLEANED path: sub/../../x escapes without a leading "../".
		if clean := filepath.ToSlash(filepath.Clean(rel)); clean == ".." || strings.HasPrefix(clean, "../") {
			scanned++
			findings = append(findings, Failure{rel, "path escapes the tree being guarded; is --dir the repository root?"})
			continue
		}
		if isAllowed(rel, allow) {
			continue
		}
		full := filepath.Join(opts.Dir, filepath.FromSlash(rel))
		fi, err := os.Lstat(full)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // staged deletion, or gone: not an accumulation
			}
			scanned++
			findings = append(findings, Failure{rel, "unreadable: " + err.Error() + " (cannot rule out machinery)"})
			continue
		}
		isLink := fi.Mode()&os.ModeSymlink != 0
		if fi.IsDir() {
			// Git stages a directory only as a gitlink: a whole nested
			// repository. That is the loudest possible violation of "prose,
			// not machinery" and it must never pass as unclassifiable.
			scanned++
			findings = append(findings, Failure{rel, "directory staged: a submodule or gitlink is an entire repository"})
			continue
		}
		if !isLink && !fi.Mode().IsRegular() {
			// FAIL CLOSED. Every earlier hole in this mode was a "cannot
			// classify this, so skip it" branch; there is now exactly one
			// thing a staged path may be skipped for, and it is a deletion.
			scanned++
			findings = append(findings, Failure{rel, fmt.Sprintf("not a regular file (mode %v): cannot rule out machinery", fi.Mode().Type())})
			continue
		}
		scanned++
		if reasons := classify(full, rel, fi, isLink, denySet, source); len(reasons) > 0 {
			findings = append(findings, Failure{rel, strings.Join(reasons, "; ")})
		}
	}
	return scanned, findings, nil
}

// gitPath normalizes one path as git reports it.
//
// With core.quotePath on — the DEFAULT — `git diff --cached --name-only`
// wraps any path containing non-ASCII or special bytes in double quotes with
// C-style octal escapes. Taken literally such a path matches nothing on disk,
// which is why this is decoded rather than trimmed: the alternative is a gate
// that silently ignores every file whose name is not plain ASCII.
func gitPath(raw string, mayBeQuoted bool) (string, error) {
	s := raw
	if mayBeQuoted && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2 {
		unquoted, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("cannot decode git-quoted path (try -z, or -c core.quotePath=false): %v", err)
		}
		s = unquoted
	}
	s = filepath.ToSlash(s)
	s = strings.TrimPrefix(s, "./")
	return s, nil
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
// answer is given in both the walk and the staged path — an --allow value that
// behaved differently in the commit gate than in the audit would be a gate
// disagreeing with its own check.
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
func classify(fullPath, rel string, fi os.FileInfo, isLink bool, denySet map[string]bool, source string) []string {
	var reasons []string
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
		if errors.Is(err, fs.ErrClosed) || errors.Is(err, fs.ErrPermission) {
			return false, err
		}
		return false, nil // a file shorter than two bytes holds no shebang
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
