package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them opens the store, the key or a helper program (the CLI style's
// rule (b)). run is the whole tool on its arguments and streams, so this drives
// it in process: no binary is built and no process started, and nothing here
// waits on a clock.
//
// The banner also carries the three things a cold reader cannot invent: the
// setup: block of real commands that makes a store from an empty directory,
// seal's --stdin path (a harness has no terminal), and no usage or flag line
// past 100 columns. Each is asserted here, and the --stdin line is run as
// printed against a store with that seat (nova-secrets help; the J05 cold
// rating's one fix). A wrapped line of the usage or flag table aligns with
// the column its table's entries start at, so the wrap reads as one entry
// (docs/CLI-STYLE.md rule (b), Help; the width matches nova-ci's tables).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	inProcess := func(args []string, stdout, stderr io.Writer) int {
		return run(args, strings.NewReader(""), stdout, stderr)
	}
	store := []string{"--store", "{dir}/store", "--as", "seat"}
	testverbhelp.Check(t, inProcess, []testverbhelp.Case{
		{Verb: "exec", Flags: store},
		{Verb: "names", Flags: store},
		{Verb: "check", Flags: store},
		{Verb: "gate", Flags: []string{"--store", "{dir}/store"}},
		{Verb: "keygen", Flags: []string{"--key", "{dir}/key.txt"}},
		{Verb: "place", Flags: []string{"--store", "{dir}/store"}},
		{Verb: "placed", Flags: []string{"--receipts", "{dir}/receipts"}},
		{Verb: "seal", Flags: store},
		{Verb: "seat add", Flags: store},
		{Verb: "seat inject", Flags: store},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, inProcess, "nova-secrets", "names", "seat add", "version")

	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, inProcess([]string{"help"}, &stdout, &stderr), "`nova-secrets help` must exit 0")
	help := stdout.String()

	// The three lines the rating paid for: the setup: block and the two tools
	// it names, seal's --stdin path, and the harness sentence on the flag.
	assert.Contains(t, help, "setup: needs age-keygen and sops on PATH")
	assert.Contains(t, help, "first value:")
	assert.Contains(t, help, "printf '%s\\n'")
	assert.Contains(t, help, "--stdin --no-pr")
	assert.Contains(t, help, "the path a harness")

	// No usage or flag line runs past 100 columns (the example: lines are held
	// by the example tests; a continuation line would end the block for
	// onboarding.ExampleLines, so they are not wrapped here).
	for i, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(line, "example:") {
			break
		}
		assert.LessOrEqualf(t, len([]rune(line)), 100, "help line %d is %d columns: %q", i+1, len([]rune(line)), line)
	}

	// Every continuation line of the usage and flag tables starts at the
	// column its table's entries start at: 22 spaces in the usage block, 23
	// in the flag table (docs/CLI-STYLE.md rule (b), Help; the width matches
	// nova-ci's tables). The 100-column bound above cannot see an indent one
	// column right of the table's own.
	for _, table := range []struct {
		name string
		want int
	}{{"usage", 22}, {"flags", 23}} {
		_, tail, found := strings.Cut(help, "\n"+table.name+":\n")
		require.Truef(t, found, "the banner has no %s block", table.name)
		body, _, _ := strings.Cut(tail, "\n\n")
		for j, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			spaces := len(line) - len(strings.TrimLeft(line, " "))
			if spaces == 2 {
				continue // an entry line of the table, not a continuation
			}
			assert.Equalf(t, table.want, spaces, "%s continuation line %d is indented %d spaces, want %d: %q",
				table.name, j+1, spaces, table.want, line)
		}
	}

	// The first value: line runs as printed, against a store holding that seat.
	h := newSittingHome(t)
	line := firstValueLine(t, help)
	value, args := parseFirstValue(t, line)
	args = localizeSitting(h, args)
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(value+"\n"), &out, &errOut)
	require.Equalf(t, 0, code, "the first value: line does not run: %s", errOut.String())
	assert.Contains(t, out.String(), "SECRETS SEAL OK name=GH_TOKEN seat=ada")
	assert.NotContains(t, out.String(), value, "the sealed value reached stdout")
	assert.NotContains(t, errOut.String(), value, "the sealed value reached stderr")
}

// fakeAgeKeygen is an age-keygen that answers --version and writes, per -o file, a
// key file in age-keygen's own layout whose public key is a valid bech32 one (age's
// alphabet, 62 characters), a different one for the recovery key than for the seat's,
// so the rule the setup block writes names two distinct recipients.
func fakeAgeKeygen(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "age-keygen")
	script := `#!/bin/sh
case "$1" in
  --version|-version) echo "v1.3.2"; exit 0 ;;
esac
out=""
prev=""
for a in "$@"; do
  [ "$prev" = "-o" ] && out="$a"
  prev="$a"
done
case "$out" in
  *recovery*) lead=z ;;
  *) lead=p ;;
esac
A=qpzry9x8gf2tvdw0s3jn54khce6mua7l
tail=$(printf %s "$A$A" | cut -c2-58)
printf '# created: 2026-09-18\n# public key: age1%s%s\nAGE-SECRET-KEY-1FAKE\n' "$lead" "$tail" > "$out"
`
	require.NoError(t, testbin.WriteExecutable(path, []byte(script), 0o755))
	return path
}

// TestTheHelpSetupBlockRunsAsPrinted executes the banner's setup: block, each line
// as the help prints it, in order, from an empty directory. The block is the store a
// cold reader has to make before any verb but keygen and names can run, so the claim
// the help makes is not "the lines are there" but "they run" (the CLI style's rule (c),
// held for the example: block by the tests below; this holds it for setup:). The two
// tools the heading names are faked on PATH the way this package's other example tests
// fake them, so no real key, secret, machine or forge is touched; git, sed and printf
// come from /usr/bin. What the block leaves is the state the banner's first value:
// line needs: a commit, an upstream tracking it, a recovery.pub equal to the recovery
// keygen's public line, and a .sops.yaml holding the rule the keygen line printed.
func TestTheHelpSetupBlockRunsAsPrinted(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the block runs through /bin/sh")
	}
	home := t.TempDir()
	bin := buildNovaSecrets(t)
	ageKeygen := fakeAgeKeygen(t)
	fakeBin := filepath.Join(home, "fakebin")
	require.NoError(t, os.MkdirAll(fakeBin, 0o755))
	writeFakeExe(t, filepath.Join(fakeBin, "sops"), "#!/bin/sh\nexit 0\n")
	help, helpErr, code := runNovaSecrets(bin, "help")
	require.Equal(t, 0, code, "`nova-secrets help` exits %d: %s", code, helpErr)
	lines := setupLines(t, help)
	require.NotEmpty(t, lines, "the banner's setup: block is empty")
	env := []string{
		"PATH=" + strings.Join([]string{filepath.Dir(bin), filepath.Dir(ageKeygen), fakeBin, "/usr/bin", "/bin"}, string(os.PathListSeparator)),
		"HOME=" + home,
		// A reader's git carries its own identity; the block's commit line needs one.
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test",
	}
	for _, line := range lines {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command("sh", "-c", line)
		cmd.Dir, cmd.Env = home, env
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		require.NoErrorf(t, err, "the setup line does not run: %s\nstdout: %s\nstderr: %s", line, stdout.String(), stderr.String())
	}

	// recovery.pub is the recovery keygen's public line, byte for byte: that is what
	// the sed line in the block is for. .sops.yaml is the rule the keygen line printed,
	// with the seat's key and the recovery key, each read back from its own key file.
	pubOf := func(name string) string {
		body, err := os.ReadFile(filepath.Join(home, ".config", "nova-secrets", name+".key"))
		require.NoError(t, err)
		_, pub, found := strings.Cut(string(body), "# public key: ")
		require.True(t, found, "the %s key holds no public key comment:\n%s", name, body)
		return strings.TrimSpace(strings.SplitN(pub, "\n", 2)[0])
	}
	recovery, ada := pubOf("recovery"), pubOf("ada")
	pubBytes, err := os.ReadFile(filepath.Join(home, "secrets", "recovery.pub"))
	require.NoError(t, err)
	assert.Equal(t, recovery+"\n", string(pubBytes), "recovery.pub is not the recovery keygen's public line")
	rule, err := os.ReadFile(filepath.Join(home, "secrets", ".sops.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: "+ada+","+recovery+"\n", string(rule),
		".sops.yaml is not the rule the keygen line printed")
	// The branch tracks the local bare remote, and HEAD is on it.
	store := filepath.Join(home, "secrets")
	status := gitHere(t, store, env, "status", "-sb")
	assert.Equal(t, "## main...origin/main\n", status, "the store is not on main tracking the bare remote, clean")
	head := gitHere(t, store, env, "rev-parse", "HEAD")
	remote := gitHere(t, store, env, "rev-parse", "origin/main")
	assert.Equal(t, head, remote, "HEAD is not the remote-tracking ref")
}

// gitHere runs the real git in a directory with the environment the setup lines ran
// under, so the assertions read the store the block left with the same tools a reader
// has. The absolute path keeps the test host's own PATH out of the read.
func gitHere(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("/usr/bin/git", args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed in %s: %s", args, dir, out)
	return string(out)
}

// setupLines returns the commands under the banner's `setup:` heading, one per
// command, with a ` \` continuation folded to one line, in the order printed.
func setupLines(t *testing.T, help string) []string {
	t.Helper()
	_, tail, found := strings.Cut(help, "\nsetup:")
	require.True(t, found, "the banner has no `setup:` block:\n%s", help)
	heading, rest, _ := strings.Cut(tail, "\n")
	block, _, _ := strings.Cut(rest, "\n\n")
	require.Contains(t, heading, "age-keygen", "the setup: heading does not name age-keygen")
	require.Contains(t, heading, "sops", "the setup: heading does not name sops")
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(block, "\\\n", " "), "\n") {
		if strings.HasPrefix(l, "  ") {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines
}

// firstValueLine returns the command under the banner's `first value:` heading,
// with a ` \` continuation folded to one line.
func firstValueLine(t *testing.T, help string) string {
	t.Helper()
	_, tail, found := strings.Cut(help, "first value:\n")
	require.True(t, found, "the banner has no `first value:` block:\n%s", help)
	block, _, _ := strings.Cut(tail, "\n\n")
	line := strings.ReplaceAll(block, "\\\n", " ")
	line = strings.Join(strings.Fields(line), " ")
	require.True(t, strings.HasPrefix(line, "printf '%s\\n'"), "the first value: block does not open with printf: %q", line)
	return line
}

// parseFirstValue splits the `printf '%s' '<value>' | nova-secrets seal ...`
// line into the value and the tool's arguments.
func parseFirstValue(t *testing.T, line string) (string, []string) {
	t.Helper()
	left, right, found := strings.Cut(line, "|")
	require.True(t, found, "the first value: line is not a pipe: %q", line)
	lf := strings.Fields(left)
	require.GreaterOrEqual(t, len(lf), 3, "the printf side names no value: %q", line)
	value := strings.Trim(lf[2], "'")
	rf := strings.Fields(right)
	require.GreaterOrEqual(t, len(rf), 2, "the pipe names no command: %q", line)
	require.Equal(t, "nova-secrets", rf[0], "the pipe does not run nova-secrets: %q", line)
	return value, rf[1:]
}

// localizeSitting rewrites the banner's paths to the sitting fixture's own, as
// the other example tests do.
func localizeSitting(h sittingHome, args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case a == "./secrets":
			a = filepath.Join(h.home, "secrets")
		case strings.HasPrefix(a, "~/"):
			a = filepath.Join(h.home, strings.TrimPrefix(a, "~/"))
		case a == "/opt/homebrew/bin/sops":
			a = h.sops
		}
		out = append(out, a)
	}
	return out
}
