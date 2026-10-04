package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerbCover covers the verb.go functions whose unit-tier per-function
// coverage was 0.0%: Dispatch (with its wrapper DispatchNote) and the paths
// of VerbFiles and hasMain they reach through. Dispatch and DispatchNote are
// pure and have no refusal; VerbFiles and hasMain each get their main path
// and one refusal.

func TestVerbCoverDispatchNote(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		tool string
		verb string
		want string
	}{
		{
			name: "hyphenated-verb-camels-in-the-case",
			tool: "nova-ci",
			verb: "my-verb",
			want: "add to the dispatch switch in cmd/nova-ci (new-verb never edits it):\n\t" +
				"case \"my-verb\":\n\t\treturn cmdMyVerb(args[1:], stdout, stderr)\n",
		},
		{
			name: "single-word-verb",
			tool: "tool",
			verb: "probe",
			want: "add to the dispatch switch in cmd/tool (new-verb never edits it):\n\t" +
				"case \"probe\":\n\t\treturn cmdProbe(args[1:], stdout, stderr)\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, DispatchNote(tc.tool, tc.verb),
				`DispatchNote(%q, %q)`, tc.tool, tc.verb)
		})
	}
}

func TestVerbCoverDispatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		verb string
		want string
	}{
		{name: "plain", verb: "probe", want: "case \"probe\":\n\treturn cmdProbe(args[1:], stdout, stderr)"},
		{name: "underscore-camel", verb: "my_verb", want: "case \"my_verb\":\n\treturn cmdMyVerb(args[1:], stdout, stderr)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Dispatch(tc.verb), "Dispatch(%q)", tc.verb)
		})
	}
}

func TestVerbCoverVerbFiles(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		tool      string
		verb      string
		hasGoMod  bool
		hasMainGo bool
		wantErr   string
		wantRels  []string
	}{
		{
			name:      "main-path-renders-four-files",
			tool:      "nova-ci",
			verb:      "probe",
			hasGoMod:  true,
			hasMainGo: true,
			wantRels: []string{
				"cmd/nova-ci/probe.go",
				"cmd/nova-ci/probe_test.go",
				"cmd/nova-ci/testdata/probe/fixture.txt",
				"make/verb_nova-ci_probe.mk",
			},
		},
		{
			name:     "refuses-a-tool-name-that-is-a-go-keyword",
			tool:     "func",
			verb:     "probe",
			hasGoMod: true,
			wantErr:  "is a Go keyword",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tree := t.TempDir()
			if tc.hasGoMod {
				require.NoError(t, os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module test"), 0o644))
			}
			if tc.hasMainGo {
				writeTool(t, tree, tc.tool)
			}

			outs, err := VerbFiles(tree, tc.tool, tc.verb)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr, "VerbFiles(%q, %q)", tc.tool, tc.verb)
				assert.Empty(t, outs, "VerbFiles wrote %v despite refusing", outs)
				return
			}
			require.NoError(t, err, "VerbFiles(%q, %q)", tc.tool, tc.verb)
			require.Len(t, outs, len(tc.wantRels), "VerbFiles planned %d files, want %d", len(outs), len(tc.wantRels))
			for i, o := range outs {
				assert.Equal(t, tc.wantRels[i], o.Rel, "planned file %d is %q, want %q", i, o.Rel, tc.wantRels[i])
				assert.NotEmpty(t, o.Data, "planned file %s rendered empty", o.Rel)
			}
		})
	}
}

func TestVerbCoverHasMain(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		dir     string
		files   map[string]string
		want    bool
		wantErr string
	}{
		{
			name:  "finds-a-plain-func-main",
			files: map[string]string{"main.go": "package main\n\nfunc main() {}\n"},
			want:  true,
		},
		{
			name: "a-missing-dir-is-false-not-an-error",
			dir:  "absent",
		},
		{
			name:    "refuses-a-file-it-cannot-parse",
			files:   map[string]string{"broken.go": "package main\n\nfunc main("},
			wantErr: "cannot parse it to find func main",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tree := t.TempDir()
			dir := filepath.Join(tree, "cmd", "tool")
			if tc.dir != "" {
				dir = filepath.Join(tree, tc.dir)
			} else {
				require.NoError(t, os.MkdirAll(dir, 0o755))
			}
			for f, src := range tc.files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte(src), 0o644))
			}

			got, err := hasMain(dir)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr, "hasMain on an unparseable file")
				return
			}
			require.NoError(t, err, "hasMain(%s)", dir)
			assert.Equal(t, tc.want, got, "hasMain(%s) = %v, want %v", dir, got, tc.want)
		})
	}
}
