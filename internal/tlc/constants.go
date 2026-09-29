package tlc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A configuration that leaves a declared constant unassigned cannot run: TLC
// stops before it explores a state. CheckConstants finds that in the files,
// without java, so a commit of a model that cannot run fails at once.

var (
	blockCommentRE = regexp.MustCompile(`(?s)\(\*.*?\*\)`)
	lineCommentRE  = regexp.MustCompile(`\\\*[^\n]*`)
	extendsRE      = regexp.MustCompile(`(?m)^\s*EXTENDS\s+([^\n]+)`)
	constantsRE    = regexp.MustCompile(`(?m)^\s*CONSTANTS?\b`)
	identRE        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
	cfgKeywordRE   = regexp.MustCompile(`^(SPECIFICATION|INIT|NEXT|INVARIANTS?|PROPERTY|PROPERTIES|SYMMETRY|CONSTRAINTS?|ACTION_CONSTRAINTS?|VIEW|CHECK_DEADLOCK|POSTCONDITION|ALIAS|CONSTANTS?)\b`)
	cfgAssignRE    = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*(=|<-)`)
)

func stripComments(text string) string {
	return lineCommentRE.ReplaceAllString(blockCommentRE.ReplaceAllString(text, " "), "")
}

// declaredConstants returns the constants a module and the modules of the
// directory it extends declare.
func declaredConstants(dir, module string, seen map[string]bool) ([]string, error) {
	if seen[module] {
		return nil, nil
	}
	seen[module] = true
	raw, err := os.ReadFile(filepath.Join(dir, module+".tla"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // a standard module (Naturals, TLC, ...)
		}
		return nil, err
	}
	text := stripComments(string(raw))
	var out []string
	for _, m := range extendsRE.FindAllStringSubmatch(text, -1) {
		for _, name := range strings.Split(m[1], ",") {
			more, err := declaredConstants(dir, strings.TrimSpace(name), seen)
			if err != nil {
				return nil, err
			}
			out = append(out, more...)
		}
	}
	for _, loc := range constantsRE.FindAllStringIndex(text, -1) {
		rest := text[loc[1]:]
		for {
			rest = strings.TrimLeft(rest, " \t\r\n")
			name := identRE.FindString(rest)
			if name == "" {
				break
			}
			out = append(out, name)
			rest = rest[len(name):]
			if strings.HasPrefix(strings.TrimLeft(rest, " \t"), "(") { // an operator constant F(_, _)
				if i := strings.Index(rest, ")"); i >= 0 {
					rest = rest[i+1:]
				}
			}
			trimmed := strings.TrimLeft(rest, " \t\r\n")
			if !strings.HasPrefix(trimmed, ",") {
				break
			}
			rest = trimmed[1:]
		}
	}
	return out, nil
}

// assignedConstants returns the names a configuration's CONSTANT sections
// assign, by = or <-.
func assignedConstants(cfg string) map[string]bool {
	out := map[string]bool{}
	in := false
	for _, line := range strings.Split(stripComments(cfg), "\n") {
		t := strings.TrimSpace(line)
		if kw := cfgKeywordRE.FindString(t); kw != "" {
			in = strings.HasPrefix(kw, "CONSTANT")
			t = strings.TrimSpace(t[len(kw):])
		}
		if !in {
			continue
		}
		for _, m := range cfgAssignRE.FindAllStringSubmatch(t, -1) {
			out[m[1]] = true
		}
	}
	return out
}

// CheckConstants refuses a case whose configuration leaves a constant of its
// module unassigned, naming the configuration and the constants.
func CheckConstants(dir string, cases []Case) error {
	var problems []string
	for _, c := range cases {
		declared, err := declaredConstants(dir, strings.TrimSuffix(c.Module, ".tla"), map[string]bool{})
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(dir, c.Config))
		if err != nil {
			return fmt.Errorf("cannot read %s: %v", c.Config, err)
		}
		assigned := assignedConstants(string(raw))
		var missing []string
		for _, name := range declared {
			if !assigned[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			problems = append(problems, fmt.Sprintf("%s leaves %s unassigned", c.Config, strings.Join(missing, ", ")))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("a configuration cannot run: %s", strings.Join(problems, "; "))
	}
	return nil
}
