package friend

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDSHOneShot is set in the environment of this test binary started again as
// dsh (`<bin> --profile headless -`): it is then the fake dsh, and never the tests.
// Its value is what the turn answers: "missing-credential", MISSING_CREDENTIAL exit
// 1; "exit-2", a failing lane that exits 2; "success", creates REPORT.md and RESULT.md.
const fakeDSHOneShot = "NOVA_FRIEND_FAKE_DSH_ONESHOT"

func init() {
	if mode := os.Getenv(fakeDSHOneShot); mode != "" && len(os.Args) > 2 && os.Args[1] == "--profile" && os.Args[2] == "headless" {
		os.Exit(fakeDSHOneShotExit(mode))
	}
}

func fakeDSHOneShotExit(mode string) int {
	io.ReadAll(os.Stdin) // ignored: the fake answers whatever the text
	outbox := os.Getenv("TEST_DSH_ONESHOT_OUTBOX")
	switch mode {
	case "missing-credential":
		fmt.Printf("dsh: provider deepseek: MISSING_CREDENTIAL (llm-deepseek: no API key for provider route deepseek-official)\n")
		return 1
	case "exit-2":
		return 2
	case "success":
		if outbox != "" {
			os.MkdirAll(outbox, 0o755)
			os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: pass\n"), 0o644)
			os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("done\n"), 0o644)
		}
		fmt.Println("turn taken")
		return 0
	}
	fmt.Println("unknown mode")
	return 1
}

// fakeDSHOneShotExec runs this test binary as the fake dsh in mode with outbox.
func fakeDSHOneShotExec(mode, outbox string) Exec {
	env := []string{fakeDSHOneShot + "=" + mode}
	if outbox != "" {
		env = append(env, "TEST_DSH_ONESHOT_OUTBOX="+outbox)
	}
	return envExec(env...)
}

// DSH one-shot lanes: each card runs as a fresh headless dsh process with the
// brief on stdin, its REPORT.md and RESULT.md read from the card's outbox.
func TestDSHCardRunnerRunsEachCardAsOneShot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outbox := t.TempDir()
	var out strings.Builder
	c := &DSHCardRunner{Friend: "test", Dir: dir, Run: fakeDSHOneShotExec("success", outbox), Program: os.Args[0], Out: &out}
	brief := filepath.Join(t.TempDir(), "BRIEF.md")
	require.NoError(t, os.WriteFile(brief, []byte("do the work"), 0o644))
	card := Card{ID: "c1", Brief: brief, Outbox: outbox}
	lt, err := c.RunCard(context.Background(), card)
	require.NoError(t, err)
	assert.Equal(t, 0, lt.Exit)
	assert.FileExists(t, filepath.Join(outbox, "REPORT.md"))
	assert.FileExists(t, filepath.Join(outbox, "RESULT.md"))
}

// A missing credential exit is reported as a provider refusal, a judgment told
// at once, never a silent failure (the finding of 2026-10-10, the headless
// one-shot probe's MISSING_CREDENTIAL exit).
func TestDSHCardRunnerReportsMissingCredentialAsRefusal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var out strings.Builder
	c := &DSHCardRunner{Friend: "test", Dir: dir, Run: fakeDSHOneShotExec("missing-credential", ""), Program: os.Args[0], Out: &out}
	brief := filepath.Join(t.TempDir(), "BRIEF.md")
	require.NoError(t, os.WriteFile(brief, []byte("do the work"), 0o644))
	card := Card{ID: "c1", Brief: brief, Outbox: t.TempDir()}
	lt, err := c.RunCard(context.Background(), card)
	var refused ProviderRefused
	require.ErrorAs(t, err, &refused, "missing credential should be ProviderRefused")
	assert.Equal(t, "dsh: missing credential for provider route deepseek-official", refused.Reason)
	assert.Equal(t, 1, lt.Exit)
}

// DSH one-shot lanes run independently per card; a failing lane does not stop
// the others: three cards run, the middle exits 2 and fails alone.
func TestDSHCardRunnerMultipleCardsRunIndependently(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	count := 0
	run := func(_ context.Context, _ string, name string, args []string, stdin string) (string, int, error) {
		count++
		outbox := filepath.Join(dir, fmt.Sprintf("out%d", count))
		os.MkdirAll(outbox, 0o755)
		if count == 1 {
			os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: pass\n"), 0o644)
			os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("done\n"), 0o644)
		} else if count == 2 {
			return "dsh: provider deepseek refused\n", 2, nil
		} else {
			os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: pass\n"), 0o644)
			os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("done\n"), 0o644)
		}
		return "done\n", 0, nil
	}
	c := &DSHCardRunner{Friend: "test", Dir: dir, Run: run, Program: os.Args[0]}
	ids := []string{"c1", "c2", "c3"}
	var failed []string
	for i, id := range ids {
		brief := filepath.Join(t.TempDir(), "BRIEF.md")
		require.NoError(t, os.WriteFile(brief, []byte("do the work"), 0o644))
		outbox := filepath.Join(dir, fmt.Sprintf("out%d", i+1))
		os.MkdirAll(outbox, 0o755)
		_, err := c.RunCard(context.Background(), Card{ID: id, Brief: brief, Outbox: outbox})
		if err != nil {
			failed = append(failed, id)
		}
	}
	assert.Equal(t, []string{"c2"}, failed, "only the failing lane fails; the others run on")
}

// DSH one-shot lanes run dsh --profile headless, the brief on stdin.
func TestDSHCardRunnerUsesHeadlessProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outbox := t.TempDir()
	var call struct {
		argv []string
		dir  string
	}
	run := func(_ context.Context, runDir, name string, args []string, stdin string) (string, int, error) {
		call.argv = args
		call.dir = runDir
		os.MkdirAll(outbox, 0o755)
		os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: pass\n"), 0o644)
		os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("done\n"), 0o644)
		return "done\n", 0, nil
	}
	c := &DSHCardRunner{Friend: "test", Dir: dir, Run: run, Program: "dsh"}
	brief := filepath.Join(t.TempDir(), "BRIEF.md")
	require.NoError(t, os.WriteFile(brief, []byte("do the work"), 0o644))
	card := Card{ID: "c1", Brief: brief, Outbox: outbox}
	// the run goes in the card's job directory when its lane names one (WithLaneDir)
	job := filepath.Join(t.TempDir(), "job")
	require.NoError(t, os.MkdirAll(job, 0o755))
	_, err := c.RunCard(WithLaneDir(context.Background(), job), card)
	require.NoError(t, err)
	assert.Equal(t, []string{"--profile", "headless", "-"}, call.argv)
	assert.Equal(t, job, call.dir)
}