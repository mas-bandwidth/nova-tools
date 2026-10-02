package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runTool runs one invocation of either name in dir and returns its exit and streams.
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
		{"misspelled flag", []string{"check", "--fiel", "x"}, []string{"UPDATE REFUSED: unknown flag --fiel; the flags are --budget, --file, --kind, --max, --timeout; run: nova-update check -h"}},
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
