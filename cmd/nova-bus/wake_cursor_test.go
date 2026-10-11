package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func appendWake(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(text)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func realWakeCLI(r *rig) testkit.Main {
	w := r.world()
	w.wakeArm, w.wakeLine = realWakeArm, realWakeLine
	return wakeCLI(w)
}

func wakeCLI(w world) testkit.Main {
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
}

// The returned file cursor survives a bus race, the disarm/rearm gap and a
// fresh process world. Only a complete returned record moves it (WakeCursor).
func TestWaitWakeCursorPreservesGapAndRestartRecords(t *testing.T) {
	t.Parallel()
	for _, initial := range []string{"missing", "empty"} {
		t.Run(initial, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wake")
			if initial == "empty" {
				require.NoError(t, os.WriteFile(path, nil, 0o600))
			}
			r := newRig("ada", "bob")
			w := r.world()
			w.wakeArm = realWakeArm
			firstLook := true
			w.wakeLine = func(path string, c wakeCursor) (string, wakeCursor, error) {
				line, next, err := realWakeLine(path, c)
				if firstLook {
					firstLook = false
					appendWake(t, path, "racing\n")
					drop(r, "ada", "01JRACE", "race")
				}
				return line, next, err
			}
			first := wakeCLI(w).Do(t, "wait", "--as", "bob", "--wake-file", path, "--json").Exit(0)
			var v waitJSON
			require.NoError(t, json.Unmarshal([]byte(first.Stdout), &v))
			require.Equal(t, "OK", v.Word)
			require.NotNil(t, v.WakeOffset)
			require.Zero(t, *v.WakeOffset, "bus return cannot consume the concurrent wake")
			saved := v.WakeAfter
			appendWake(t, path, "in-gap\npartial")
			restarted := newRig("ada", "bob")
			restarted.wireClock()
			second := realWakeCLI(restarted).Do(t, "wait", "--as", "bob", "--after", v.After, "--wake-file", path, "--wake-after", saved, "--timeout", "1s", "--json").Exit(0)
			require.NoError(t, json.Unmarshal([]byte(second.Stdout), &v))
			require.NotNil(t, v.Wake)
			assert.Equal(t, "racing", v.Wake.Line)
			require.Equal(t, int64(7), *v.WakeOffset)
			thirdRig := newRig("ada", "bob")
			thirdRig.wireClock()
			third := realWakeCLI(thirdRig).Do(t, "wait", "--as", "bob", "--after", v.After, "--wake-file", path, "--wake-after", v.WakeAfter, "--timeout", "1s", "--json").Exit(0)
			require.NoError(t, json.Unmarshal([]byte(third.Stdout), &v))
			assert.Equal(t, "in-gap", v.Wake.Line)
			assert.Equal(t, int64(14), *v.WakeOffset)
			c, err := parseWakeCursor(v.WakeAfter)
			require.NoError(t, err)
			line, unchanged, err := realWakeLine(path, c)
			require.NoError(t, err)
			assert.Empty(t, line)
			assert.Equal(t, c, unchanged)
			appendWake(t, path, "-complete\n")
			line, next, err := realWakeLine(path, c)
			require.NoError(t, err)
			assert.Equal(t, "partial-complete", line)
			assert.Equal(t, int64(31), next.Offset)
		})
	}
}

func TestWakeCursorRefusesChangedOrUnsupportedFiles(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"same-size replacement", "truncated", "rewritten prefix", "disappeared", "directory"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wake")
			require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o600))
			c, err := realWakeArm(path, "")
			require.NoError(t, err)
			switch kind {
			case "same-size replacement":
				replacement := path + ".new"
				require.NoError(t, os.WriteFile(replacement, []byte("replaced\n"), 0o600))
				require.NoError(t, os.Rename(replacement, path))
			case "truncated":
				require.NoError(t, os.Truncate(path, 1))
			case "rewritten prefix":
				require.NoError(t, os.WriteFile(path, []byte("replaced\n"), 0o600))
			case "disappeared":
				require.NoError(t, os.Remove(path))
			case "directory":
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Mkdir(path, 0o700))
			}
			_, err = realWakeArm(path, c.token())
			require.Error(t, err)
			_, next, err := realWakeLine(path, c)
			assert.Error(t, err)
			assert.Equal(t, c, next)
		})
	}
}

func TestWakeCursorValidationAndBoundedRecords(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.cli().Do(t, "wait", "--as", "bob", "--wake-after", "bad").Exit(2).Err("--wake-after wants --wake-file", "complete wake-after cursor")
	for _, raw := range []string{"", "!", "e30", strings.Repeat("A", 2000)} {
		_, err := parseWakeCursor(raw)
		assert.Error(t, err)
	}
	path := filepath.Join(t.TempDir(), "wake")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	c, err := realWakeArm(path, "")
	require.NoError(t, err)
	appendWake(t, path, strings.Repeat("x", wakeLineMax))
	_, next, err := realWakeLine(path, c)
	assert.ErrorContains(t, err, "newline")
	assert.Equal(t, c, next)
}

func TestWaitWakeTimeoutKeepsPartialRecordCursor(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.wireClock()
	path := filepath.Join(t.TempDir(), "wake")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	w := r.world()
	w.wakeArm = realWakeArm
	first := true
	w.wakeLine = func(path string, c wakeCursor) (string, wakeCursor, error) {
		if first {
			first = false
			appendWake(t, path, "part")
		}
		return realWakeLine(path, c)
	}
	out := wakeCLI(w).Do(t, "wait", "--as", "bob", "--wake-file", path, "--timeout", "1s", "--json").Exit(1)
	var v waitJSON
	require.NoError(t, json.Unmarshal([]byte(out.Stdout), &v))
	assert.Equal(t, "NONE", v.Word)
	require.NotNil(t, v.WakeOffset)
	assert.Zero(t, *v.WakeOffset)
	appendWake(t, path, "ial\n")
	next := realWakeCLI(newRig("ada", "bob")).Do(t, "wait", "--as", "bob", "--after", v.After, "--wake-file", path, "--wake-after", v.WakeAfter, "--json").Exit(0)
	require.NoError(t, json.Unmarshal([]byte(next.Stdout), &v))
	assert.Equal(t, "partial", v.Wake.Line)
	assert.Equal(t, int64(8), *v.WakeOffset)
}

// The explicit zero seed is native migration, never a manufactured cursor.
// Existing records, including the event preceding the first rearm, are shown.
func TestWaitWakeZeroBootstrapKeepsRecordsBeforeFirstRearm(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "wake")
	appendWake(t, path, "already-retained\npending-before-rearm\nfragment")
	r := newRig("ada", "bob")
	r.wireClock()
	out := realWakeCLI(r).Do(t, "wait", "--as", "bob", "--wake-file", path, "--wake-after", "0", "--timeout", "1s", "--json").Exit(0)
	var v waitJSON
	require.NoError(t, json.Unmarshal([]byte(out.Stdout), &v))
	require.NotNil(t, v.Wake)
	assert.Equal(t, "already-retained", v.Wake.Line)
	assert.Equal(t, int64(17), *v.WakeOffset)
	c, err := parseWakeCursor(v.WakeAfter)
	require.NoError(t, err)
	assert.NotEmpty(t, c.Identity)
	r = newRig("ada", "bob")
	r.wireClock()
	next := realWakeCLI(r).Do(t, "wait", "--as", "bob", "--after", v.After, "--wake-file", path, "--wake-after", v.WakeAfter, "--timeout", "1s", "--json").Exit(0)
	require.NoError(t, json.Unmarshal([]byte(next.Stdout), &v))
	assert.Equal(t, "pending-before-rearm", v.Wake.Line)
	assert.Equal(t, int64(38), *v.WakeOffset)
}
