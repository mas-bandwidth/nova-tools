package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads, runs, dials or writes anything (the CLI style's rule
// (b), #4505). functional's -h used to be a refusal at exit 2 (#4503, against
// silence); it is help now, which is not silence either.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, ciRun, []testverbhelp.Case{
		{Verb: "slowtests", Flags: []string{"--allowlist", "{dir}/allow.txt"}},
		{Verb: "local", Flags: []string{"--base", "origin/dev"}},
		{Verb: "functional"},
		{Verb: "new-rule", Flags: []string{"--root", "{dir}"}},
		{Verb: "new-verb", Flags: []string{"--root", "{dir}"}},
		{Verb: "github receipt", Flags: []string{"--redis", "{addr}", "--from-runner"}},
		{Verb: "github"},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, ciRun, "nova-ci", "slowtests", "functional", "version")

	// The three lines a cold read of `nova-ci help` costs most, held to the
	// behaviour by running it (docs/CLI-STYLE.md (d); docs/STANDARD.md §2;
	// docs/SPEC-CI.md `ci-receipt`: one XADD, a refusal is not retried).
	code, stdout, stderr := runCI(t, []string{"help"}, "")
	require.Equal(t, 0, code, "stderr %s", stderr)
	assert.Contains(t, stdout, "with the password in the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names, never on the line")
	attempts := refusedWriteAttempts(t)
	retrySentence := "A refused write is tried once, not retried."
	if attempts != 1 {
		retrySentence = "A refused write is retried."
	}
	assert.Contains(t, stdout, retrySentence, "refused-write attempts=%d; help must name that count", attempts)
	assert.Contains(t, stdout, "exit codes: 0 done, 1 the verb said no (slowtests, local, github receipt), 2 usage or could not run; by verb:")
	assert.NotContains(t, stdout, "go test -json <packages> | nova-ci slowtests --budget 60")
	assert.Contains(t, stdout, "In your own module, slowtests reads the events of the packages you name, at a\n60-second budget; the commands under example: are what runs.")
	row := allowlistRowInHelp(t, stdout)
	assert.Equal(t, "internal/ci/slowtests\tTestA\t4.5\t3s@run1", row)
	slowtestsHonorsTheHelpRow(t, row)
	exitOneMatchesTheSummary(t)
	examples, err := onboarding.ExampleLines(stdout, "nova-ci")
	require.NoError(t, err)
	for _, ex := range examples {
		args := strings.Fields(ex)[1:]
		exit, out, errs := runCI(t, args, "")
		assert.NotEqual(t, 2, exit, "example %q does not run: %s", ex, errs)
		assert.NotEmpty(t, out, "example %q printed nothing", ex)
	}
}

// refusedWriteAttempts runs github receipt against a store that refuses the
// one XADD and returns how many times that write was sent. A refusal is not
// retried: cireceipt.Write appends once and cmdReceipt returns 1
// (docs/SPEC-CI.md `ci-receipt`; internal/ghevent.Publish is one XADD).
func refusedWriteAttempts(t *testing.T) int {
	t.Helper()
	var n int
	var out, errb bytes.Buffer
	code := cmdReceipt(context.Background(), []string{"--from-runner", "--redis", "127.0.0.1:1", "--repo", "mas-bandwidth/nova-tools",
		"--sha", receiptSHA, "--run-id", "42", "--workflow", "CI", "--conclusion", "success"},
		&out, &errb, noEnv, countingRefusedStore(&n))
	require.Equal(t, 1, code, "a refused write exits 1; stdout %q stderr %q attempts %d", out.String(), errb.String(), n)
	require.Contains(t, errb.String(), "FAILED")
	return n
}

func countingRefusedStore(n *int) receiptOpener {
	return func(ctx context.Context, addr string) (*store.Store, error) {
		st, err := store.Open(ctx, addr)
		if err != nil {
			return nil, err
		}
		st.Client().AddHook(countRefusedHook{n: n})
		return st, nil
	}
}

// countRefusedHook answers every command with WRONGTYPE and counts XADD, the
// write a refusal is. It never dials (STANDARD §8).
type countRefusedHook struct{ n *int }

func (countRefusedHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h countRefusedHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), "xadd") {
			*h.n++
		}
		cmd.SetErr(wrongType)
		return wrongType
	}
}

func (h countRefusedHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if strings.EqualFold(cmd.Name(), "xadd") {
				*h.n++
			}
			cmd.SetErr(wrongType)
		}
		return wrongType
	}
}

// allowlistRowInHelp is the one literal allowlist row the banner prints:
// module-relative package, test, seconds, measured@where, tab-separated, the bytes a reader pastes.
func allowlistRowInHelp(t *testing.T, help string) string {
	t.Helper()
	var row string
	for _, line := range strings.Split(help, "\n") {
		line = strings.TrimSpace(line)
		if strings.Count(line, "\t") == 3 && strings.Contains(line, "@") {
			require.Empty(t, row, "help prints two allowlist rows: %q and %q", row, line)
			row = line
		}
	}
	require.NotEmpty(t, row, "help has no literal tab-separated allowlist row")
	return row
}

// slowtestsHonorsTheHelpRow writes the banner's row, unchanged, and runs
// slowtests against it: the same test is over the default test budget and
// inside the row's budget (docs/SPEC-CI.md slowtests).
func slowtestsHonorsTheHelpRow(t *testing.T, row string) {
	t.Helper()
	allow := filepath.Join(t.TempDir(), "allow.txt")
	require.NoError(t, os.WriteFile(allow, []byte(row+"\n"), 0o644))
	stdin := "{\"Action\":\"pass\",\"Package\":\"example.com/internal/ci/slowtests\",\"Test\":\"TestA\",\"Elapsed\":3.2}\n" +
		"{\"Action\":\"pass\",\"Package\":\"example.com/internal/ci/slowtests\",\"Elapsed\":3.2}\n"
	code, _, stderr := runCI(t, []string{"slowtests", "--budget", "60", "--test-budget", "1", "--enforce", "--load", "1", "--cpus", "2"}, stdin)
	require.Equal(t, 1, code, "without the row the test is over budget: stderr %s", stderr)
	code, stdout, stderr := runCI(t, []string{"slowtests", "--budget", "60", "--test-budget", "1", "--allowlist", allow, "--enforce", "--load", "1", "--cpus", "2"}, stdin)
	require.Equal(t, 0, code, "the help row is not a row the verb honors: stdout %q stderr %s", stdout, stderr)
	assert.Contains(t, stdout, "CI-SLOW OK")
}

// exitOneMatchesTheSummary runs the three verbs the exit summary names as
// able to say no, and one verb it does not, so the summary is the behaviour
// (docs/CLI-STYLE.md (d)).
func exitOneMatchesTheSummary(t *testing.T) {
	t.Helper()
	stdin := "{\"Action\":\"pass\",\"Package\":\"example.com/pkg\",\"Elapsed\":75.3}\n"
	code, _, _ := runCI(t, []string{"slowtests", "--budget", "60", "--enforce", "--load", "1", "--cpus", "2"}, stdin)
	assert.Equal(t, 1, code, "slowtests --enforce over budget")
	f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: "{\"Action\":\"fail\",\"Package\":\"example.com/m/cmd/a\",\"Test\":\"TestB\",\"Elapsed\":0.1}\n{\"Action\":\"fail\",\"Package\":\"example.com/m/cmd/a\",\"Elapsed\":0.2}\n", code: 2})
	code, _, _ = runLocal(t, f)
	assert.Equal(t, 1, code, "local with a red test")
	code, _, _ = runCI(t, []string{"functional", t.TempDir()}, "")
	assert.NotEqual(t, 1, code, "functional has no exit 1; the summary must not claim every verb says no")
}

func ciRun(args []string, stdout, stderr io.Writer) int {
	return run(args, strings.NewReader(""), stdout, stderr)
}
