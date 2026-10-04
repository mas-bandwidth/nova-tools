package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusAfter returns the first word after token in out, and whether the token
// was found on any line: the status word that leads a typed line (STANDARD §2).
func statusAfter(out, token string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, token); ok && strings.HasPrefix(rest, " ") {
			rest = strings.TrimLeft(rest, " ")
			word, _, _ := strings.Cut(rest, " ")
			return word, true
		}
	}
	return "", false
}

// grammarTree is a scratch checkout with just a go.mod: what the scaffolding
// verbs dry-run against, so no test writes outside its own t.TempDir().
func grammarTree(t *testing.T) string {
	t.Helper()
	tree := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module example.com/m\n"), 0o644))
	return tree
}

// TestStatusGrammar holds the tool's one status grammar: after the verb's
// token the first word is OK, REFUSED or FAILED where the line carries a
// status, and the exit code tells the same truth (0 done, 1 the verb ran and
// said no, 2 could not run). Each row runs one outcome of one verb through
// run (the tool's Run function), or through the verb's own function where the
// fake is a parameter of it, with a fake or a fixture, and asserts the first
// word after the verb's token and the exit together, so a word that moved
// without its exit (or an exit without its word) fails the row (STANDARD §2).
//
// The rows cover each verb across the outcomes its exit table holds.
// functional, new-rule, new-verb, version and help carry no FAILED row: their
// table holds no exit 1, and a green row for an outcome the verb cannot have
// would be a claim no run made. Verbs that print finding lines or build
// information pin the token on the line their outcome leads; local OK is go
// test's own lowercase ok, which the PKG line repeats; the scaffold and
// receipt OK rows close on a NOTE continuation line, which STANDARD §2 names
// as the shape a continuation opens with. The receipt FAILED row drives the
// refused write through a fake store that answers the one XADD with WRONGTYPE
// (the refusal the functional tier drives against a real store), so the
// exit-1 line the verb prints is pinned by a run of the verb itself.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		token string
		run   func(t *testing.T) (int, string)
		word  string
		code  int
	}{
		{
			name:  "slowtests OK",
			token: "CI-SLOW",
			run: func(t *testing.T) (int, string) {
				stdin := "{\"Action\":\"pass\",\"Package\":\"example.com/pkg\",\"Test\":\"TestA\",\"Elapsed\":3.2}\n" +
					"{\"Action\":\"pass\",\"Package\":\"example.com/pkg\",\"Elapsed\":3.2}\n"
				code, out, _ := runCI(t, []string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2"}, stdin)
				return code, out
			},
			word: "OK",
			code: 0,
		},
		{
			name:  "slowtests REFUSED",
			token: "nova-ci slowtests",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"slowtests", "--budget", "0"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "slowtests FAILED",
			token: "CI-SLEEPS",
			run: func(t *testing.T) (int, string) {
				stdin := "{\"Action\":\"output\",\"Package\":\"example.com/pkg\",\"Test\":\"TestNew\",\"Output\":\"SLEEPS: x\\n\"}\n" +
					"{\"Action\":\"skip\",\"Package\":\"example.com/pkg\",\"Test\":\"TestNew\",\"Elapsed\":0}\n" +
					"{\"Action\":\"pass\",\"Package\":\"example.com/pkg\",\"Elapsed\":0.1}\n"
				code, out, _ := runCI(t, []string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2"}, stdin)
				return code, out
			},
			word: "test=TestNew",
			code: 1,
		},
		{
			name:  "local OK",
			token: "PKG",
			run: func(t *testing.T) (int, string) {
				f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: localGreenStream})
				code, out, _ := runLocal(t, f)
				return code, out
			},
			word: "ok",
			code: 0,
		},
		{
			name:  "local REFUSED",
			token: "nova-ci local",
			run: func(t *testing.T) (int, string) {
				f := localFixture(t, "./cmd/a\n")
				code, _, errb := runLocal(t, f, "--bse", "x")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "local FAILED",
			token: "PKG",
			run: func(t *testing.T) (int, string) {
				stream := "{\"Action\":\"run\",\"Package\":\"example.com/m/cmd/a\",\"Test\":\"TestB\"}\n" +
					"{\"Action\":\"output\",\"Package\":\"example.com/m/cmd/a\",\"Test\":\"TestB\",\"Output\":\"    b_test.go:9: got 1, want 2\\n\"}\n" +
					"{\"Action\":\"fail\",\"Package\":\"example.com/m/cmd/a\",\"Test\":\"TestB\",\"Elapsed\":0.1}\n" +
					"{\"Action\":\"fail\",\"Package\":\"example.com/m/cmd/a\",\"Elapsed\":0.2}\n"
				f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: stream, code: 2})
				code, out, _ := runLocal(t, f)
				return code, out
			},
			word: "FAILED",
			code: 1,
		},
		{
			name:  "functional OK",
			token: "CI FUNCTIONAL",
			run: func(t *testing.T) (int, string) {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n"), 0o644))
				code, out, _ := runCI(t, []string{"functional", dir}, "")
				return code, out
			},
			word: "OK",
			code: 0,
		},
		{
			name:  "functional REFUSED",
			token: "nova-ci functional",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"functional"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "new-rule OK",
			token: "nova-ci new-rule",
			run: func(t *testing.T) (int, string) {
				code, out, _ := runCI(t, []string{"new-rule", "--root", grammarTree(t), "--dry-run", "demo"}, "")
				return code, out
			},
			word: "NOTE",
			code: 0,
		},
		{
			name:  "new-rule REFUSED",
			token: "nova-ci new-rule",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"new-rule", "--root", grammarTree(t), "Bad Name"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "new-verb OK",
			token: "nova-ci new-verb",
			run: func(t *testing.T) (int, string) {
				tree := grammarTree(t)
				require.NoError(t, os.MkdirAll(filepath.Join(tree, "cmd", "demo"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(tree, "cmd", "demo", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
				code, out, _ := runCI(t, []string{"new-verb", "--root", tree, "--dry-run", "demo", "probe"}, "")
				return code, out
			},
			word: "NOTE",
			code: 0,
		},
		{
			name:  "new-verb REFUSED",
			token: "nova-ci new-verb",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"new-verb", "--root", grammarTree(t), "ghost", "probe"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "github receipt OK",
			token: "CI RECEIPT NOTE",
			run: func(t *testing.T) (int, string) {
				code, out, _ := runCI(t, []string{"github", "receipt", "--dry-run", "--from-runner", "--repo", "mas-bandwidth/nova-tools",
					"--sha", receiptSHA, "--run-id", "42", "--workflow", "CI", "--conclusion", "success"}, "")
				return code, out
			},
			word: "--dry-run:",
			code: 0,
		},
		{
			name:  "github receipt REFUSED",
			token: "nova-ci github receipt",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"github", "receipt", "--repo", "x"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "github receipt FAILED",
			token: "nova-ci github receipt",
			run: func(t *testing.T) (int, string) {
				var out, errb bytes.Buffer
				code := cmdReceipt(context.Background(), receiptArgs()[1:], &out, &errb, noEnv, refusedReceiptStore)
				return code, errb.String()
			},
			word: "FAILED:",
			code: 1,
		},
		{
			name:  "version OK",
			token: "nova-ci",
			run: func(t *testing.T) (int, string) {
				code, out, _ := runCI(t, []string{"version"}, "")
				return code, out
			},
			word: "devel",
			code: 0,
		},
		{
			name:  "version REFUSED",
			token: "nova-ci version",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"version", "extra"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
		{
			name:  "help OK",
			token: "nova-ci:",
			run: func(t *testing.T) (int, string) {
				code, out, _ := runCI(t, []string{"help"}, "")
				return code, out
			},
			word: "test-time",
			code: 0,
		},
		{
			name:  "help REFUSED",
			token: "nova-ci",
			run: func(t *testing.T) (int, string) {
				code, _, errb := runCI(t, []string{"help", "bogus"}, "")
				return code, errb
			},
			word: "REFUSED:",
			code: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, line := tc.run(t)
			word, ok := statusAfter(line, tc.token)
			require.True(t, ok, "output lacks token %q on any line:\n%s", tc.token, line)
			assert.Equal(t, tc.word, word, "first word after %q = %q, want %q (exit %d)\n%s", tc.token, word, tc.word, code, line)
			assert.Equal(t, tc.code, code, "exit = %d, want %d for status word %q", code, tc.code, tc.word)
		})
	}
}

// storeReply is an error the store itself replied with.
type storeReply string

func (e storeReply) Error() string { return string(e) }
func (storeReply) RedisError()     {}

// wrongType is what a store answers when the stream the receipt appends to
// holds a string: the refusal the functional tier drives against a real
// store (receipt_functional_test.go).
var wrongType = storeReply("WRONGTYPE Operation against a key holding the wrong kind of value")

// refusedReceiptStore is the receipt verb's failing store, handed through the
// receiptOpener seam: a client that answers every command with WRONGTYPE, so
// the refused write runs the verb's real path to its FAILED line and exit 1.
// refusedHook answers without calling the next hook, so the client dials
// nothing (STANDARD §8: unit tests own no sockets).
func refusedReceiptStore(ctx context.Context, addr string) (*store.Store, error) {
	c := redis.NewClient(&redis.Options{Addr: addr})
	c.AddHook(refusedHook{})
	return store.New(c), nil
}

// refusedHook answers every command with WRONGTYPE and never passes one on,
// so no connection is dialed and no command reaches a store.
type refusedHook struct{}

func (refusedHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (refusedHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		cmd.SetErr(wrongType)
		return wrongType
	}
}

func (refusedHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			cmd.SetErr(wrongType)
		}
		return wrongType
	}
}
