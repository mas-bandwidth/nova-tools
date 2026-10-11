package secrets

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// skipPOSIXFakesOnWindows marks the seal tests whose sops/git/gh fakes are POSIX
// shell scripts and whose fixtures depend on POSIX mode bits. Windows can run
// neither, so the piped-stdin assertion is carried there by
// TestSealEncryptTakesValueOnStdin, which fakes the child in pure Go.
func skipPOSIXFakesOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell-script fakes and POSIX mode bits are unavailable on Windows; covered by TestSealEncryptTakesValueOnStdin")
	}
}

// sealFixture is a throwaway store, key, and a bench of fake helper binaries.
// Nothing here touches the network or a real credential.
type sealFixture struct {
	dir        string
	storeDir   string
	keyPath    string
	sopsPath   string
	gitPath    string
	ghPath     string
	sopsArgs   string
	sopsStdin  string
	sopsDecOut string
	gitArgs    string
	ghArgs     string
}

func newSealFixture(t *testing.T, decryptOut string) *sealFixture {
	t.Helper()
	dir := t.TempDir()
	f := &sealFixture{
		dir:        dir,
		storeDir:   filepath.Join(dir, "store"),
		sopsArgs:   filepath.Join(dir, "sops.args"),
		sopsStdin:  filepath.Join(dir, "sops.stdin"),
		sopsDecOut: filepath.Join(dir, "decrypt.out"),
		gitArgs:    filepath.Join(dir, "git.args"),
		ghArgs:     filepath.Join(dir, "gh.args"),
	}
	require.NoError(t, os.MkdirAll(f.storeDir, 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(f.storeDir, ".git"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(f.storeDir, ".sops.yaml"), []byte("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: age1abc\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(f.storeDir, "rowan.yaml"), []byte("ENC[old]\n"), 0644))

	keyDir := filepath.Join(dir, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0700))
	f.keyPath = filepath.Join(keyDir, "rowan.key")
	require.NoError(t, os.WriteFile(f.keyPath, []byte("AGE-SECRET-KEY-1TEST\n# public key: age1test\n"), 0600))

	sopsBody := "ARGS=\"" + f.sopsArgs + "\"\n" +
		"STDIN=\"" + f.sopsStdin + "\"\n" +
		"DECOUT=\"" + f.sopsDecOut + "\"\n" +
		"if [ \"$1\" = \"--version\" ]; then echo \"sops 3.13.3\"; exit 0; fi\n" +
		"printf '%s\\n' \"$@\" >> \"$ARGS\"\n" +
		"case \"$*\" in\n" +
		"  *\"-d\"*) cat \"$DECOUT\"; exit 0;;\n" +
		"esac\n" +
		"# strict like real sops: encrypt needs a file argument and the store as cwd\n" +
		"for last; do :; done\n" +
		"if [ \"$last\" != \"/dev/stdin\" ]; then exit 100; fi\n" +
		"pwd -P > \"$ARGS.cwd\"\n" +
		"cp .sops.yaml \"$ARGS.cfg\" 2>/dev/null\n" +
		"cat > \"$STDIN\"\n" +
		"echo \"ENC[marker]\"\n"
	f.sopsPath = f.writeScript(t, "sops", sopsBody)

	gitBody := "printf '%s\\n' \"$@\" >> \"" + f.gitArgs + "\"\n" +
		"if [ \"$1\" = \"rev-parse\" ] && [ \"$2\" = \"--abbrev-ref\" ]; then echo \"main\"; fi\n" +
		"exit 0\n"
	f.gitPath = f.writeScript(t, "git", gitBody)
	ghBody := "printf '%s\\n' \"$@\" >> \"" + f.ghArgs + "\"\n" +
		"if [ \"$1 $2\" = \"pr create\" ]; then echo \"https://example.com/mas-bandwidth/secrets/pull/42\"; fi\n" +
		"if [ \"$1 $2\" = \"pr view\" ]; then echo \"APPROVED\"; fi\n" +
		"exit 0\n"
	f.ghPath = f.writeScript(t, "gh", ghBody)

	require.NoError(t, os.WriteFile(f.sopsDecOut, []byte(decryptOut), 0644))
	return f
}

func (f *sealFixture) writeScript(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(f.dir, name)
	require.NoError(t, testbin.WriteExecutable(p, []byte("#!/bin/sh\n"+body), 0755))
	return p
}

func (f *sealFixture) options(t *testing.T, name, value string, noPR bool) SealOptions {
	t.Helper()
	return SealOptions{
		StoreDir:        f.storeDir,
		AsName:          "rowan",
		KeyPath:         f.keyPath,
		SopsPath:        f.sopsPath,
		Name:            name,
		GitPath:         f.gitPath,
		GHPath:          f.ghPath,
		NoPR:            noPR,
		Stdin:           strings.NewReader(value),
		StdinIsTerminal: false,
		Now:             func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) },
		Check:           func(storeDir, asName, keyPath, sopsPath string) error { return nil },
	}
}

func readMaybe(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func TestSealPipedValueLandsInEncryptStdinNotArgv(t *testing.T) {
	t.Parallel()

	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "OTHER: keepme\nTARGET: oldvalue\n")
	line, err := RunSeal(f.options(t, "TARGET", "newsecretvalue\n", true))
	require.NoError(t, err, "RunSeal: %v", err)
	stdin := readMaybe(t, f.sopsStdin)
	assert.Contains(t, stdin, "TARGET: "+yamlSingleQuote("newsecretvalue"), "encrypt stdin missing the pasted value; got:\n%s", stdin)
	n := strings.Count(stdin, "TARGET:")
	assert.Equal(t, 1, n, "encrypt stdin holds %d TARGET lines, want 1:\n%s", n, stdin)
	argv := readMaybe(t, f.sopsArgs)
	assert.NotContains(t, argv, "newsecretvalue", "value leaked into sops argv:\n%s", argv)
	assert.Contains(t, argv, "--filename-override", "encrypt did not use --filename-override rowan.yaml:\n%s", argv)
	assert.Contains(t, argv, "rowan.yaml", "encrypt did not use --filename-override rowan.yaml:\n%s", argv)
	assert.True(t, strings.HasSuffix(strings.TrimSpace(argv), "/dev/stdin"), "encrypt argv must end with the /dev/stdin file argument (real sops exits 100 without it):\n%s", argv)
	// sops finds .sops.yaml from its cwd. The fixture's rule predates the mark, so the
	// config it found there is the store's with the mark admitted to rowan.yaml's rule,
	// and that is the .sops.yaml the commit carries beside the file.
	wantCfg := "creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    unencrypted_regex: ^NOVA_SECRETS_WRITTEN_BY$\n    age: age1abc\n"
	gotCfg := readMaybe(t, f.sopsArgs+".cfg")
	assert.Equal(t, wantCfg, gotCfg, "encrypt did not find the store's rule with the mark admitted in its cwd")
	assert.Contains(t, readMaybe(t, f.gitArgs), "add\n.sops.yaml\n", "the rule that admits the mark was not committed beside the file")
	assert.NotContains(t, line, "newsecretvalue", "value leaked into the OK line: %s", line)
	assert.Contains(t, line, "SEAL OK", "unexpected OK line: %s", line)
	assert.Contains(t, line, "name=TARGET", "unexpected OK line: %s", line)
	assert.Contains(t, line, "seat=rowan", "unexpected OK line: %s", line)
}

// TestSealValueWithoutNewlineIsRefused: --stdin and a pipe take one line.
// EOF with no newline is Ctrl-D, and that is not a value.
func TestSealValueWithoutNewlineIsRefused(t *testing.T) {
	t.Parallel()

	const secret = "topsecret-no-newline"
	_, err := readSealValue(SealOptions{
		UseStdin:        true,
		StdinIsTerminal: true,
		Stdin:           strings.NewReader(secret),
	})
	require.Error(t, err, "a value with no newline must be refused")
	assert.NotContains(t, err.Error(), secret, "the refusal must not echo the value")
	assert.Contains(t, err.Error(), "Enter", "the refusal must say the value ends with Enter: %v", err)
	assert.Contains(t, err.Error(), "Ctrl-D", "the refusal must say Ctrl-D is not a value: %v", err)
}

// TestSealValueWithAnotherLineIsRefused: one Enter ends the value. Bytes after
// that line used to be dropped; they are the multi-line refusal.
func TestSealValueWithAnotherLineIsRefused(t *testing.T) {
	t.Parallel()

	_, err := readSealValue(SealOptions{
		UseStdin:        true,
		StdinIsTerminal: false,
		Stdin:           strings.NewReader("one\ntwo\n"),
	})
	require.Error(t, err, "a second line must be refused")
	assert.NotContains(t, err.Error(), "one", "the refusal must not echo the value")
	assert.NotContains(t, err.Error(), "two", "the refusal must not echo the rest")
	assert.Contains(t, err.Error(), "multi-line", "the refusal must say multi-line: %v", err)
}

func TestSealEmptyValueRefused(t *testing.T) {
	t.Parallel()

	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "TARGET: old\n")
	for _, value := range []string{"", "\n"} {
		_, err := RunSeal(f.options(t, "TARGET", value, true))
		require.Error(t, err, "RunSeal accepted an empty value %q", value)
		assert.Contains(t, strings.ToLower(err.Error()), "empty", "empty-value refusal does not say empty: %v", err)
	}
	got := readMaybe(t, f.sopsArgs)
	assert.Empty(t, got, "empty value still started sops:\n%s", got)
}

func TestSealReplacesExistingNameNotDuplicated(t *testing.T) {
	t.Parallel()

	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "TARGET: old\nOTHER: keepme\nTARGET: older\n")
	_, err := RunSeal(f.options(t, "TARGET", "fresh\n", true))
	require.NoError(t, err, "RunSeal: %v", err)
	stdin := readMaybe(t, f.sopsStdin)
	n := strings.Count(stdin, "TARGET:")
	assert.Equal(t, 1, n, "TARGET appears %d times, want 1:\n%s", n, stdin)
	assert.Contains(t, stdin, "TARGET: "+yamlSingleQuote("fresh"), "new value missing:\n%s", stdin)
	assert.Contains(t, stdin, "OTHER: keepme", "unrelated key was dropped:\n%s", stdin)
}

// TestSealQuotesTheValue pins security#64 finding 1: sealApply must render the
// value as a YAML single-quoted scalar, the same form seat add (seatAddSelect)
// and seat inject use. sops parses the plaintext as YAML on encrypt and
// re-serializes it on decrypt, so an unquoted value with YAML-significant bytes
// comes back changed: "hunter2 # tail" loses its tail as a comment, "[a, b]"
// becomes a sequence, and leading or trailing spaces are lost. Each case must
// land on the encrypt child's stdin quoted, and parse back byte for byte.
func TestSealQuotesTheValue(t *testing.T) {
	t.Parallel()

	skipPOSIXFakesOnWindows(t)
	cases := []struct {
		name  string
		value string
	}{
		{name: "comment tail", value: "hunter2 # tail"},
		{name: "flow sequence", value: "[a, b]"},
		{name: "leading and trailing spaces", value: "  spaced  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSealFixture(t, "OTHER: keepme\nTARGET: old\n")
			_, err := RunSeal(f.options(t, "TARGET", tc.value+"\n", true))
			require.NoError(t, err, "RunSeal: %v", err)
			stdin := readMaybe(t, f.sopsStdin)
			assert.Contains(t, stdin, "TARGET: "+yamlSingleQuote(tc.value),
				"value on the encrypt stdin is not single-quoted; got:\n%s", stdin)

			secrets, _, err := ParseDecryptedSecrets([]byte(stdin))
			require.NoError(t, err, "ParseDecryptedSecrets(%q): %v", stdin, err)
			got, ok := secrets["TARGET"]
			require.True(t, ok, "TARGET missing from the round-tripped document:\n%s", stdin)
			require.True(t, got.Loaded(), "TARGET missing from the round-tripped document:\n%s", stdin)
			require.NoError(t, got.Use(func(v string) error {
				assert.Equal(t, tc.value, v, "value changed on the round trip through the seat document")
				return nil
			}))
		})
	}
}

// TestReviewSealNoPRPreservesStartingDirtyWorktree: checkout -f of the
// starting branch discards caller-owned tracked edits (#2016 HOLD). Seal must
// refuse a dirty store before checkout -b so unstaged and staged tracked
// changes are still there after the refusal.
func TestReviewSealNoPRPreservesStartingDirtyWorktree(t *testing.T) {
	t.Parallel()

	skipPOSIXFakesOnWindows(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	const marker = "caller-owned-edit"
	cases := []struct {
		name   string
		staged bool
	}{
		{name: "unstaged tracked", staged: false},
		{name: "staged tracked", staged: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSealFixture(t, "TARGET: old\n")
			initTrackedGitStore(t, f.storeDir)

			gitBin, err := exec.LookPath("git")
			require.NoError(t, err)
			owned := filepath.Join(f.storeDir, ".sops.yaml")
			origOwned, err := os.ReadFile(owned)
			require.NoError(t, err)
			dirty := append(append([]byte{}, origOwned...), []byte("# "+marker+"\n")...)
			require.NoError(t, os.WriteFile(owned, dirty, 0644))
			if tc.staged {
				gitC(t, f.storeDir, "add", ".sops.yaml")
			}

			origBranch := gitC(t, f.storeDir, "rev-parse", "--abbrev-ref", "HEAD")
			origHEAD := gitC(t, f.storeDir, "rev-parse", "HEAD")
			origSeat, err := os.ReadFile(filepath.Join(f.storeDir, "rowan.yaml"))
			require.NoError(t, err)

			var gitLog []string
			opts := f.options(t, "TARGET", "placeholder-value\n", true)
			opts.GitPath = gitBin
			opts.Exec = func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
				if name == gitBin || filepath.Base(name) == "git" {
					gitLog = append(gitLog, strings.Join(args, " "))
					return realExecCommand(stdin, env, dir, gitBin, args...)
				}
				return realExecCommand(stdin, env, dir, name, args...)
			}

			line, runErr := RunSeal(opts)
			assert.NotContains(t, line, marker, "caller edit leaked into the OK line: %s", line)
			if assert.Error(t, runErr, "RunSeal accepted a dirty worktree") {
				msg := strings.ToLower(runErr.Error())
				assert.True(t, strings.Contains(msg, "clean") || strings.Contains(msg, "dirty"), "refusal does not name a dirty/clean store: %v", runErr)
				assert.True(t, strings.Contains(msg, "git status") || strings.Contains(msg, "commit"), "refusal is not actionable: %v", runErr)
				assert.NotContains(t, runErr.Error(), marker, "caller edit leaked into the error: %v", runErr)
			}

			gotOwned, err := os.ReadFile(owned)
			require.NoError(t, err)
			assert.Equal(t, string(dirty), string(gotOwned), "starting dirty worktree was not restored")
			gotSeat, err := os.ReadFile(filepath.Join(f.storeDir, "rowan.yaml"))
			require.NoError(t, err)
			assert.Equal(t, string(origSeat), string(gotSeat), "seat file was rewritten despite a dirty starting worktree")
			assert.Equal(t, origBranch, gitC(t, f.storeDir, "rev-parse", "--abbrev-ref", "HEAD"), "left the starting branch")
			assert.Equal(t, origHEAD, gitC(t, f.storeDir, "rev-parse", "HEAD"), "moved HEAD")
			joined := strings.Join(gitLog, "\n")
			assert.NotContains(t, joined, "checkout -b", "dirty store still switched branches:\n%s", joined)
			assert.NotContains(t, joined, "checkout -f", "dirty store still switched branches:\n%s", joined)
		})
	}
}

func TestSealFullPathOpensPRAndMerges(t *testing.T) {
	t.Parallel()

	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "TARGET: old\n")
	line, err := RunSeal(f.options(t, "TARGET", "v\n", false))
	require.NoError(t, err, "RunSeal: %v", err)
	gh := readMaybe(t, f.ghArgs)
	for _, want := range []string{"pr", "create", "view", "merge"} {
		assert.Contains(t, gh, want, "gh calls missing %q:\n%s", want, gh)
	}
	assert.Contains(t, line, "pr=#42", "unexpected merged line: %s", line)
	assert.Contains(t, line, "merged", "unexpected merged line: %s", line)
}

// TestSealSaysWhatItIsDoing: a person at a terminal must be able to tell waiting from
// hung (Glenn 2026-09-17: the verb polled for approval in silence and read as a hang).
// Every step that can take time has a progress line; none of them carries the value;
// and after the merge the store goes back to the branch it was on before pulling.
func TestSealSaysWhatItIsDoing(t *testing.T) {
	t.Parallel()

	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "TARGET: old\n")
	opts := f.options(t, "TARGET", "quietsecretvalue\n", false)
	var progress strings.Builder
	opts.Progress = &progress
	_, err := RunSeal(opts)
	require.NoError(t, err, "RunSeal: %v", err)
	got := progress.String()
	for _, want := range []string{"reading rowan.yaml", "encrypting", "committing on branch seal/rowan-TARGET-",
		"pushing", "opening the pull request", "pull request #42 is open; waiting", "approved; merging #42",
		"returning the store to its branch", "checking the seat decrypts"} {
		assert.Contains(t, got, want, "progress missing %q:\n%s", want, got)
	}
	assert.NotContains(t, got, "quietsecretvalue", "value leaked into progress:\n%s", got)
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		assert.True(t, strings.HasPrefix(l, "seal: "), "progress line without the seal: prefix: %q", l)
	}
	git := strings.ReplaceAll(readMaybe(t, f.gitArgs), "\n", " ")
	back, pull := strings.Index(git, "checkout -f"), strings.LastIndex(git, "pull")
	assert.GreaterOrEqual(t, back, 0, "after the merge git must checkout -f the starting branch and then pull; got: %s", git)
	assert.GreaterOrEqual(t, pull, 0, "after the merge git must checkout -f the starting branch and then pull; got: %s", git)
	assert.LessOrEqual(t, back, pull, "after the merge git must checkout -f the starting branch and then pull; got: %s", git)
}

// TestSealEncryptTakesValueOnStdin asserts the core seal promise on every
// platform: the plaintext reaches the encrypt child on stdin and never in its
// argv. The child is a pure-Go fake supplied through the exec seam, so no
// shell and no POSIX mode bits are involved (card 8517).
func TestSealEncryptTakesValueOnStdin(t *testing.T) {
	t.Parallel()

	var gotStdin, gotDir string
	var gotArgs []string
	run := func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
		require.Equal(t, "sops", name, "unexpected helper %q", name)
		gotDir = dir
		gotArgs = append([]string(nil), args...)
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}
		gotStdin = string(b)
		return []byte("ENC[marker]\n"), nil
	}

	const value = "newsecretvalue"
	plaintext := []byte("TARGET: " + value + "\n")
	out, _, err := sealEncrypt(run, "sops", "/nonexistent/rowan.key", "/the/store", "rowan.yaml", "seal", plaintext)
	require.NoError(t, err, "sealEncrypt: %v", err)
	assert.Contains(t, gotStdin, "TARGET: "+value, "encrypt stdin missing the pasted value; got:\n%s", gotStdin)
	n := strings.Count(gotStdin, "TARGET:")
	assert.Equal(t, 1, n, "encrypt stdin holds %d TARGET lines, want 1:\n%s", n, gotStdin)
	for _, a := range gotArgs {
		assert.NotContains(t, a, value, "value leaked into encrypt argv: %q", a)
	}
	if assert.NotEmpty(t, gotArgs, "encrypt argv must end with /dev/stdin, got %q", gotArgs) {
		assert.Equal(t, "/dev/stdin", gotArgs[len(gotArgs)-1], "encrypt argv must end with /dev/stdin, got %q", gotArgs)
	}
	assert.Equal(t, "/the/store", gotDir, "encrypt dir = %q, want the store", gotDir)
	assert.Contains(t, string(out), "ENC[marker]", "encrypt stdout not returned: %q", out)
}

func initTrackedGitStore(t *testing.T, storeDir string) {
	t.Helper()
	remote := t.TempDir()
	gitC(t, remote, "init", "--bare", "-b", "main")
	gitC(t, storeDir, "init", "-b", "main")
	gitC(t, storeDir, "config", "user.name", "seal-test")
	gitC(t, storeDir, "config", "user.email", "seal-test@example.com")
	gitC(t, storeDir, "config", "commit.gpgsign", "false")
	gitC(t, storeDir, "remote", "add", "origin", remote)
	gitC(t, storeDir, "add", "-A")
	gitC(t, storeDir, "commit", "-m", "initial")
	gitC(t, storeDir, "push", "-u", "origin", "main")
}

func gitC(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCErr(t, dir, args...)
	require.NoError(t, err, "git %v: %v\n%s", args, err, out)
	return out
}

func gitCErr(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// The read of 2026-10-07: `seal 1.2.<80 digits>` passed as a mark in the clear, because
// markVersionPattern capped nothing but the rc suffix, so an arbitrary value could ride
// under the mark key. Each version component is capped at four digits; an oversized one is
// no mark, and the gate reads the cleartext under the mark key as a plain value. Each row
// is a probe and, for a refusal, the line it must print.
func TestGateRefusesAnOversizedMarkVersion(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("9", 80)
	cases := []struct {
		name string
		val  string
		ok   bool
	}{
		{"a release tag", "seal v1.2.3-rc1", true},
		{"a numeric release", "seat add 1.2.3", true},
		{"dev", "seal dev", true},
		{"an eighty-digit patch", "seal 1.2." + long, false},
		{"a five-digit component", "seal 12345.2.3", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.ok, isSeatMark(c.val), "isSeatMark(%q)", c.val)
			dir := gateStart(t)
			base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
			body := strings.Replace(gateSealedFile(), gateMarkLine, SeatMarkKey+": "+c.val+"\n", 1)
			head := gateCommit(t, dir, map[string]string{
				".sops.yaml": gateSops(gateRuleWith("rowan.yaml", "^NOVA_SECRETS_WRITTEN_BY$")),
				"rowan.yaml": body,
			})
			line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
			if c.ok {
				assert.Equal(t, 0, code, line)
				assert.Equal(t, "GATE APPROVE files=2 machines=-", line)
				return
			}
			assert.Equal(t, 1, code, line)
			assert.True(t, strings.HasPrefix(line,
				"GATE FAILED rule=1 check=2 file=rowan.yaml: key NOVA_SECRETS_WRITTEN_BY is a plain value, not encrypted"), line)
		})
	}
}
