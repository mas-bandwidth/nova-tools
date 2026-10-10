package secrets

// The unit cover for seal.go's value reader, review classifier, child error and
// carry refusals. Every test here runs with no child process: readSealValue takes
// its reader from SealOptions.Stdin, classifySealReview is pure, and carry takes
// an execCommand, for which seam_test.go's strict scripted fake stands in and
// seal_terminal_test.go's fake clock advances the poll without a real sleep.
// readSealFromTTY, disableEcho and runStty (a controlling terminal and the stty
// program), checkSeatDecrypts (RunCheck runs real sops), and the default child
// runner opts.exec() falls back to stay outside the unit tier, as do markVersion
// and sealSopsEnv (a package variable and the process environment a parallel test
// must not race).

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The carry fixtures: a seat file, the branch and commit a seal makes, and the
// pull request body. The names only have to be stable, not real.
const (
	sealUnitSeat    = "rowan.yaml"
	sealUnitBranch  = "seal/rowan-TOKEN-20261010-120000"
	sealUnitMessage = "seal TOKEN into rowan.yaml"
	sealUnitBody    = "Sealed with a test."
)

// TestSecretsSealCoverChildError pins the carrier a helper child's failure travels
// in: Error is the inner message, errors.Is reaches the inner error through Unwrap,
// and Stderr answers the transcript the child wrote.
func TestSecretsSealCoverChildError(t *testing.T) {
	t.Parallel()

	inner := errors.New("the child exited 3")
	ce := &childError{err: inner, stderr: "sops: wrong key\n"}
	assert.Equal(t, "the child exited 3", ce.Error(), "Error is the inner error's message")
	assert.ErrorIs(t, ce, inner, "Unwrap reaches the inner error")
	assert.Equal(t, "sops: wrong key\n", ce.Stderr(), "Stderr answers the child's stderr")
}

// TestSecretsSealCoverReadSealValue pins every refusal readSealValue makes over a
// reader it is handed, and the terminal read that takes its line and reads no
// further.
func TestSecretsSealCoverReadSealValue(t *testing.T) {
	t.Parallel()

	errSentinel := errors.New("stdin read exploded")
	tests := []struct {
		name    string
		opts    SealOptions
		want    string
		wantErr string
		wantIs  error
	}{
		{"a line answers its value", SealOptions{Stdin: strings.NewReader("v\n")}, "v", "", nil},
		{"no newline is the Ctrl-D refusal", SealOptions{Stdin: strings.NewReader("v")}, "",
			"the value must end with Enter, and Ctrl-D is not a value", nil},
		{"empty is the empty refusal", SealOptions{Stdin: strings.NewReader("")}, "",
			"empty value: refusing to seal nothing", nil},
		{"a second line is the multi-line refusal", SealOptions{Stdin: strings.NewReader("v\nw\n")}, "",
			"value is multi-line", nil},
		{"a second line after a carriage return is the multi-line refusal", SealOptions{Stdin: strings.NewReader("a\rb\n")}, "",
			"value is multi-line", nil},
		{"a NUL byte is the NUL refusal", SealOptions{Stdin: strings.NewReader("a\x00b\n")}, "",
			"value contains a NUL byte", nil},
		{"a reader that fails at once names the read", SealOptions{Stdin: iotest.ErrReader(errSentinel)}, "",
			"unable to read value from stdin", errSentinel},
		{"a reader that fails after the line wraps the read", SealOptions{Stdin: io.MultiReader(strings.NewReader("v\n"), iotest.ErrReader(errSentinel))}, "",
			"unable to read value from stdin", errSentinel},
		{"--stdin on a terminal takes the line and reads no further",
			SealOptions{UseStdin: true, StdinIsTerminal: true, Stdin: strings.NewReader("v\nmore\n")}, "v", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			value, err := readSealValue(tt.opts)
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.want, value)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			if tt.wantIs != nil {
				assert.ErrorIs(t, err, tt.wantIs, "the read error is wrapped, not replaced")
			}
		})
	}
}

// TestSecretsSealCoverClassifySealReview pins the classifier's terminal states and
// remedies, the check-failure gate, the approved continue and the pending poll.
func TestSecretsSealCoverClassifySealReview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		raw          string
		wantErr      string
		wantApproved bool
	}{
		{"invalid JSON is a response error", "{", "response invalid", false},
		{"CLOSED is a terminal state with a reopen remedy", `{"state":"CLOSED"}`, "was closed without merging", false},
		{"MERGED is a terminal state with a pull remedy", `{"state":"MERGED"}`, "was merged elsewhere", false},
		{"a check in ERROR with no name is the gate", `{"state":"OPEN","statusCheckRollup":[{"state":"ERROR"}]}`, "check gate failed", false},
		{"a check that timed out is named", `{"state":"OPEN","statusCheckRollup":[{"name":"seat-rule","conclusion":"TIMED_OUT"}]}`, "check seat-rule failed", false},
		{"a JSON approval continues", `{"state":"OPEN","reviewDecision":"APPROVED"}`, "", true},
		{"the plain word approved continues", "approved", "", true},
		{"changes requested stops with a view remedy", "CHANGES_REQUESTED", "changes requested", false},
		{"failed is a check failure", "FAILED", "check gate failed", false},
		{"pending polls", "PENDING", "", false},
		{"empty polls", "", "", false},
		{"a bare comment polls", "COMMENTED", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err, approved := classifySealReview(tt.raw, "42")
			assert.Equal(t, tt.wantApproved, approved)
			if tt.wantErr == "" {
				require.NoError(t, err, "no terminal error expected")
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestSecretsSealCoverCarryRefusals pins the road's refusals over the strict
// scripted fake and a t.TempDir() store: a store with no commit, a detached HEAD,
// a dirty store, a create with no pull number, the two view remedies, and the
// combined error when the return to the home branch fails too. Every script must
// be consumed whole.
func TestSecretsSealCoverCarryRefusals(t *testing.T) {
	t.Parallel()

	opens := func() []scriptedStep {
		return []scriptedStep{
			{name: "git", args: []string{"rev-parse", "--abbrev-ref", "HEAD"}, out: "main\n"},
			{name: "git", args: []string{"status", "--porcelain", "-uno"}},
			{name: "git", args: []string{"checkout", "-b", sealUnitBranch}},
			{name: "git", args: []string{"add", sealUnitSeat}},
			{name: "git", args: []string{"commit", "-m", sealUnitMessage}},
		}
	}
	push := scriptedStep{name: "git", args: []string{"push", "-u", "origin", sealUnitBranch}}
	create := func(out string) scriptedStep {
		return scriptedStep{name: "gh", args: []string{"pr", "create", "--head", sealUnitBranch, "--title", sealUnitMessage, "--body", sealUnitBody}, out: out}
	}
	view := func(err error) scriptedStep {
		return scriptedStep{name: "gh", args: []string{"pr", "view", "42", "--json", "reviewDecision,state,statusCheckRollup"}, err: err}
	}
	restore := scriptedStep{name: "git", args: []string{"checkout", "-f", "main"}}
	restoreErr := scriptedStep{name: "git", args: []string{"checkout", "-f", "main"}, err: fakeExit(9)}

	tests := []struct {
		name      string
		steps     []scriptedStep
		wantErr   []string
		wantCalls int
	}{
		{"rev-parse fails: a store with no commit", []scriptedStep{
			{name: "git", args: []string{"rev-parse", "--abbrev-ref", "HEAD"}, err: fakeExit(128)},
		}, []string{"a store with no commit yet has no branch to return to", "git rev-parse failed"}, 1},
		{"rev-parse answers HEAD: not on a branch", []scriptedStep{
			{name: "git", args: []string{"rev-parse", "--abbrev-ref", "HEAD"}, out: "HEAD\n"},
		}, []string{"is not on a branch"}, 1},
		{"a non-empty status is not clean", []scriptedStep{
			{name: "git", args: []string{"rev-parse", "--abbrev-ref", "HEAD"}, out: "main\n"},
			{name: "git", args: []string{"status", "--porcelain", "-uno"}, out: " M rowan.yaml\n"},
		}, []string{"is not clean"}, 2},
		{"gh pr create answers no pull number", append(opens(), push, create("opened without a number\n"), restore),
			[]string{"gh pr create did not return a pull request number"}, 8},
		{"gh pr view not found names the list remedy", append(opens(), push, create("https://example.com/x/pull/42\n"), view(errors.New("not found")), restore),
			[]string{"not found", "run: gh pr list"}, 9},
		{"gh pr view fails another way names the view remedy", append(opens(), push, create("https://example.com/x/pull/42\n"), view(errors.New("boom")), restore),
			[]string{"review query failed", "run: gh pr view 42"}, 9},
		{"a failing checkout -f names both errors", []scriptedStep{
			{name: "git", args: []string{"rev-parse", "--abbrev-ref", "HEAD"}, out: "main\n"},
			{name: "git", args: []string{"status", "--porcelain", "-uno"}},
			{name: "git", args: []string{"checkout", "-b", sealUnitBranch}},
			{name: "git", args: []string{"add", sealUnitSeat}, err: fakeExit(7)},
			restoreErr,
		}, []string{"git add failed", "also failed to return the store to main"}, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			run, calls := scripted(t, tt.steps...)
			c := sealCarry{
				run: run, storeDir: t.TempDir(), gitPath: "git", ghPath: "gh",
				seatFile: sealUnitSeat, branch: sealUnitBranch,
				message: sealUnitMessage, title: sealUnitMessage, body: sealUnitBody,
			}
			pr, merged, err := c.carry([]byte("ENC[cover]\n"))
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			assert.Empty(t, pr, "a refusal returns no pull request number")
			assert.False(t, merged, "a refusal does not merge")
			assert.Equal(t, tt.wantCalls, *calls, "the script must be consumed whole")
		})
	}
}

// TestSecretsSealCoverCarryPollsToTheDeadline pins the poll a REVIEW_REQUIRED
// review rides to the two-minute bound under the fake clock: the pull number, no
// merge, and the returned-open result, with the whole script consumed.
func TestSecretsSealCoverCarryPollsToTheDeadline(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	steps := []scriptedStep{
		{name: "git", args: []string{"rev-parse", "--abbrev-ref", "HEAD"}, out: "main\n"},
		{name: "git", args: []string{"status", "--porcelain", "-uno"}},
		{name: "git", args: []string{"checkout", "-b", sealUnitBranch}},
		{name: "git", args: []string{"add", sealUnitSeat}},
		{name: "git", args: []string{"commit", "-m", sealUnitMessage}},
		{name: "git", args: []string{"push", "-u", "origin", sealUnitBranch}},
		{name: "gh", args: []string{"pr", "create", "--head", sealUnitBranch, "--title", sealUnitMessage, "--body", sealUnitBody}, out: "https://example.com/x/pull/42\n"},
	}
	// The loop polls once at start and once every five seconds to the 120s bound:
	// 25 views, the last one after the bound is reached, so 24 sleeps of 5s. There
	// is no merge step in the script; a merge would fail the strict fake.
	for i := 0; i < 25; i++ {
		steps = append(steps, scriptedStep{
			name: "gh", args: []string{"pr", "view", "42", "--json", "reviewDecision,state,statusCheckRollup"},
			out: `{"state":"OPEN","reviewDecision":"REVIEW_REQUIRED"}`,
		})
	}
	steps = append(steps, scriptedStep{name: "git", args: []string{"checkout", "-f", "main"}})

	clock := newTerminalFakeClock(start)
	run, calls := scripted(t, steps...)
	c := sealCarry{
		run: run, storeDir: t.TempDir(), gitPath: "git", ghPath: "gh",
		seatFile: sealUnitSeat, branch: sealUnitBranch,
		message: sealUnitMessage, title: sealUnitMessage, body: sealUnitBody,
		now: clock.Now, sleep: clock.Sleep,
	}
	pr, merged, err := c.carry([]byte("ENC[cover]\n"))
	require.NoError(t, err, "an unapproved review is an open pull request, not an error")
	assert.Equal(t, "42", pr)
	assert.False(t, merged, "an unapproved review never merges")
	sleepCalls, slept := clock.stats()
	assert.Equal(t, 24, sleepCalls, "the poll sleeps to the two-minute bound")
	assert.Equal(t, 120*time.Second, slept, "the fake clock advanced to the bound without a real sleep")
	assert.Equal(t, 33, *calls, "the script must be consumed whole")
}

// TestSecretsSealCoverSealDecryptAndAtomicWrite pins the two filesystem refusals a
// unit test reaches with no child: sealDecrypt returns the stat error under a
// regular file rather than the absent-seat refusal, and atomicWriteFile names a
// write it could not make.
func TestSecretsSealCoverSealDecryptAndAtomicWrite(t *testing.T) {
	t.Parallel()

	t.Run("sealDecrypt returns the stat error under a regular file", func(t *testing.T) {
		t.Parallel()
		plain := filepath.Join(t.TempDir(), "plain")
		require.NoError(t, os.WriteFile(plain, []byte("x"), 0o644))
		run, calls := scripted(t) // no step is scripted: a child would fail the test
		_, err := sealDecrypt(run, "sops", "key", filepath.Join(plain, "rowan.yaml"))
		require.Error(t, err)
		assert.False(t, errors.Is(err, errSeatFileAbsent), "a stat error is not the absent-seat refusal: %v", err)
		assert.Equal(t, 0, *calls, "no child runs for a stat that failed")
	})

	t.Run("atomicWriteFile into a regular file names the write", func(t *testing.T) {
		t.Parallel()
		plain := filepath.Join(t.TempDir(), "plain")
		require.NoError(t, os.WriteFile(plain, []byte("x"), 0o644))
		err := atomicWriteFile(filepath.Join(plain, "rowan.yaml"), []byte("ENC[cover]\n"), 0o600)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to write")
	})
}
