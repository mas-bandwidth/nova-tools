package secrets

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errText is a verb's refusal as one string, "" when it did not refuse.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestOneRefusalNamesEveryMissingFlag pins ONBOARDING point 2 for every verb: a call
// with nothing given names every required flag in one refusal, never one per run.
func TestOneRefusalNamesEveryMissingFlag(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		verb  string
		run   func() string
		flags []string
	}{
		{"names", func() string { _, err := RunNames("", "", 20); return errText(err) }, []string{"--store", "--as"}},
		{"check", func() string { _, _, _, _, _, err := RunCheck("", "", "", "", 20); return errText(err) }, []string{"--store", "--as", "--key", "--sops"}},
		{"seal", func() string { _, err := RunSeal(SealOptions{}); return errText(err) }, []string{"--store", "--as", "--key", "--sops", "--name"}},
		{"place", func() string { _, err := RunPlace(PlaceInput{}); return errText(err) }, []string{"--machine", "--secret", "--store", "--as", "--key", "--sops"}},
		{"seat add", func() string { _, err := RunSeatAdd(SeatAddOptions{}); return errText(err) }, []string{"--store", "--as", "--from", "--pub", "--key", "--sops", "--only"}},
		{"seat inject", func() string { _, err := RunSeatInject(SeatInjectOptions{}); return errText(err) }, []string{"--store", "--as", "--from", "--key", "--sops", "--only"}},
		{"keygen", func() string { _, err := RunKeygen("", "", "", ""); return errText(err) }, []string{"--as", "--key", "--age-keygen"}},
		{"gate", func() string { line, _ := RunGate(GateInput{}); return line }, []string{"--store", "--base", "--head", "run: nova-secrets gate -h"}},
		{"exec", func() string { _, err := RunExec("", "", "", "", "", nil, nil); return errText(err) }, []string{"--store", "--as", "--key", "--sops", "--only", "no command after '--'", "--as worker"}},
	} {
		c := c
		t.Run(c.verb, func(t *testing.T) {
			t.Parallel()
			got := c.run()
			require.NotEmpty(t, got, "%s with nothing given must refuse", c.verb)
			for _, f := range c.flags {
				assert.Contains(t, got, f, "%s names every missing input in one refusal", c.verb)
			}
		})
	}
}

// TestOneRefusalNamesEveryWayADirectoryIsNotAStore: a directory with neither .git nor
// .sops.yaml is refused once, naming both and the command that makes a store, by every
// verb that reads a store.
func TestOneRefusalNamesEveryWayADirectoryIsNotAStore(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		verb string
		run  func(store, dir string) error
	}{
		{"names", func(s, _ string) error { _, err := RunNames(s, "a", 20); return err }},
		{"check", func(s, d string) error {
			_, _, _, _, _, err := RunCheck(s, "a", filepath.Join(d, "k"), filepath.Join(d, "sops"), 20)
			return err
		}},
		{"seal", func(s, d string) error {
			_, err := RunSeal(SealOptions{StoreDir: s, AsName: "a", KeyPath: filepath.Join(d, "k"), SopsPath: filepath.Join(d, "sops"), Name: "N"})
			return err
		}},
		{"keygen", func(s, d string) error {
			_, err := RunKeygen("a", filepath.Join(d, "k"), filepath.Join(d, "age-keygen"), s)
			return err
		}},
		{"exec", func(s, d string) error {
			_, err := OpenSeatFile(s, "a", filepath.Join(d, "k"), filepath.Join(d, "sops"))
			return err
		}},
	} {
		c := c
		t.Run(c.verb, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			store := filepath.Join(dir, "store")
			require.NoError(t, os.Mkdir(store, 0o755))
			got := errText(c.run(store, dir))
			assert.Contains(t, got, "no .git directory")
			assert.Contains(t, got, "no .sops.yaml")
			assert.Contains(t, got, "run: git clone <store url> "+store)
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Len(t, entries, 1, "a refusal writes nothing")
		})
	}
}

// TestAWorktreeStoreIsOneSentenceOnEveryVerb: a --store whose .git is a file (a git
// worktree or a submodule) is refused by every verb that reads a store in the same words,
// with the way to the store's own working copy, and nothing is written.
func TestAWorktreeStoreIsOneSentenceOnEveryVerb(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		verb string
		run  func(store, dir string) error
	}{
		{"names", func(s, _ string) error { _, err := RunNames(s, "a", 20); return err }},
		{"check", func(s, d string) error {
			_, _, _, _, _, err := RunCheck(s, "a", filepath.Join(d, "k"), filepath.Join(d, "sops"), 20)
			return err
		}},
		{"exec", func(s, d string) error {
			_, err := OpenSeatFile(s, "a", filepath.Join(d, "k"), filepath.Join(d, "sops"))
			return err
		}},
		{"the working copy check", func(s, _ string) error { _, err := CheckGitWorkingCopy(s); return err }},
	} {
		t.Run(c.verb, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			store := filepath.Join(dir, "store")
			require.NoError(t, os.Mkdir(store, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(store, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/store\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules:\n"), 0o644))
			got := errText(c.run(store, dir))
			assert.Contains(t, got, "store "+store+" has a .git that is a file (a worktree or submodule), where a directory working copy is wanted")
			assert.Contains(t, got, "git -C "+store+" rev-parse --path-format=absolute --git-common-dir")
			assert.NotContains(t, got, "git clone <store url> "+store, "a clone into the worktree's own directory cannot run")
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Len(t, entries, 1, "a refusal writes nothing")
		})
	}
}

// TestABadSeatNameAndABadStoreAreOneRefusal: a seat name that is a path and a --store
// that is no store are both named in the one refusal, the seat name before anything is
// read under it.
func TestABadSeatNameAndABadStoreAreOneRefusal(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "absent")
	_, err := RunNames(store, "../outside", 20)
	got := errText(err)
	assert.Contains(t, got, `invalid seat name "../outside" for --as`)
	assert.Contains(t, got, "store "+store+" is not a directory")
	_, err = RunSeatAdd(SeatAddOptions{StoreDir: store, AsName: "new", From: "a/b"})
	got = errText(err)
	assert.Contains(t, got, "missing --pub")
	assert.Contains(t, got, `invalid seat name "a/b" for --from`)
}

// TestASealNotYetInTheStoreSaysSoAndWhatIsNext: a --no-pr seal, whose value is not on
// the store's own branch, prints a NOTE saying exec does not read it yet, with the next
// command, and the value is on no line.
func TestASealNotYetInTheStoreSaysSoAndWhatIsNext(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)
	const value = "placeholder-value-0123456789"
	f := newSealFixture(t, "TARGET: old\n")
	out, err := RunSeal(f.options(t, "TARGET", value+"\n", true))
	require.NoError(t, err)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 2, "the OK line, then its NOTE: %q", out)
	assert.True(t, strings.HasPrefix(lines[0], "SECRETS SEAL OK name=TARGET seat=rowan committed branch=seal/rowan-TARGET-20260917-120000"))
	assert.Equal(t, "SECRETS SEAL NOTE exec and check read the store's own branch, which does not hold this value yet; next: git -C "+
		f.storeDir+" push -u origin seal/rowan-TARGET-20260917-120000, then open and merge its pull request", lines[1])
	assert.False(t, Leaks(out, NewSecret(value)), "a seal's receipt carries no value")
}

// TestAFirstStoreRefusalSaysWhatIsMissingAndHowToMakeIt: the two refusals a first store
// meets past its shape, a missing recovery.pub and a sops encrypt that failed, say what
// the thing is and what to run, and the failed encrypt still withholds the transcript.
func TestAFirstStoreRefusalSaysWhatIsMissingAndHowToMakeIt(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	_, err := ReadRecoveryPub(store)
	got := errText(err)
	assert.Contains(t, got, "recovery.pub is absent: it holds the store's recovery key")
	assert.Contains(t, got, "run: age-keygen -o ")
	assert.Contains(t, got, filepath.Join(store, "recovery.pub"))

	const value = "qzxjwkvbnmplqzxj"
	failing := func(io.Reader, []string, string, string, ...string) ([]byte, error) {
		return []byte("sops said: " + value), errors.New("exit status 1")
	}
	_, _, err = sealEncrypt(failing, "sops", "k", store, "ada.yaml", "seal", []byte(value))
	got = errText(err)
	assert.Contains(t, got, "transcript withheld")
	assert.Contains(t, got, "the .sops.yaml rule for ada.yaml")
	assert.Contains(t, got, "<recovery key> placeholder")
	assert.Contains(t, got, "--dry-run lists the recipients")
	assert.False(t, Leaks(got, NewSecret(value)))

	_, err = sealCarry{run: failing, storeDir: store, gitPath: "git"}.preflight()
	got = errText(err)
	assert.Contains(t, got, "git rev-parse failed")
	assert.Contains(t, got, "a store with no commit yet has no branch to return to; run: git -C "+store+" add .sops.yaml recovery.pub")
}

// TestAnAbsentSeatNamesTheSeatsAndTheVerbThatStartsOne: the refusal for a seat with no
// file names the store's seats, or, in a store with none, the seal that writes the first.
func TestAnAbsentSeatNamesTheSeatsAndTheVerbThatStartsOne(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		seats []string
		want  []string
	}{
		{"no seat yet", nil, []string{"holds no seat file yet", "run: nova-secrets seal --store "}},
		{"other seats", []string{"lead", "worker"}, []string{"its seats are lead, worker", "pass --as one of them", "run: nova-secrets seal --store "}},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			store := t.TempDir()
			for _, s := range append([]string{".sops"}, c.seats...) {
				require.NoError(t, os.WriteFile(filepath.Join(store, s+".yaml"), []byte("x: y\n"), 0o644))
			}
			got := errText(seatAbsent(store, "absent"))
			for _, w := range c.want {
				assert.Contains(t, got, w)
			}
			assert.NotContains(t, got, ".sops,", ".sops.yaml is the rule file, not a seat")
		})
	}
}
