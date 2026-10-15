package update

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A manifest with several problems is refused with ALL of them, in line order, each worded
// as a single problem was: the third cold rating learned the header, the kinds and the
// latest grammar in four runs, one error each.
func TestLoadReportsEveryProblemOfTheManifest(t *testing.T) {
	t.Parallel()
	manifest := strings.Join([]string{
		"name\tkind\tinstalled\tlatest\tapply\towner",
		"# a comment is skipped",
		"a\tgadget\tgo version\tlocal:go version\tnone\tme",
		"b\ttool\tgo version\tnowhere:x\tnone\tme",
		"c\ttool\tgo version",
		"d\ttool\t\tlocal:go version\tnone\tme",
		"a\ttool\tgo version\tlocal:go version\tnone\tme",
		"e\tpin\tgo version\tgithub:o/r\tnone\tme",
		"f\ttool\tgo  version\tlocal:go version\t-\tme",
		"g\ttool\tgo version\tlocal:go version\tnone\tme",
	}, "\n") + "\n"
	_, err := Load(strings.NewReader(manifest))
	var me *ManifestError
	require.Truef(t, errors.As(err, &me), "Load returned %v, want a *ManifestError", err)
	for _, want := range []string{
		"line 3: unknown kind gadget",
		"line 4: unknown source nowhere",
		"line 5: 3 fields, want 6",
		"line 6: empty installed field",
		"line 7: duplicate name a",
		"line 8: pin requires local source",
		"line 9: installed: argv requires single spaces",
	} {
		assert.Truef(t, strings.Contains(err.Error(), want), "the refusal does not carry %q:\n%s", want, err)
	}
	assert.Falsef(t, len(me.Problems) != 7 || me.More != 0, "%d problems (+%d more), want 7: %q", len(me.Problems), me.More, me.Problems)
	// the last line is fine and is not named
	assert.Falsef(t, strings.Contains(err.Error(), "line 10"), "a good line is named: %s", err)
	// a wrong header is one problem among the others, not the only one reported
	_, err = Load(strings.NewReader("name,kind\nx\ttool\tgo version\tlocal:go version\tnone\tme\ny\ttool\n"))
	assert.Falsef(t, err == nil || !strings.Contains(err.Error(), "line 1: invalid header") || !strings.Contains(err.Error(), "line 3: 2 fields"), "a bad header with a bad line: %v", err)
	// one line with two bad fields names both
	_, err = Load(strings.NewReader(Header + "\nx\tgadget\tgo  version\tlocal:go version\tnone\tme\n"))
	assert.Falsef(t, err == nil || !strings.Contains(err.Error(), "unknown kind gadget") || !strings.Contains(err.Error(), "installed: argv requires single spaces"), "a line with two bad fields: %v", err)
	// a good manifest loads
	es, err := Load(strings.NewReader(Header + "\ngo\ttool\tgo version\tlocal:go version\tnone\tme\n"))
	assert.Falsef(t, err != nil || len(es) != 1, "a good manifest: %v %v", es, err)
}

// Problems past the cap are counted, never silently dropped.
func TestLoadCountsProblemsPastTheCap(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString(Header + "\n")
	for i := 0; i < manifestProblemCap+7; i++ {
		b.WriteString("x\tgadget\tgo version\tlocal:go version\tnone\tme\n")
	}
	_, err := Load(strings.NewReader(b.String()))
	var me *ManifestError
	require.Falsef(t, !errors.As(err, &me) || len(me.Problems) != manifestProblemCap || me.More == 0, "got %v", err)
	assert.Falsef(t, !strings.Contains(err.Error(), "and ") || !strings.Contains(err.Error(), " more (fix these and run again)"), "the refusal does not count what it left out: %s", err)
}

// Through the verb: a run over a manifest with two bad lines refuses once, naming both.
func TestReportRefusesAManifestNamingEveryProblem(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/m.tsv"
	err := os.WriteFile(path, []byte(Header+"\na\tgadget\tgo version\tlocal:go version\tnone\tme\nb\ttool\tgo version\tnowhere:x\tnone\tme\n"), 0o600)
	require.NoError(t, err, err)
	var out, errs bytes.Buffer
	rc := Main("nova-version", []string{"report", "--file", path}, "v0", &out, &errs)
	require.EqualValuesf(t, 2, rc, "exit %d, want 2: %s", rc, errs.String())
	for _, want := range []string{"REPORT REFUSED", "line 2: unknown kind gadget", "line 3: unknown source nowhere"} {
		assert.Truef(t, strings.Contains(errs.String(), want), "stderr has no %q: %s", want, errs.String())
	}
	assert.Truef(t, strings.Count(errs.String(), "\n") == 1, "the refusal is not one line: %q", errs.String())
}

// The two tools say, each in its own banner, that they are two binaries sharing the
// manifest and report, and what each is for (ledger U8, V1: they are two builds, never
// "one binary under two names"); `report -h` carries the manifest format in six lines
// under its example line.
func TestBannersSayTwoBinariesAndReportHelpCarriesTheManifest(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"nova-update", "nova-version"} {
		var out, errs bytes.Buffer
		rc := Main(name, []string{"help"}, "v0", &out, &errs)
		require.EqualValuesf(t, 0, rc, "%s help: exit %d", name, rc)
		help := out.String()
		for _, want := range []string{"two binaries", "THE MANIFEST is the file --file names"} {
			assert.Contains(t, help, want, name)
		}
		// nova-update has no snapshot verb, so its help names no snapshot default.
		for _, untrue := range []string{"ONE binary", "same build", "snapshot's --timeout"} {
			assert.NotContains(t, help, untrue, name)
		}
		other := "nova-version"
		if name == "nova-version" {
			other = "nova-update"
		}
		assert.Containsf(t, help, other+"'s", "%s help does not say which verbs are %s's", name, other)
		out.Reset()
		errs.Reset()
		rc = Main(name, []string{"report", "-h"}, "v0", &out, &errs)
		require.EqualValuesf(t, 0, rc, "%s report -h: exit %d", name, rc)
		h := out.String()
		numbered := 0
		for _, l := range strings.Split(h, "\n") {
			if t := strings.TrimSpace(l); len(t) > 2 && t[0] >= '1' && t[0] <= '6' && t[1] == '.' {
				numbered++
			}
		}
		assert.EqualValuesf(t, 6, numbered, "%s report -h carries %d manifest lines, want six:\n%s", name, numbered, h)
		for _, want := range []string{"name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner", "github:<owner>/<repo>", "harness, engine, model, tool or pin", "EVERY problem"} {
			assert.Containsf(t, h, want, "%s report -h does not carry %q", name, want)
		}
	}
	// nova-version's exit codes name no verb it does not have
	var out, errs bytes.Buffer
	Main("nova-version", []string{"help"}, "v0", &out, &errs)
	for _, l := range strings.Split(out.String(), "\n") {
		assert.Falsef(t, strings.HasPrefix(l, "exit codes:") && strings.Contains(l, "apply"), "nova-version's exit codes name apply, which it does not have: %s", l)
	}
}
