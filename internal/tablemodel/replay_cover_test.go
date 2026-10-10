package tablemodel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit tier reaches the replay's pure edges without a store: the action a
// step names, the hash of the source and the model modules, the file copy, the
// refusal each entry point gives a missing input, and the harness run through
// the injected executor. capture and replayReceipts run their calls against a
// store and are exercised only by the functional replay.

func TestReplayCoverActionIsTheStepsVerbArgsAndActor(t *testing.T) {
	t.Parallel()
	step := TraceStep{Verb: "cell_add", Args: []string{"t1", "r1", "c1", "1", "m1"}, Actor: "w1"}
	require.Equal(t, Action{Verb: "cell_add", Args: []string{"t1", "r1", "c1", "1", "m1"}, Actor: "w1"}, step.action())
}

func TestReplayCoverSumIsTheSHA256OfItsBytes(t *testing.T) {
	t.Parallel()
	require.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", sum(nil))
	require.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", sum([]byte("hello")))
}

func TestReplayCoverHashModelsHashesEveryModule(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "A.tla"), []byte("one"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "B.tla"), []byte("two"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.cfg"), []byte("ignored"), 0o644))
	got, err := hashModels(dir)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"A.tla": sum([]byte("one")), "B.tla": sum([]byte("two"))}, got)
	// A directory named like a module cannot be read, so the hash is refused.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "C.tla"), 0o755))
	_, err = hashModels(dir)
	require.Error(t, err)
}

func TestReplayCoverCopyFilesCopiesEveryPattern(t *testing.T) {
	t.Parallel()
	src, dst := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "a.tla"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "b.cfg"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "c.txt"), []byte("c"), 0o644))
	require.NoError(t, copyFiles(src, dst, "*.tla", "*.cfg"))
	for name, want := range map[string]string{"a.tla": "a", "b.cfg": "b"} {
		raw, err := os.ReadFile(filepath.Join(dst, name))
		require.NoError(t, err, name)
		require.Equal(t, want, string(raw), name)
	}
	tassert.NoFileExists(t, filepath.Join(dst, "c.txt"))
	// A malformed pattern is refused rather than silently copying nothing.
	require.Error(t, copyFiles(src, dst, "["))
}

func TestReplayCoverRunReplayRefusesMissingInputs(t *testing.T) {
	t.Parallel()
	models := filepath.Join("..", "..", "tla")
	source := filepath.Join("testdata", "table-pinned.lua")
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	tests := map[string]ReplayOptions{
		"a missing source":                   {Source: filepath.Join(t.TempDir(), "no.lua"), Models: models, Dir: t.TempDir()},
		"a missing model dir":                {Source: source, Models: filepath.Join(t.TempDir(), "none"), Dir: t.TempDir()},
		"a model path that is not a pattern": {Source: source, Models: "[", Dir: t.TempDir()},
		"an output dir that is a file":       {Source: source, Models: models, Dir: file},
	}
	for name, o := range tests {
		var c *CannotRun
		err := RunReplay(o)
		require.ErrorAs(t, err, &c, "%s: error = %v, want CannotRun", name, err)
	}
}

func TestReplayCoverRunHarnessAcceptsTheInjectedExecutorsResult(t *testing.T) {
	t.Parallel()
	trace := loadTrace(t)
	models := filepath.Join("..", "..", "tla")
	for _, mutate := range []bool{false, true} {
		label := "execution"
		code := tlc.ExitPass
		out := "5 states generated, 5 distinct states found, 0 states left on queue.\nModel checking completed. No error has been found.\n"
		if mutate {
			label = "mutated-observation"
			code = tlc.ExitInvariant
			out = "Error: Invariant MatchesExecution is violated.\n5 states generated, 5 distinct states found, 1 states left on queue.\n"
		}
		dir := t.TempDir()
		var got tlc.Run
		exec := func(ctx context.Context, r tlc.Run, log string) int {
			got = r
			require.NoError(t, os.WriteFile(log, []byte(out), 0o644))
			return code
		}
		log, err := runHarness(context.Background(), ReplayOptions{Models: models, Exec: exec}, dir, label, mutate, trace)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, label+".log"), log)
		require.Equal(t, HarnessName+".cfg", got.Config)
		require.Equal(t, HarnessName+".tla", got.Module)
		require.Equal(t, 1, got.Workers)
		tassert.FileExists(t, filepath.Join(dir, label, HarnessName+".tla"))
		tassert.FileExists(t, filepath.Join(dir, label, HarnessName+".cfg"))
	}
}

func TestReplayCoverRunHarnessRefusesAnUnacceptedResult(t *testing.T) {
	t.Parallel()
	trace := loadTrace(t)
	dir := t.TempDir()
	exec := func(ctx context.Context, r tlc.Run, log string) int {
		require.NoError(t, os.WriteFile(log, []byte("1 states generated, 1 distinct states found, 0 states left on queue.\n"), 0o644))
		return tlc.ExitPass
	}
	_, err := runHarness(context.Background(), ReplayOptions{Models: filepath.Join("..", "..", "tla"), Exec: exec}, dir, "execution", false, trace)
	require.ErrorContains(t, err, "execution harness")
}
