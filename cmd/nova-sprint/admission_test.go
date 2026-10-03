package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

const admissionMismatch = "../../internal/swarm/testdata/admission-mismatch.md"

func TestAddAndBriefHelpStateTheAdmissionContract(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, verb := range []string{"help add", "help brief"} {
		out := ta.ok(verb)
		require.Contains(t, out, swarm.AdmissionContract, verb)
	}
}

// The fixture fails result-first and carries the six general rules, so add
// admits it. Dropping one general-rule sentence is still a refusal.
func TestAddAdmitsAShapeDriftAndStillRefusesAMissingChildRule(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("add --stream s1 --count 1 --brief-file " + admissionMismatch)
	require.Equal(t, 0, code, "exit %d\n%s%s", code, out, errs)
	require.Contains(t, out, "ADD OK")
	require.NotContains(t, errs, "LINT DRIFT")

	raw, err := os.ReadFile(admissionMismatch)
	require.NoError(t, err)
	dropped := strings.Replace(string(raw), "Report what was not done.\n", "", 1)
	require.NotEqual(t, string(raw), dropped)
	path := filepath.Join(t.TempDir(), "dropped.md")
	require.NoError(t, os.WriteFile(path, []byte(dropped), 0o644))
	code, out, errs = ta.do("add --stream s2 --count 1 --brief-file " + path)
	require.Equal(t, 2, code, "exit %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "LINT DRIFT brief rule-report-not-done:")
	require.Contains(t, errs, "fails the card lint (1 finding);")
	require.NotContains(t, out, "ADD OK")

	text := "RESULT: done\n" + strings.SplitN(string(raw), "\n", 2)[1]
	i := strings.Index(text, "\nRULES.\n")
	require.Greater(t, i, 0)
	shape := filepath.Join(t.TempDir(), "shape.md")
	require.NoError(t, os.WriteFile(shape, []byte(text[:i+1]), 0o644))
	code, out, errs = ta.do("add --stream s3 --count 1 --brief-file " + shape)
	require.Equal(t, 2, code, "exit %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "LINT DRIFT brief rule-worktree:")
	require.NotContains(t, out, "ADD OK")
}
