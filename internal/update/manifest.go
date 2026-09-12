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
func Load(r io.Reader) ([]Entry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() || sc.Text() != Header {
		return nil, fmt.Errorf("line 1: invalid header (put the header back exactly: %s)", Header)
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
			return nil, fmt.Errorf("line %d: %d fields, want 6 (use the six-column TSV header)", line, len(f))
		}
		for _, v := range f {
			if v == "" {
				return nil, fmt.Errorf("line %d: empty field (supply all six fields; apply may be none)", line)
			}
		}
		if !kindValid(f[1]) {
			return nil, fmt.Errorf("line %d: unknown kind %s (use harness,engine,model,tool,pin)", line, f[1])
		}
		if seen[f[0]] {
			return nil, fmt.Errorf("line %d: duplicate name %s (give each entry a distinct name)", line, f[0])
		}
		seen[f[0]] = true
		e := Entry{Name: f[0], Kind: f[1], Latest: f[3], Owner: f[5]}
		var err error
		e.Installed, err = argv(f[2])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if f[4] != "none" {
			e.Apply, err = argv(f[4])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
		}
		scheme, loc, ok := strings.Cut(e.Latest, ":")
		if !ok || loc == "" {
			return nil, fmt.Errorf("line %d: invalid latest (use github,npm,brew,ollama or local with a locator)", line)
		}
		switch scheme {
		case "github", "npm", "brew", "ollama":
		case "local":
			if _, err = argv(loc); err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
		default:
			return nil, fmt.Errorf("line %d: unknown source %s (use github,npm,brew,ollama,local)", line, scheme)
		}
		if e.Kind == "pin" && scheme != "local" {
			return nil, fmt.Errorf("line %d: pin requires local source (name the depended-on version argv)", line)
		}
		if scheme == "ollama" && e.Kind != "model" {
			return nil, fmt.Errorf("line %d: ollama digest requires model kind (set kind=model)", line)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: unreadable manifest (use lines below 1 MiB)", line)
	}
	return out, nil
}
