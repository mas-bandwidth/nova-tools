package secrets

// The strict scripted fake behind the one seam: each test writes the exact argv it
// expects and the result, and any other child fails the test. The refusals below are
// the ones a real sops, age-keygen or ssh produced, which only a functional run
// reached before the seam carried them into the unit tier.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testguard"
)

// wildcard stands for any one argv word a scripted step cannot predict, such as the
// snapshot path place hands sops -d.
const wildcard = "*"

// scriptedStep is one expected child: the program, its argv (wildcard allows one word at
// that position) and the result the real tool would give.
type scriptedStep struct {
	name string
	args []string
	out  string
	err  error
}

// failer is the little of testing.TB the strict fake needs, so its own test can hand it
// a recorder that captures a failure instead of failing the test that runs it.
type failer interface {
	Helper()
	Errorf(format string, args ...any)
}

// scriptedExec is a fake whose steps run in order. Any child the script does not name,
// or a named child whose argv differs, fails the test: a fake that says yes to anything
// is how a green test hides a broken verb.
type scriptedExec struct {
	t     failer
	steps []scriptedStep
	i     int
}

// fakeExit is a scripted child's non-zero exit: the exit code the real tool returned.
type fakeExit int

func (e fakeExit) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e fakeExit) ExitCode() int { return int(e) }

// scripted returns the strict fake and a pointer to its step counter, so a test can
// assert the whole script was consumed.
func scripted(f failer, steps ...scriptedStep) (execCommand, *int) {
	f.Helper()
	s := &scriptedExec{t: f, steps: steps}
	return s.run, &s.i
}

func (s *scriptedExec) run(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
	s.t.Helper()
	if s.i >= len(s.steps) {
		s.t.Errorf("unexpected child %s %s: the script has no step left", name, strings.Join(args, " "))
		return nil, errors.New("scripted fake: unexpected child")
	}
	step := s.steps[s.i]
	s.i++
	if name != step.name || !argsMatch(step.args, args) {
		s.t.Errorf("child %d = %s %s, want %s %s", s.i, name, strings.Join(args, " "), step.name, strings.Join(step.args, " "))
		return nil, errors.New("scripted fake: wrong child")
	}
	if stdin != nil {
		_, _ = io.Copy(io.Discard, stdin)
	}
	return []byte(step.out), step.err
}

// argsMatch reports whether got matches want, where want's wildcard matches exactly one
// word of got.
func argsMatch(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != wildcard && want[i] != got[i] {
			return false
		}
	}
	return true
}

// executable writes a file with the executable bit set. The scripted fake replaces what
// it runs, so the bytes are never a program; the bit is what filepathIsExecutable reads.
func executable(t *testing.T, path string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	return path
}

// TestSealEncryptFailureRefusalNamesTheChildExit pins the refusal a real sops encrypt
// produced: the exit code the tool returned, the seat file, and the --dry-run remedy,
// with the value on stdin and never in argv. Only a blue bench reached it before.
func TestSealEncryptFailureRefusalNamesTheChildExit(t *testing.T) {
	t.Parallel()

	run, calls := scripted(t, scriptedStep{
		name: "sops",
		args: []string{"-e", "--filename-override", "rowan.yaml", "--input-type", "yaml", "--output-type", "yaml", "/dev/stdin"},
		err:  fakeExit(7),
	})
	const value = "topsecret-value"
	_, _, err := sealEncrypt(run, "sops", "/keys/rowan.key", "/store", "rowan.yaml", "seal", []byte("TARGET: "+value+"\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sops encrypt failed: exit 7", "the refusal does not name the exit sops returned: %v", err)
	assert.Contains(t, err.Error(), "rowan.yaml", "the refusal does not name the seat file: %v", err)
	assert.Contains(t, err.Error(), "--dry-run", "the refusal carries no remedy: %v", err)
	assert.NotContains(t, err.Error(), value, "the refusal carries the value: %v", err)
	assert.Equal(t, 1, *calls, "the fake ran %d children, want exactly the encrypt", *calls)
}

// TestSealDecryptFailureRefusalNamesTheChildExit pins the refusal a real sops -d
// produced: the exit code, the store's file and the inspect remedy, never a transcript.
func TestSealDecryptFailureRefusalNamesTheChildExit(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "rowan.yaml")
	require.NoError(t, os.WriteFile(file, []byte("ENC[old]\n"), 0o600))
	run, calls := scripted(t, scriptedStep{name: "sops", args: []string{"-d", file}, err: fakeExit(3)})
	_, err := sealDecrypt(run, "sops", "/keys/rowan.key", file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sops failed: exit 3", "the refusal does not name the exit sops returned: %v", err)
	assert.Contains(t, err.Error(), file, "the refusal does not name the store's file: %v", err)
	assert.Contains(t, err.Error(), "sops -d", "the refusal carries no inspect remedy: %v", err)
	assert.Equal(t, 1, *calls, "the fake ran %d children, want exactly the decrypt", *calls)
}

// TestKeygenRefusalNamesTheAgeKeygenExit pins the refusal a real age-keygen produced
// when it failed to write the key: the exit code, the transcript withheld, and the
// version probe reaching the same seam first.
func TestKeygenRefusalNamesTheAgeKeygenExit(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture relies on POSIX mode bits for the key directory")
	}

	dir := t.TempDir()
	keyDir := filepath.Join(dir, "keys")
	require.NoError(t, os.Mkdir(keyDir, 0o700))
	keyPath := filepath.Join(keyDir, "rowan.key")
	ageKeygen := executable(t, filepath.Join(dir, "age-keygen"))

	run, calls := scripted(t,
		scriptedStep{name: ageKeygen, args: []string{"--version"}, out: "1.3.2\n"},
		scriptedStep{name: ageKeygen, args: []string{"-o", keyPath}, err: fakeExit(2)},
	)
	_, err := runKeygen(run, "rowan", keyPath, ageKeygen, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "age-keygen failed: exit 2", "the refusal does not name the exit age-keygen returned: %v", err)
	assert.Contains(t, err.Error(), "transcript withheld", "the refusal does not say the transcript is withheld: %v", err)
	assert.Equal(t, 2, *calls, "the fake ran %d children, want the probe and the generation", *calls)
}

// placeSeamFixture lays out what RunPlace reads before it reaches the ssh child: a
// store shape, a mode-0600 key in a mode-0700 directory, a sealed seat file, a fleet
// registry naming the machine, and an executable sops so the version probe runs.
type placeSeamFixture struct {
	storeDir string
	keyPath  string
	sopsPath string
	machines string
	receipts string
	target   string
}

func newPlaceSeamFixture(t *testing.T) placeSeamFixture {
	t.Helper()
	dir := t.TempDir()
	f := placeSeamFixture{
		storeDir: filepath.Join(dir, "store"),
		machines: filepath.Join(dir, "fleet.tsv"),
		receipts: filepath.Join(dir, "receipts"),
		target:   "web-1.invalid",
	}
	require.NoError(t, os.MkdirAll(filepath.Join(f.storeDir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.storeDir, ".sops.yaml"),
		[]byte("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: age1seat,age1recovery\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.storeDir, "rowan.yaml"), []byte("ENC[sealed]\n"), 0o600))

	keyDir := filepath.Join(dir, "keys")
	require.NoError(t, os.Mkdir(keyDir, 0o700))
	f.keyPath = filepath.Join(keyDir, "rowan.key")
	require.NoError(t, os.WriteFile(f.keyPath, []byte("AGE-SECRET-KEY-1TEST\n"), 0o600))
	f.sopsPath = executable(t, filepath.Join(dir, "sops"))

	require.NoError(t, os.WriteFile(f.machines, []byte("web-1\t"+f.target+"\t/srv/web\n"), 0o600))
	return f
}

// TestPlaceSSHDeliveryRefusalNamesTheChildExit pins the refusal a real ssh produced
// when the delivery failed: the target and the exit code, the value on stdin and never
// in argv, and no receipt written for a delivery that did not happen.
func TestPlaceSSHDeliveryRefusalNamesTheChildExit(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture relies on POSIX mode bits for the key directory")
	}

	f := newPlaceSeamFixture(t)
	// The seam is faked, but the guard still sees the ssh command line: declare the fake
	// the test installs as the allowed seam, as testguard names.
	defer testguard.AllowHosts()()
	remotePath := "/srv/web/.config/nova-secrets/TOKEN.env"
	remoteCmd := "umask 077 && set -e && mkdir -p \"$(dirname " + shSingleQuote(remotePath) + ")\" && cat > " +
		shSingleQuote(remotePath) + " && chmod 600 " + shSingleQuote(remotePath)
	var gotStdin string
	run, calls := scripted(t,
		scriptedStep{name: f.sopsPath, args: []string{"--version", "--disable-version-check"}, out: "sops 3.13.3\n"},
		scriptedStep{name: f.sopsPath, args: []string{"-d", wildcard}, out: "TOKEN: delivered\n"},
		scriptedStep{name: "ssh", args: []string{f.target, remoteCmd}, err: fakeExit(5)},
	)
	send := func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
		if name == "ssh" {
			b, _ := io.ReadAll(stdin)
			gotStdin = string(b)
			for _, a := range args {
				assert.NotContains(t, a, "delivered", "the value leaked into ssh argv: %q", a)
			}
		}
		return run(stdin, env, dir, name, args...)
	}

	_, err := RunPlace(PlaceInput{
		StoreDir: f.storeDir, AsName: "rowan", KeyPath: f.keyPath, SopsPath: f.sopsPath,
		Machine: "web-1", Secret: "TOKEN", RemotePath: remotePath,
		Machines: f.machines, Receipts: f.receipts, SSH: "ssh", Exec: send,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh to "+f.target+" failed: exit 5", "the refusal does not name the target and exit: %v", err)
	assert.Contains(t, err.Error(), "transcript withheld", "the refusal does not say the transcript is withheld: %v", err)
	assert.Equal(t, "delivered", gotStdin, "the value did not travel to ssh on stdin")
	assert.Equal(t, 3, *calls, "the fake ran %d children, want the probe, the decrypt and the ssh", *calls)
	assert.NoFileExists(t, receiptPath(f.receipts, "web-1"), "a receipt was written for a delivery that failed")
}

// recordingFailer captures the strict fake's own failures.
type recordingFailer struct{ msgs []string }

func (r *recordingFailer) Helper() {}
func (r *recordingFailer) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

// TestSeamFakeRefusesAnUnscriptedChild pins the fake itself: a child the script does not
// name fails the test, so a test cannot pass by leaving a real tool reachable.
func TestSeamFakeRefusesAnUnscriptedChild(t *testing.T) {
	t.Parallel()

	r := &recordingFailer{}
	run, _ := scripted(r, scriptedStep{name: "sops", args: []string{"--version"}})
	_, err := run(nil, nil, "", "git", "status")
	assert.Error(t, err, "the strict fake must refuse a child its script does not name")
	require.Len(t, r.msgs, 1, "the strict fake must report the unscripted child")
	assert.Contains(t, r.msgs[0], "git status", "the failure names the child that ran: %s", r.msgs[0])
}

// TestArgsMatchWildcard pins the matcher: a wildcard matches exactly one word, so the
// script stays strict about the shape while allowing a path it cannot predict.
func TestArgsMatchWildcard(t *testing.T) {
	t.Parallel()

	assert.True(t, argsMatch([]string{"-d", wildcard}, []string{"-d", "/tmp/x.yaml"}))
	assert.True(t, argsMatch([]string{"--version"}, []string{"--version"}))
	assert.False(t, argsMatch([]string{"-d", wildcard}, []string{"-d"}))
	assert.False(t, argsMatch([]string{"-d", wildcard}, []string{"-d", "a", "b"}))
	assert.False(t, argsMatch([]string{"-e"}, []string{"-d"}))
}
