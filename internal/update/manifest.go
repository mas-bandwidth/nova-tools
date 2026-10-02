// Package update implements the shared, explicit version inventory behind
// nova-update and nova-version. It never discovers homes or installs on a verdict.
package update

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

const Header = "name\tkind\tinstalled\tlatest\tapply\towner"

// tabbed is a tab-separated header as a reader copies it from a line: each tab
// spelled <TAB>, the way the banner spells it, never escaped as \x09.
func tabbed(header string) string { return strings.ReplaceAll(header, "\t", "<TAB>") }

var Kinds = []string{"harness", "engine", "model", "tool", "pin"}

type Entry struct {
	Name, Kind, Latest, Owner string
	Installed, Apply          []string
}

func kindValid(s string) bool {
	for _, k := range Kinds {
		if k == s {
			return true
		}
	}
	return false
}
func argv(s string) ([]string, error) {
	if s == "" || strings.TrimSpace(s) != s || strings.Contains(s, "  ") || strings.ContainsAny(s, "\r\n\t\"'") {
		return nil, fmt.Errorf("argv requires single spaces and no quoting (put arguments needing spaces in a script and name the script)")
	}
	return strings.Split(s, " "), nil
}

// ManifestError is every problem one read of a manifest found, in line order, each one
// worded as a single-problem refusal was (`line 3: ...`): a run that finds several says so
// once, and the writer fixes the file in one pass and not one error per run.
type ManifestError struct {
	Problems []string
	// More is how many problems past manifestProblemCap were found and not listed.
	More int
}

func (e *ManifestError) Error() string {
	out := strings.Join(e.Problems, "; ")
	if e.More > 0 {
		out += fmt.Sprintf("; and %d more (fix these and run again)", e.More)
	}
	return out
}

// manifestProblemCap bounds the problems one refusal lists; the rest are counted.
const manifestProblemCap = 50

// Load reads the manifest (SPEC-UPDATE rule 2) and returns every problem it finds as one
// *ManifestError: the header, then each line's fields in order, every bad field of a line
// named and every bad line of the file. A file with no problem yields its entries.
func Load(r io.Reader) ([]Entry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	var problems []string
	add := func(line int, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("line %d: ", line)+fmt.Sprintf(format, args...))
	}
	if !sc.Scan() || sc.Text() != Header {
		add(1, "invalid header (put the header back exactly: %s)", tabbed(Header))
	}
	var out []Entry
	seen := map[string]bool{}
	line := 1
	for sc.Scan() {
		line++
		s := sc.Text()
		if strings.HasPrefix(s, "#") {
			continue
		}
		f := strings.Split(s, "\t")
		if len(f) != 6 {
			add(line, "%d fields, want 6 (use the six-column TSV header)", len(f))
			continue
		}
		before := len(problems)
		for i, v := range f {
			if v == "" {
				add(line, "empty %s field (supply all six fields; apply may be none)", strings.Split(Header, "\t")[i])
			}
		}
		if len(problems) > before {
			continue
		}
		if !kindValid(f[1]) {
			add(line, "unknown kind %s (use harness,engine,model,tool,pin)", f[1])
		}
		if seen[f[0]] {
			add(line, "duplicate name %s (give each entry a distinct name)", f[0])
		}
		seen[f[0]] = true
		e := Entry{Name: f[0], Kind: f[1], Latest: f[3], Owner: f[5]}
		var err error
		e.Installed, err = argv(f[2])
		if err != nil {
			add(line, "installed: %v", err)
		}
		// "-" is the field a snapshot leaves for a person to fill in, and it is the same
		// answer as "none": there is no command to apply an update with. It is not a
		// command named "-" (#571).
		if f[4] != "none" && f[4] != "-" {
			e.Apply, err = argv(f[4])
			if err != nil {
				add(line, "apply: %v", err)
			}
		}
		// A latest of "-" is NOT YET KNOWN, which is what a snapshot writes and what a
		// person promotes to a source by hand. It is read, reported as unknown, and never
		// guessed at; every other value is still a <scheme>:<locator> or a refusal.
		if e.Latest == "-" {
			out = append(out, e)
			continue
		}
		scheme, loc, ok := strings.Cut(e.Latest, ":")
		if !ok || loc == "" {
			add(line, "invalid latest (use github,npm,brew,ollama or local with a locator)")
			continue
		}
		switch scheme {
		case "github", "npm", "brew", "ollama":
		case "local":
			if _, err = argv(loc); err != nil {
				add(line, "latest: %v", err)
			}
		default:
			add(line, "unknown source %s (use github,npm,brew,ollama,local)", scheme)
			continue
		}
		if e.Kind == "pin" && scheme != "local" {
			add(line, "pin requires local source (name the depended-on version argv)")
		}
		if scheme == "ollama" && e.Kind != "model" {
			add(line, "ollama digest requires model kind (set kind=model)")
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		add(line, "unreadable manifest (use lines below 1 MiB)")
	}
	if len(problems) > 0 {
		me := &ManifestError{Problems: problems}
		if len(problems) > manifestProblemCap {
			me.Problems, me.More = problems[:manifestProblemCap], len(problems)-manifestProblemCap
		}
		return nil, me
	}
	return out, nil
}
