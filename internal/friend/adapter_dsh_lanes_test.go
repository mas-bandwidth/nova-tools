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
// Its value is what the turn answers: "missing-credential", MISSING_CREDENTIAL; "success",
// creates REPORT.md and RESULT.md; "exit-n", exits with code n.
const fakeDSHOneShot = "NOVA_FRIEND_FAKE_DSH_ONESHOT"

func init() {
	if mode := os.Getenv(fakeDSHOneShot); mode != "" && len(os.Args) > 1 && os.Args[1] == "--profile" && os.Args[2] == "headless" {
		os.Exit(fakeDSHOneShotExit(mode))
	}
}

func fakeDSHOneShotExit(mode string) int {
	io.ReadAll(os.Stdin) // ignored: the fake answers whatever the text
	outbox := os.Getenv("TEST_DSH_ONESHOT_OUTBOX")
	switch mode {
	case "missing-credential":
		fmt.Printf("dsh: provider deepseek: MISSING_CREDENTIAL (DEEPSEEK_API_KEY=sk-fake-key-12345 rejected)\n")
		return 1
	case "exit-2":
		return 2
	case "success":
		// Create REPORT.md and RESULT.md in outbox
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

// DSH one-shot lanes: each card runs as a fresh headless dsh process with HOME
// confined to the friend's working directory.
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

// A missing credential exit is reported as provider refusal, not silent failure.
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

// DSH one-shot lanes run independently per card; a failing lane does not stop others.
func TestDSHCardRunnerMultipleCardsRunIndependently(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	count := 0
	run := func(_ context.Context, _ string, name string, args []string, stdin string) (string, int, error) {
		count++
		outbox := fmt.Sprintf("%s/out%d", dir, count)
		os.MkdirAll(outbox, 0o755)
		if count == 2 {
			os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: pass\n"), 0o644)
			os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("done\n"), 0o644)
		} else if count == 3 {
			return "dsh: provider deepseek: MISSING_CREDENTIAL\n", 1, nil
		} else {
			os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: pass\n"), 0o644)
			os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("done\n"), 0o644)
		}
		return "done\n", 0, nil
	}
	c := &DSHCardRunner{Friend: "test", Dir: dir, Run: run, Program: os.Args[0]}
	var failed []string
	for i := 1; i <= 3; i++ {
		brief := filepath.Join(t.TempDir(), "BRIEF.md")
		require.NoError(t, os.WriteFile(brief, []byte("do the work"), 0o644))
		outbox := filepath.Join(dir, fmt.Sprintf("out%d", i))
		os.MkdirAll(outbox, 0o755)
		card := Card{ID: fmt.Sprintf("c%d", i), Brief: brief, Outbox: outbox}
		_, err := c.RunCard(context.Background(), card)
		if err != nil {
			failed = append(failed, card.ID)
		}
	}
	assert.Len(t, failed, 1, "only one card should fail")
	assert.Equal(t, "c3", failed[0])
}

// DSH one-shot lanes use dsh --profile headless.
func TestDSHCardRunnerUsesHeadlessProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outbox := t.TempDir()
	var call struct {
		args []string
	}
	run := func(_ context.Context, _ string, name string, args []string, stdin string) (string, int, error) {
		call.args = args
		os.MkdirAll(outbox, 0o755)
		os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: pass\n"), 0o644)
		os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("done\n"), 0o644)
		return "done\n", 0, nil
	}
	c := &DSHCardRunner{Friend: "test", Dir: dir, Run: run, Program: "dsh"}
	brief := filepath.Join(t.TempDir(), "BRIEF.md")
	require.NoError(t, os.WriteFile(brief, []byte("do the work"), 0o644))
	card := Card{ID: "c1", Brief: brief, Outbox: outbox}
	_, err := c.RunCard(context.Background(), card)
	require.NoError(t, err)
	assert.Equal(t, []string{"--profile", "headless", "-"}, call.args)
}
