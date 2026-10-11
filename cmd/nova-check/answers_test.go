package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every mistake is answered with the way forward, in the grammar every tool
// here uses: `nova-check[ <verb>] REFUSED: ...; run: ...`, the verbs or the
// flags named, and the nearest one.
func TestEveryMistakeIsRefusedWithTheWayForward(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "CHECK REFUSED: no verb given; the verbs are quickstart, attest, links"},
		{[]string{"linsk"}, `CHECK REFUSED: unknown verb "linsk"; did you mean links? the verbs are quickstart`},
		{[]string{"links", "--dri", "x"}, "LINKS REFUSED: unknown flag --dri; the flags of links are"},
		{[]string{"hygiene", "--rep", "."}, "HYGIENE REFUSED: unknown flag --rep;"},
		{[]string{"hygiene", "stray"}, `HYGIENE REFUSED: takes no positional arguments, got "stray"`},
		{[]string{"version", "x"}, `VERSION REFUSED: takes no positional arguments, got "x"`},
		{[]string{"version", "--zz"}, "VERSION REFUSED: unknown flag --zz; the flags of version are"},
	} {
		exit, stdout, stderr := runCheck(t, tc.args...)
		assert.Equal(t, 2, exit, "%v", tc.args)
		assert.Empty(t, stdout, "%v", tc.args)
		assert.Contains(t, stderr, tc.want, "%v", tc.args)
		assert.NotContains(t, stderr, "flag provided but not defined", "%v", tc.args)
	}
}

// Every verb's -h ends its own lines with its effect, and every verb that can
// write lists --dry-run; convergence says it reads the forge over the network.
func TestEveryVerbHelpStatesItsEffect(t *testing.T) {
	t.Parallel()
	for verb, writes := range map[string]bool{
		"quickstart": false, "attest": false, "links": false, "kernel": false, "nocode": false,
		"floors": false, "corpus": false, "hygiene": false, "dogfood ledger": false, "dogfood gate": false,
		"dogfood record": true, "convergence": true, "spelling": true, "version": false,
	} {
		exit, stdout, stderr := runCheck(t, append(strings.Fields(verb), "-h")...)
		require.Equal(t, 0, exit, "%s -h: %s", verb, stderr)
		want := "\neffect: inspection: "
		if writes {
			want = "\neffect: local write: "
		}
		assert.Contains(t, stdout, want, verb)
		assert.Equal(t, writes, strings.Contains(stdout, "--dry-run"), "%s: --dry-run listed", verb)
	}
	_, stdout, _ := runCheck(t, "convergence", "-h")
	assert.Contains(t, stdout, "through gh, over the network")
}

// A failed quickstart names next the failed check run alone, not the verbs a
// healthy run moves on to.
func TestAFailedQuickstartNamesTheCheckToFix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("[x](missing.md)\n"), 0o644))
	exit, stdout, _ := runCheck(t, "quickstart", "--dir", dir)
	assert.Equal(t, 1, exit)
	last := strings.TrimSpace(stdout[strings.LastIndex(strings.TrimSpace(stdout), "\n")+1:])
	assert.True(t, strings.HasPrefix(last, "QUICKSTART FAILED checks=2 failed=links worst-exit=1 next=nova-check links --dir "+dir), last)
	assert.NotContains(t, last, "kernel")
}

// The quickstart's next= is a command to paste and run, so a --dir with a
// blank in it comes back as one shell word.
func TestAFailedQuickstartsNextQuotesADirWithABlank(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "my self")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("[x](missing.md)\n"), 0o644))
	exit, stdout, _ := runCheck(t, "quickstart", "--dir", dir)
	assert.Equal(t, 1, exit)
	assert.Contains(t, stdout, "next=nova-check links --dir '"+dir+"' (fix what it names")
}

// dogfood record names every problem of one run: the missing flags and the
// missing verdict together.
func TestDogfoodRecordNamesEveryProblemAtOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, stderr := dogfoodRun(t, "dogfood", "record", "--cli", writeCLI(t, dir), "--tool", "nova-example", "--verb", "links", "--receipts", dir)
	for _, want := range []string{"--by is required", "--notes is required", "state the verdict exactly once"} {
		assert.Contains(t, stderr, want)
	}
}

// A reference that declares no verb is refused with the shape a verb is
// declared in.
func TestAnEmptyReferenceIsAnsweredWithTheShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cli := filepath.Join(dir, "CLI.md")
	require.NoError(t, os.WriteFile(cli, []byte("# nothing here\n"), 0o644))
	exit, _, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", dir)
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "a --cli reference declares a verb as a command line in a fenced block")
}

// --dry-run on the three verbs that write makes every check and writes nothing.
func TestDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	t.Run("dogfood record", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		receipts := filepath.Join(dir, "receipts")
		exit, stdout, stderr := dogfoodRun(t, "dogfood", "record", "--cli", writeCLI(t, dir), "--tool", "nova-example", "--verb", "links",
			"--by", "Ada", "--ok", "--notes", "ran it on real work", "--receipts", receipts, "--dry-run")
		require.Equal(t, 0, exit, stderr)
		assert.Contains(t, stdout, "DOGFOOD RECORD OK tool=nova-example verb=links by=Ada")
		assert.Contains(t, stdout, "file="+receipts+string(filepath.Separator))
		assert.True(t, strings.HasSuffix(stdout, ".json dry_run=true\n"), stdout)
		_, err := os.Stat(receipts)
		assert.True(t, os.IsNotExist(err), "a dry run made the receipts directory: %v", err)
	})

	t.Run("spelling --write", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "a.md")
		require.NoError(t, os.WriteFile(file, []byte("the recieve step\n"), 0o644))
		exit, stdout, stderr := runCheck(t, "spelling", "--dir", dir, "--write", "--dry-run")
		require.Equal(t, 0, exit, stderr)
		assert.Contains(t, stdout, "SPELLING FIX ")
		assert.Contains(t, stdout, "written=0 dry_run=true")
		got, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Equal(t, "the recieve step\n", string(got), "a dry run edited the file")
	})

	t.Run("convergence --state", func(t *testing.T) {
		t.Parallel()
		f := newConvFixture(t)
		state := filepath.Join(t.TempDir(), "state.json")
		exit, stdout, stderr := f.run(t, "--state", state, "--dry-run")
		require.Equal(t, 0, exit, stderr)
		assert.Contains(t, stdout, "CONVERGENCE")
		assert.Contains(t, stderr, "CONVERGENCE NOTE dry_run=true: --state")
		_, err := os.Stat(state)
		assert.True(t, os.IsNotExist(err), "a dry run wrote the state: %v", err)
	})
}

// The banner's quickstart and kernel examples run as printed, on the tree the
// banner's two `setup:` lines make, and print what is written here, through the
// comparator; only the tree's directory is the run's.
func TestTheBannerQuickstartAndKernelExamplesMatchOutput(t *testing.T) {
	t.Parallel()
	_, help, _ := runCheck(t, "help")
	require.Contains(t, help, "setup:\n  mkdir -p ./self/docs\n  printf '# Kernel\\n' > ./self/docs/SEED-CORE.md\n")
	scratch := t.TempDir()
	self := filepath.Join(scratch, "self")
	require.NoError(t, os.MkdirAll(filepath.Join(self, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(self, "docs", "SEED-CORE.md"), []byte("# Kernel\n"), 0o644))
	volatile := []onboarding.Field{{Name: "tmpdir", Doc: "./self", Run: self}}
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"nova-check quickstart --dir ./self", []string{
			"QUICKSTART RUN dir=./self checks=2: links, then nocode",
			"LINKS OK dir=./self files=1 links=0 excluded=0 broken=0",
			"NOCODE OK dir=./self files=1 deny-list=floor-list findings=0",
			"QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus (kernel wants a size budget, attest a manifest of what a full boot reads, floors a derived copy and its source, corpus a ledger of protected lines: nova-check help)",
		}},
		{"nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000", []string{"KERNEL OK file=./self/docs/SEED-CORE.md bytes=9 budget=4000 findings=0"}},
	} {
		require.Contains(t, help, "  "+tc.command+"\n")
		args := strings.Fields(strings.ReplaceAll(tc.command, "./self", self))[1:]
		exit, stdout, stderr := runCheck(t, args...)
		step := onboarding.Step{Line: "$ " + tc.command, Args: args, Want: tc.want}
		assert.Empty(t, onboarding.CompareTranscript([]onboarding.Step{step}, []onboarding.Result{{Code: exit, Stdout: stdout, Stderr: stderr}}, volatile))
	}
}
