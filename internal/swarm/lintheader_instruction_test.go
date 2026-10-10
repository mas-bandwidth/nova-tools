package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLintNamesTheInstructionKindOfEveryCard is the card lint's instruction
// kind (docs/SPEC-ISA.md): one column of internal/hygiene/kinds.txt, named on
// every declared KIND, refused when the row has none, and the only table of
// kinds in the tree.
func TestLintNamesTheInstructionKindOfEveryCard(t *testing.T) {
	t.Parallel()

	spec := specInstructionKinds(t)
	require.NotEmpty(t, spec, "docs/SPEC-ISA.md names no work kind on an instruction")
	column := kindsTxtInstructionColumn(t)
	require.Len(t, column, len(hygiene.Kinds()), "every row of kinds.txt names an instruction kind")
	for _, kind := range hygiene.Kinds() {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			want, ok := hygiene.InstructionKind(kind)
			require.True(t, ok, "KIND %s has no instruction kind in the one list", kind)
			assert.Equal(t, spec[kind], want, "kinds.txt instruction for %s disagrees with docs/SPEC-ISA.md", kind)
			raw := typedCard(
				"KIND: "+kind,
				"PATHS: internal/swarm/lintheader.go",
				"TEST: internal/swarm TestLintNamesTheInstructionKindOfEveryCard",
			)
			for _, f := range LintCardHeader(raw, nil, false) {
				assert.NotContains(t, f.Excerpt, "no instruction kind", "KIND %s was refused: %s", kind, f.Excerpt)
			}
			// The lint names it: the resolved instruction kind is the kinds.txt
			// column for this row, read here from the file itself.
			work, inst, ok := ResolveInstructionKind(raw)
			require.True(t, ok, "the lint resolved no instruction kind for KIND %s", kind)
			assert.Equal(t, kind, work)
			assert.Equal(t, column[kind], inst, "the lint's instruction kind for %s is not the kinds.txt column", kind)
		})
	}

	// Nothing resolves where the lint refuses: no KIND: line, or an undeclared kind.
	_, _, ok := ResolveInstructionKind(typedCard("PATHS: internal/swarm/lintheader.go"))
	assert.False(t, ok, "a card with no KIND: line resolves no instruction kind")
	_, _, ok = ResolveInstructionKind(typedCard("KIND: no-such-kind", "PATHS: internal/swarm/lintheader.go"))
	assert.False(t, ok, "an undeclared KIND resolves no instruction kind")

	// The refusal is the lint's own: a declared kind whose row names no
	// instruction kind is kind-declared from lintCardHeader, on the KIND: line.
	raw := typedCard(
		"KIND: fix-red",
		"PATHS: internal/swarm/lintheader.go",
		"TEST: internal/swarm TestLintNamesTheInstructionKindOfEveryCard",
	)
	noColumn := func(string) (string, bool) { return "", false }
	var refused []CardHeaderFinding
	for _, f := range lintCardHeader(raw, nil, false, noColumn) {
		if f.Check == "kind-declared" && strings.Contains(f.Excerpt, "no instruction kind") {
			refused = append(refused, f)
		}
	}
	require.Len(t, refused, 1, "a declared KIND whose row names no instruction kind is refused by the lint")
	assert.Contains(t, refused[0].Excerpt, `"fix-red"`)
	assert.Equal(t, 2, refused[0].Line, "the finding sits on the KIND: line")

	src, err := os.ReadFile("lintheader.go")
	require.NoError(t, err)
	assert.NotContains(t, string(src), "ungatedKinds")

	root := filepath.Join("..", "..")
	var tables []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if isKindsTable(t, path, hygiene.Kinds()) {
			tables = append(tables, rel)
		}
		if strings.HasSuffix(rel, ".go") && goFileQuotesEveryKind(t, path, hygiene.Kinds()) {
			tables = append(tables, rel)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/hygiene/kinds.txt"}, tables)
}

func specInstructionKinds(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-ISA.md"))
	require.NoError(t, err)
	declared := map[string]bool{}
	for _, kind := range hygiene.Kinds() {
		declared[kind] = true
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") || strings.Contains(line, "KIND: line") {
			continue
		}
		cells := splitTableRow(line)
		if len(cells) < 5 {
			continue
		}
		instruction := strings.Trim(cells[0], "` ")
		if instruction == "" || instruction == "kind" {
			continue
		}
		for _, tok := range backtickTokens(cells[4]) {
			if declared[tok] {
				out[tok] = instruction
			}
		}
	}
	swarm, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-WORKER.md"))
	require.NoError(t, err)
	require.Contains(t, string(swarm), "names no instruction kind is `kind-declared`")
	require.Contains(t, string(swarm), "internal/hygiene/kinds.txt")
	return out
}

func splitTableRow(line string) []string {
	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		cells = append(cells, p)
	}
	return cells
}

func backtickTokens(cell string) []string {
	var out []string
	for {
		i := strings.IndexByte(cell, '`')
		if i < 0 {
			return out
		}
		cell = cell[i+1:]
		j := strings.IndexByte(cell, '`')
		if j < 0 {
			return out
		}
		out = append(out, cell[:j])
		cell = cell[j+1:]
	}
}

func isKindsTable(t *testing.T, path string, kinds []string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Size() > 1<<20 || info.IsDir() {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw[:min(len(raw), 512)]), "\x00") {
		return false
	}
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if !strings.Contains(line, "\t") {
			return false
		}
		name := strings.TrimSpace(strings.SplitN(line, "\t", 2)[0])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) != len(kinds) {
		return false
	}
	for _, kind := range kinds {
		if !seen[kind] {
			return false
		}
	}
	return true
}

func goFileQuotesEveryKind(t *testing.T, path string, kinds []string) bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(raw)
	for _, kind := range kinds {
		if !strings.Contains(text, `"`+kind+`"`) && !strings.Contains(text, "`"+kind+"`") {
			return false
		}
	}
	return true
}

// kindsTxtInstructionColumn reads the fourth column of internal/hygiene/kinds.txt
// from the file, not through hygiene, so the lint's answer is checked against the
// list itself.
func kindsTxtInstructionColumn(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "hygiene", "kinds.txt"))
	require.NoError(t, err)
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 4 && strings.TrimSpace(fields[3]) != "" {
			out[strings.TrimSpace(fields[0])] = strings.TrimSpace(fields[3])
		}
	}
	return out
}
