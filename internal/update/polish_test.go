package update

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runTool runs one invocation of either tool and returns its exit and streams.
func runTool(t *testing.T, name string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Run(name, args, "v0", &out, &errs, Environment{})
	return code, out.String(), errs.String()
}

// writeFile writes body to name under a fresh temporary directory and returns its path.
func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// A header refusal shows the tab as <TAB>, the way the banner spells it, never
// the escape \x09 (ledger U3, V6): a reader copies the header from the line.
func TestAHeaderRefusalSpellsTheTabAsTAB(t *testing.T) {
	t.Parallel()
	bad := writeFile(t, "bad.tsv", "name\tkind\n")
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"check", []string{"check", "--file", bad}, "name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner"},
		{"version report", []string{"report", "--file", bad}, "name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner"},
		{"adoption", []string{"adoption", "--file", bad}, "tool<TAB>friend<TAB>state<TAB>version<TAB>detail"},
		{"watch", []string{"watch", "--adopt", bad}, "check<TAB>command<TAB>owner"},
	} {
		t.Run(c.name, func(t *testing.T) {
			name := "nova-update"
			if c.name == "version report" {
				name = "nova-version"
			}
			code, _, errs := runTool(t, name, c.args...)
			assert.Equal(t, 2, code)
			assert.Contains(t, errs, c.want)
			assert.NotContains(t, errs, `\x09`)
		})
	}
	t.Run("diff", func(t *testing.T) {
		code, _, errs := runTool(t, "nova-version", "diff", "--from", bad, "--to", bad)
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "name<TAB>stamp<TAB>revision<TAB>platform")
	})
}

// A latest of "-" is the manifest's "not known yet" (banner rule 4): check says
// so and names the column to fill, never "unsupported source" (ledger U11). The
// exit stays 1, an UNKNOWN entry.
func TestADashLatestIsNotDeclaredNotUnsupported(t *testing.T) {
	t.Parallel()
	dash := writeFile(t, "dash.tsv", Header+"\nfoo\ttool\t1.0.0\t-\tnone\tme\n")
	for _, verb := range []string{"check", "status"} {
		t.Run(verb, func(t *testing.T) {
			code, out, errs := runTool(t, "nova-update", verb, "--file", dash)
			assert.Equal(t, 1, code)
			assert.Contains(t, out+errs, "latest not declared (-)")
			assert.Contains(t, out+errs, "fill the latest column")
			assert.NotContains(t, out+errs, "unsupported source")
		})
	}
}

// Every refusal nova-update prints is one line in the one grammar,
// `<TOKEN> REFUSED: <reason>; run: <a command that runs>` (STANDARD §2, §3.1;
// ledger U12, X3): an unknown verb is named and the verbs listed, a misspelled
// flag is named with the verb's flags, a bad value says what the flag wants.
func TestEveryUpdateRefusalEndsInACommandToRun(t *testing.T) {
	t.Parallel()
	dash := writeFile(t, "dash.tsv", Header+"\nfoo\ttool\t1.0.0\t-\tnone\tme\n")
	adopt := writeFile(t, "adopt.tsv", "check\tcommand\towner\n")
	verbs := "the verbs are check, status, apply, report, watch, adoption, release, version"
	for _, c := range []struct {
		name string
		args []string
		want []string
	}{
		{"no verb", nil, []string{"UPDATE REFUSED: no verb given; " + verbs + "; run: nova-update help"}},
		{"unknown verb", []string{"bogus"}, []string{`UPDATE REFUSED: unknown verb "bogus"; ` + verbs + "; run: nova-update help"}},
		{"help of an unknown verb", []string{"help", "bogus"}, []string{`unknown verb "bogus"`}},
		{"version with an argument", []string{"version", "x"}, []string{"; run: nova-update version"}},
		{"misspelled flag", []string{"check", "--fiel", "x"}, []string{"CHECK REFUSED: unknown flag --fiel; the flags are --budget, --file, --json, --kind, --max, --timeout; run: nova-update check -h"}},
		{"bad duration", []string{"status", "--file", dash, "--timeout", "abc"}, []string{`--timeout wants a duration (5s, 2m), got "abc"`, "; run: nova-update status -h"}},
		{"bad kind", []string{"check", "--file", dash, "--kind", "bogus"}, []string{"unknown kind bogus (use harness,engine,model,tool,pin)", "; run: nova-update check -h"}},
		{"missing file", []string{"report"}, []string{"missing --file", "; run: nova-update report -h"}},
		{"unreadable file", []string{"check", "--file", "nope.tsv"}, []string{"cannot open nope.tsv", "; run: nova-update check -h"}},
		{"bad bound", []string{"check", "--file", dash, "--max", "-1"}, []string{"--max", "; run: nova-update check -h"}},
		{"apply with no name", []string{"apply", "--file", dash}, []string{"; run: nova-update apply -h"}},
		{"watch with no checks file", []string{"watch"}, []string{"ADOPT REFUSED: missing --adopt", "; run: nova-update watch -h"}},
		{"watch names every missing bus flag", []string{"watch", "--adopt", adopt, "--bus", "b"}, []string{"missing --remote, --branch, --as, --to"}},
		{"watch with an argument", []string{"watch", "--adopt", adopt, "x"}, []string{"; run: nova-update watch -h"}},
		{"adoption with no file", []string{"adoption"}, []string{"missing --file", "; run: nova-update adoption -h"}},
		{"release with no verb", []string{"release"}, []string{"RELEASE REFUSED: a release verb is required; the release verbs are cut, build, install, adopt, pull; run: nova-update help release"}},
		{"unknown release verb", []string{"release", "bogus"}, []string{`RELEASE REFUSED: unknown release verb "bogus"; the release verbs are cut, build, install, adopt, pull; run: nova-update help release`}},
		{"release flag misspelled", []string{"release", "cut", "--rpeo", "x"}, []string{"CUT REFUSED:", "--rpeo", "; run: nova-update release cut -h"}},
		{"release missing flags", []string{"release", "build"}, []string{"missing --version, --out, --source", "; run: nova-update release build -h"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out, errs := runTool(t, "nova-update", c.args...)
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			assert.Equal(t, 1, strings.Count(errs, "\n"), "one line: %q", errs)
			assert.NotContains(t, errs, "(run ")
			assert.Contains(t, errs, "; run: nova-update ")
			for _, w := range c.want {
				assert.Contains(t, errs, w)
			}
		})
	}
}

// nova-version's refusals name every problem and the shape that would run
// (ledger V4, V7, and the rows found using it cold): a bare snapshot names both
// of its shapes, moved on a directory that is no checkout says so, and diff
// names both unreadable snapshots in one run.
func TestVersionRefusalsNameWhatWouldRun(t *testing.T) {
	t.Parallel()
	manifest := writeFile(t, "m.tsv", Header+"\ngo\ttool\tgo version\tlocal:go version\tnone\tme\n")
	for _, c := range []struct {
		name string
		args []string
		want []string
	}{
		{"bare snapshot", []string{"snapshot"}, []string{"missing --bin, --out", "--file <manifest>"}},
		{"snapshot with both shapes", []string{"snapshot", "--file", manifest, "--bin", t.TempDir()}, []string{"give one shape"}},
		{"moved outside a checkout", []string{"moved", "--from", "a", "--to", "b", "--repo", t.TempDir(), "--out", "m.md"}, []string{"is not a git checkout"}},
		{"diff of two missing snapshots", []string{"diff", "--from", "x.tsv", "--to", "y.tsv"}, []string{"cannot read x.tsv", "cannot read y.tsv", "write one with nova-version snapshot --bin <dir> --out", "; run: nova-version snapshot -h"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out, errs := runTool(t, "nova-version", c.args...)
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			assert.NotContains(t, errs, "git fetch")
			for _, w := range c.want {
				assert.Contains(t, errs, w)
			}
		})
	}
}

// snapshot --file names each adopted tool that did not answer, with its
// reason, so a FAIL needs no second call to learn which.
func TestSnapshotOfAManifestNamesTheToolsThatDidNotAnswer(t *testing.T) {
	t.Parallel()
	manifest := writeFile(t, "m.tsv", Header+"\nghost\ttool\tnova-no-such-tool-here\tgithub:example/ghost\tnone\tme\nknown\ttool\tv1.2.3\tgithub:example/known\tnone\tme\n")
	code, _, errs := runTool(t, "nova-version", "snapshot", "--file", manifest)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "SNAPSHOT FAIL checked=2 known=1 unknown=1")
	assert.Contains(t, errs, "SNAPSHOT UNKNOWN name=ghost")
	assert.NotContains(t, errs, "name=known")
}

// Every nova-update verb on a manifest builds one value and takes --json for it
// (STANDARD §2; ledger U14, X1, X4): the JSON is one object on stdout, refusals
// included; the line form opens with the verb and its status word; and kinds=
// names the kinds the run read, never a kind the file does not hold (U13).
func TestUpdateVerbsTakeJSONAndLeadWithTheirStatus(t *testing.T) {
	t.Parallel()
	m := writeFile(t, "m.tsv", Header+"\nfoo\ttool\tv1.0.0\t-\tinstall-foo {version}\tme\n")
	adoption := writeFile(t, "a.tsv", "tool\tfriend\tstate\tversion\tdetail\nfoo\tme\tadopted\t1.0.0\ttried it first\n")
	for _, c := range []struct {
		name   string
		args   []string
		exit   int
		status string
		item   string
	}{
		{"check", []string{"check", "--file", m}, 1, "failed", "unknown"},
		{"status", []string{"status", "--file", m}, 1, "failed", "unknown"},
		{"report", []string{"report", "--file", m}, 0, "ok", "tool"},
		{"apply dry run", []string{"apply", "--file", m, "foo", "--version", "1.2.0", "--dry-run"}, 0, "ok", "plan"},
		{"adoption", []string{"adoption", "--file", adoption}, 0, "ok", "choice"},
		{"refusal", []string{"check"}, 2, "refused", ""},
		{"unknown verb", []string{"bogus"}, 2, "refused", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out, errs := runTool(t, "nova-update", append(c.args, "--json")...)
			assert.Equal(t, c.exit, code)
			assert.Empty(t, errs)
			var got struct {
				Result struct {
					Verb, Status, Remedy string
				}
				Facts map[string]any
				Items []struct{ Kind string }
			}
			require.NoError(t, json.Unmarshal([]byte(out), &got), out)
			assert.Equal(t, c.status, got.Result.Status)
			if c.item != "" {
				require.NotEmpty(t, got.Items)
				assert.Equal(t, c.item, got.Items[len(got.Items)-1].Kind)
			} else {
				assert.Contains(t, got.Result.Remedy, "nova-update ")
			}
			if c.name == "check" {
				assert.Equal(t, "tool", got.Facts["kinds"])
			}
		})
	}
	t.Run("lines open with the verb and its status", func(t *testing.T) {
		_, _, errs := runTool(t, "nova-update", "check", "--file", m)
		assert.True(t, strings.HasPrefix(errs, "CHECK FAIL checked=1 "), errs)
		assert.Contains(t, errs, " kinds=tool ")
		_, out, _ := runTool(t, "nova-update", "apply", "--file", m, "foo", "--version", "1.2.0", "--dry-run")
		assert.True(t, strings.HasPrefix(out, "APPLY OK name=foo dry_run=true from=1.0.0 to=1.2.0 "), out)
		assert.Contains(t, out, "APPLY PLAN name=foo argv=2 version=1.2.0: install-foo 1.2.0")
	})
}

// moved reads the usage lines of every tool's help, indented or not: a tool on
// the shared skeleton indents them under usage:, and its verbs were invisible.
func TestMovedReadsAnIndentedUsageBlock(t *testing.T) {
	t.Parallel()
	verbs := parseMovedHelp("nova-version", VersionTool("", Environment{}).Banner())
	require.Contains(t, verbs, "diff")
	assert.True(t, verbs["diff"]["--from"])
	assert.Contains(t, verbs, "moved")
}
