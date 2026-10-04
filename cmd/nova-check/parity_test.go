package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusWord is the first word after the token on a line of a verb's text form.
var statusWord = regexp.MustCompile(`(?m)^(?:[A-Z][A-Z-]*|nova-check(?: [a-z]+)*) (OK|FAIL|NO|WARN|REFUSED)\b`)

// Every verb with both a text and a --json form answers one run with one value:
// the same exit code, the same status, and the same dry-run fact, on a passing
// and a failing input.
func TestTextAndJSONGiveTheSameVerdict(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
		return p
	}
	kernelFile := write("k/kernel.md", "# Kernel\n")
	goodManifest := write("home/MANIFEST", "kernel.md\n")
	write("home/kernel.md", "# Kernel\n")
	badManifest := write("home/BAD", "missing.md\n")
	write("links-good/a.md", "[b](b.md)\n")
	write("links-good/b.md", "# b\n")
	write("links-bad/a.md", "[x](missing.md)\n")
	write("prose-good/a.md", "the receive step\n")
	write("prose-bad/a.md", "the recieve step\n")
	write("code-bad/run.sh", "#!/bin/sh\n")
	write("prose-dry/a.md", "the recieve step\n")
	home := filepath.Join(dir, "home")
	core, source := "../../internal/check/testdata/seed-core-floors.md", "../../internal/check/testdata/seed-floors.md"
	ledger := "testdata/example-self/corpus/anchors.md"

	cases := []struct {
		name string
		args []string
	}{
		{"attest pass", []string{"attest", "--home", home, "--manifest", goodManifest}},
		{"attest fail", []string{"attest", "--home", home, "--manifest", badManifest}},
		{"links pass", []string{"links", "--dir", filepath.Join(dir, "links-good")}},
		{"links fail", []string{"links", "--dir", filepath.Join(dir, "links-bad")}},
		{"kernel pass", []string{"kernel", "--file", kernelFile, "--max-bytes", "4000"}},
		{"kernel fail", []string{"kernel", "--file", kernelFile, "--max-bytes", "1"}},
		{"kernel tokens fail", []string{"kernel", "--file", kernelFile, "--max-tokens", "1", "--bytes-per-token", "1"}},
		{"nocode pass", []string{"nocode", "--dir", filepath.Join(dir, "prose-good")}},
		{"nocode fail", []string{"nocode", "--dir", filepath.Join(dir, "code-bad")}},
		{"floors pass", []string{"floors", "--core", core, "--source", source}},
		{"floors fail", []string{"floors", "--core", source, "--source", core}},
		{"corpus pass", []string{"corpus", "--ledger", ledger, "--root", exampleSelf, "--min-anchors", "2"}},
		{"corpus fail", []string{"corpus", "--ledger", ledger, "--root", exampleSelf, "--min-anchors", "99"}},
		{"spelling pass", []string{"spelling", "--dir", filepath.Join(dir, "prose-good")}},
		{"spelling fail", []string{"spelling", "--dir", filepath.Join(dir, "prose-bad")}},
		{"spelling write planned", []string{"spelling", "--dir", filepath.Join(dir, "prose-dry"), "--write", "--dry-run"}},
		{"refused", []string{"links", "--dir", filepath.Join(dir, "absent")}},
		{"version", []string{"version"}},
	}
	lab := hygLab(t)
	hygWrite(t, lab, "elsewhere/x.go", "package elsewhere\n")
	hygGit(t, lab, "add", "-A")
	hygGit(t, lab, "commit", "-q", "-m", "out of path")
	cases = append(cases,
		struct {
			name string
			args []string
		}{"hygiene pass", []string{"hygiene", "--repo", lab, "--base", "main", "--head", "HEAD", "--identity", "Rowan <rowan@example.com>"}},
		struct {
			name string
			args []string
		}{"hygiene fail", []string{"hygiene", "--repo", lab, "--base", "main", "--head", "HEAD", "--identity", "Rowan <rowan@example.com>", "--paths", "sign/**"}},
	)
	for _, tc := range cases {
		textExit, textOut, textErr := runCheck(t, tc.args...)
		jsonExit, jsonOut, _ := runCheck(t, append(tc.args, "--json")...)
		assert.Equal(t, textExit, jsonExit, "%s: exit", tc.name)
		var obj struct {
			Result struct {
				Exit   int
				Status string
			}
			Facts map[string]any
		}
		require.NoError(t, json.Unmarshal([]byte(jsonOut), &obj), "%s: %s", tc.name, jsonOut)
		assert.Equal(t, jsonExit, obj.Result.Exit, "%s: the object's exit", tc.name)
		want := map[int]string{0: "ok", 1: "failed", 2: "refused"}[textExit]
		assert.Equal(t, want, obj.Result.Status, "%s: status", tc.name)
		if tc.name != "version" { // version's line form is the bare build identity
			words := statusWord.FindAllStringSubmatch(textOut+textErr, -1)
			require.NotEmpty(t, words, "%s: no status line in %q", tc.name, textOut+textErr)
			last := words[len(words)-1][1]
			switch textExit {
			case 0:
				assert.Equal(t, "OK", last, "%s", tc.name)
			case 1:
				assert.Contains(t, []string{"FAIL", "NO"}, last, "%s", tc.name)
			default:
				assert.Equal(t, "REFUSED", last, "%s", tc.name)
			}
		}
		assert.Equal(t, strings.Contains(textOut+textErr, "dry_run=true"), obj.Facts["dry_run"] == true, "%s: the dry-run fact", tc.name)
	}
}

// convergence's --json is its own reading object: it agrees with the lines on
// the exit, the verdict word and the dry-run fact, on a quiet tick and a red one.
func TestConvergenceTextAndJSONGiveTheSameVerdict(t *testing.T) {
	t.Parallel()
	for _, red := range []bool{false, true} {
		{
			f := newConvFixture(t)
			state := filepath.Join(f.dir, "state.json")
			_, _, _ = f.run(t, "--state", state)
			owe := func(row string) {
				fh, err := os.OpenFile(filepath.Join(f.dir, "ledger.md"), os.O_APPEND|os.O_WRONLY, 0o644)
				require.NoError(t, err)
				_, err = fh.WriteString(row)
				require.NoError(t, err)
				require.NoError(t, fh.Close())
			}
			if red {
				owe("| c | three | TODO |\n")
				_, _, _ = f.run(t, "--state", state, "--now", "2026-09-18T13:00:00Z")
				owe("| d | four | TODO |\n")
			}
			args := []string{"--state", state, "--now", "2026-09-18T14:00:00Z", "--dry-run"}
			textExit, textOut, textErr := f.run(t, args...)
			jsonExit, jsonOut, _ := f.run(t, append(args, "--json")...)
			assert.Equal(t, textExit, jsonExit, "red=%v", red)
			var obj struct {
				Verdict string `json:"verdict"`
				DryRun  bool   `json:"dry_run"`
			}
			require.NoError(t, json.Unmarshal([]byte(jsonOut), &obj), jsonOut)
			assert.Contains(t, textOut, "CONVERGENCE "+obj.Verdict, "red=%v", red)
			assert.Equal(t, strings.Contains(textErr, "dry_run=true"), obj.DryRun, "red=%v", red)
			if red {
				assert.Equal(t, 1, textExit, "the red tick")
			}
		}
	}
}

// A planned write lists its corrections under --fail-max like every listing, in
// both renderings, and changes no byte.
func TestAPlannedSpellingWriteIsBoundedAndWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(file, []byte("recieve\nseperate\n"), 0o644))
	args := []string{"spelling", "--dir", dir, "--write", "--dry-run", "--fail-max", "1"}
	exit, stdout, _ := runCheck(t, args...)
	assert.Equal(t, 0, exit)
	assert.Equal(t, 1, strings.Count(stdout, "SPELLING FIX "), stdout)
	assert.Contains(t, stdout, "SPELLING MORE kind=misspelling shown=1 total=2")
	exit, stdout, _ = runCheck(t, append(args, "--json")...)
	assert.Equal(t, 0, exit)
	var obj struct {
		Items []any
		More  []struct{ Shown, Total int }
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &obj))
	assert.Len(t, obj.Items, 1)
	require.Len(t, obj.More, 1)
	assert.Equal(t, 2, obj.More[0].Total)
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, "recieve\nseperate\n", string(raw))
}
