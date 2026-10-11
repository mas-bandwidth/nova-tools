package friend

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// CodexWritableRoots is the setting in CODEX_HOME/config.toml that lets a
// workspace-write sandbox write the friend's directory:
// [sandbox_workspace_write] writable_roots. The friend's directory is
// written there by its real path; a symlink there took no writes for ten
// hours on 2026-10-05.
const CodexWritableRoots = "sandbox_workspace_write.writable_roots"

// codexSettings: the config file is not a symlink, and writable_roots holds
// the friend's directory. The file is edited by line, so every comment and
// every other key stays as the person wrote it.
func codexSettings(h HarnessSettings) ([]setting, error) {
	file := filepath.Join(h.homeDir(h.CodexHome, ".codex"), "config.toml")
	return []setting{h.realFile(file), {
		Setting: Setting{Harness: h.Harness, File: file, Name: CodexWritableRoots, Want: h.Dir},
		read: func() (string, error) {
			text, err := h.readOrEmpty(file)
			if err != nil {
				return "", err
			}
			roots, _, err := CodexRoots(text)
			if err != nil {
				return "", fmt.Errorf("%s: %w", file, err)
			}
			if slices.Contains(roots, h.Dir) {
				return h.Dir, nil
			}
			return "[" + strings.Join(roots, ", ") + "]", nil
		},
		write: func() error {
			text, err := h.readOrEmpty(file)
			if err != nil {
				return err
			}
			out, err := CodexAddRoot(text, h.Dir)
			if err != nil {
				return err
			}
			return h.writeConfig(file, out, 0o600)
		},
	}}, nil
}

var (
	tomlTable   = regexp.MustCompile(`^\s*\[\[?([^\[\]]+)\]\]?\s*(#.*)?$`) // a [table] or an [[array of tables]]
	codexKey    = regexp.MustCompile(`^\s*writable_roots\s*=`)
	codexDotted = regexp.MustCompile(`^\s*sandbox_workspace_write\.writable_roots\s*=`)
	tomlString  = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"|'([^']*)'`)
)

// codexSpan is where writable_roots sits in the file's lines: the first and
// last line of its value (an array may span lines), and the line of the
// [sandbox_workspace_write] header (-1 none).
type codexSpan struct{ first, last, table int }

func codexFind(lines []string) (codexSpan, bool, error) {
	span := codexSpan{first: -1, table: -1}
	table := ""
	for i, l := range lines {
		if m := tomlTable.FindStringSubmatch(l); m != nil {
			table = strings.TrimSpace(m[1])
			if table == "sandbox_workspace_write" {
				span.table = i
			}
			continue
		}
		if (table == "sandbox_workspace_write" && codexKey.MatchString(l)) || (table == "" && codexDotted.MatchString(l)) {
			span.first = i
			for j := i; j < len(lines); j++ {
				if strings.Contains(stripTOMLComment(lines[j]), "]") {
					span.last = j
					return span, true, nil
				}
			}
			return span, false, fmt.Errorf("%s at line %d has no closing ]", CodexWritableRoots, i+1)
		}
	}
	return span, false, nil
}

// stripTOMLComment is the line without a # comment outside a string.
func stripTOMLComment(l string) string {
	quote := byte(0)
	for i := 0; i < len(l); i++ {
		switch c := l[i]; {
		case quote != 0 && c == '\\' && quote == '"':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && c == '#':
			return l[:i]
		}
	}
	return l
}

// CodexRoots is writable_roots in a Codex config.toml's text; found is false
// when the key is absent.
func CodexRoots(text string) (roots []string, found bool, err error) {
	lines := strings.Split(text, "\n")
	span, found, err := codexFind(lines)
	if err != nil || !found {
		return nil, false, err
	}
	if span.first < 0 || span.last < span.first || span.last >= len(lines) {
		return nil, false, fmt.Errorf("%s: lines %d to %d are outside the file's %d", CodexWritableRoots, span.first+1, span.last+1, len(lines))
	}
	var value strings.Builder
	for _, l := range lines[span.first : span.last+1] {
		value.WriteString(stripTOMLComment(l))
	}
	for _, m := range tomlString.FindAllStringSubmatch(value.String(), -1) {
		if strings.HasPrefix(m[0], "'") { // a literal string: no escapes
			roots = append(roots, m[2])
			continue
		}
		roots = append(roots, strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(m[1]))
	}
	return roots, true, nil
}

// CodexAddRoot is the text with root added to writable_roots: the key's
// lines replaced by one line holding every root there and root, the key
// added under its table when absent, the table appended when absent.
func CodexAddRoot(text, root string) (string, error) {
	roots, found, err := CodexRoots(text)
	if err != nil {
		return "", err
	}
	if slices.Contains(roots, root) {
		return text, nil
	}
	quoted := make([]string, 0, len(roots)+1)
	for _, r := range append(roots, root) {
		quoted = append(quoted, `"`+strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(r)+`"`)
	}
	value := "[" + strings.Join(quoted, ", ") + "]"
	lines := strings.Split(text, "\n")
	span, _, _ := codexFind(lines) // ignored: CodexRoots above read the same lines without an error
	switch {
	case found:
		key := "writable_roots"
		if span.table < 0 || span.first < span.table {
			key = CodexWritableRoots
		}
		line := key + " = " + value
		lines = slices.Replace(lines, span.first, span.last+1, line)
	case span.table >= 0:
		lines = slices.Insert(lines, span.table+1, "writable_roots = "+value)
	default:
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "[sandbox_workspace_write]", "writable_roots = "+value, "")
	}
	return strings.Join(lines, "\n"), nil
}
