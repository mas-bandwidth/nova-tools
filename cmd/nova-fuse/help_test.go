package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

// foldBanner joins the banner's wrapped lines into running prose, so a
// sentence is compared as the reader folds it, while the banner itself keeps
// its wrap.
func foldBanner(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func TestVerbHelpNamesItsEffectsFlagsAndExitMeanings(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		verb, effect, flag, exit string
	}{
		{"init", "never replaced", "--box <path>", "1 already exists"},
		{"status", "not a permission gate", "--max <n>", "0 reported a readable box"},
		{"check", "no quarantine is checked", "--box <path>", "2 cannot prove clear"},
		{"lockdown", "Every untrusted read", "--box <path>", "Every failure remains no permission"},
		{"quarantine", "Requires a readable existing box", "--box <path>", "1 write or verification failed"},
		{"lift quarantine", "Any lockdown still blocks", "--box <path>", "0 lifted and verified"},
		{"lift lockdown", "live conversation with the person you work with", "no reset or override", "2 always refused"},
		{"path", "without reading or writing", "--box <path>", "0 path printed"},
		{"version", "Takes no flags or arguments", "no box", "0 build identity printed"},
	} {
		t.Run(row.verb, func(t *testing.T) {
			t.Parallel()
			code, out, errs := runFuse(t, append([]string{"help"}, strings.Fields(row.verb)...)...)
			require.Zero(t, code, errs)
			assert.Empty(t, errs)
			assert.True(t, strings.HasPrefix(out, "usage: nova-fuse "+row.verb+"\n") || strings.HasPrefix(out, "usage: nova-fuse "+row.verb+" "))
			assert.Contains(t, out, row.effect)
			assert.Contains(t, out, row.flag)
			assert.Contains(t, out, "exit codes:")
			assert.Contains(t, out, row.exit)
			assert.NotContains(t, out, "how it works:", "focused help does not print the whole banner")
		})
	}

	// The banner's three sentences below are checked against the behaviour by
	// running it, and the banner is what changes to match (ONBOARDING point 5:
	// tests pin the banner by EXECUTING it; docs/CLI-STYLE.md (d): 0 passed or
	// done, 1 ran and said no, 2 could not run; docs/SPEC.md "Exit codes and
	// output grammar" and "The read has one yes and two noes").

	t.Run("exit codes name one meaning of 1 per verb", func(t *testing.T) {
		t.Parallel()
		code, out, errs := runFuse(t, "help")
		require.Zero(t, code, errs)
		require.Empty(t, errs)
		said := foldBanner(out)
		assert.Contains(t, said, "check: 1 blown (a lockdown, or a quarantine on the surface)")
		assert.Contains(t, said, "init, lockdown, quarantine, lift quarantine: 1 the write was attempted and re-reading the box did not show it")
		assert.Contains(t, said, "status, path: 0 only")
		assert.Contains(t, said, "every verb: 2 could not run")
		assert.NotContains(t, said, "could not do it / could not verify it",
			"one sentence must not carry both meanings of 1: blown for check, unverified for a write")
	})

	t.Run("the exit meanings answer as the banner says", func(t *testing.T) {
		t.Parallel()
		// check: 1 blown -- a quarantine on the named surface, and a lockdown.
		box := freshBox(t) // the fixture quarantines a-public-issue-tracker
		code, _, _ := runFuse(t, "check", "--box", box, "a-public-issue-tracker")
		assert.Equal(t, 1, code, "check of a quarantined surface answers 1")
		blown := boxIn(t)
		writeRaw(t, blown, `{"lockdown":{"at":"2026-09-08T21:14:00Z","reason":"suspected compromise"},"quarantine":{}}`)
		code, _, _ = runFuse(t, "check", "--box", blown, "any-surface")
		assert.Equal(t, 1, code, "check under a lockdown answers 1")
		// write verbs: 1 the write was attempted and re-reading the box did not
		// show it -- init where a box stands, and a lift with nothing to lift.
		code, _, _ = runFuse(t, "init", "--box", box)
		assert.Equal(t, 1, code, "init over an existing box answers 1")
		code, _, _ = runFuse(t, "lift", "quarantine", "--box", box, "a-forum")
		assert.Equal(t, 1, code, "a lift the re-read does not show answers 1")
		// status, path: 0 only -- never 1, blown box or not.
		code, _, _ = runFuse(t, "status", "--box", blown)
		assert.Equal(t, 0, code, "status reports a blown box at 0; it never answers 1")
		code, _, _ = runFuse(t, "path", "--box", blown)
		assert.Equal(t, 0, code, "path echoes at 0; it never answers 1")
		// every verb: 2 could not run.
		code, _, _ = runFuse(t, "check")
		assert.Equal(t, 2, code, "a check with no --box could not run")
	})

	t.Run("a path with no box refuses only where the banner says", func(t *testing.T) {
		t.Parallel()
		_, help, _ := runFuse(t, "help")
		said := foldBanner(help)
		assert.Contains(t, said, "Every verb except init, lockdown, and path refuses a path with no box, never read as CLEAR")
		assert.Contains(t, said, "lockdown makes a blown box there")
		assert.NotContains(t, said, "2 could not run -- missing flag, no box at the path",
			"the blanket sentence called a path with no box exit 2 for every verb; lockdown answers 0 there")

		dir := t.TempDir()
		for _, args := range [][]string{
			{"status", "--box", filepath.Join(dir, "status.json")},
			{"check", "--box", filepath.Join(dir, "check.json"), "a-forum"},
			{"quarantine", "--box", filepath.Join(dir, "quarantine.json"), "a-forum", "a reason"},
			{"lift", "quarantine", "--box", filepath.Join(dir, "lift.json"), "a-forum"},
		} {
			code, _, _ := runFuse(t, args...)
			assert.Equal(t, 2, code, "%v: a verb that needs a box refuses a path with none at 2", args)
		}

		// The exception the run records: lockdown --dry-run at a path with no
		// box answers 0, writes nothing, and says a real run would make a box
		// there holding a blown lockdown.
		absent := filepath.Join(dir, "fuse-box.json")
		code, dryOut, dryErrs := runFuse(t, "lockdown", "--box", absent, "--dry-run", "a reason")
		require.Equal(t, 0, code, "lockdown --dry-run at a path with no box: %s", dryErrs)
		assert.Contains(t, dryOut, "dry_run=true")
		assert.Contains(t, dryOut, "a real run would make a box there holding a blown lockdown")
		_, err := os.Stat(absent)
		assert.True(t, os.IsNotExist(err), "the dry run wrote a box")

		// And the real run makes the blown box the banner now names.
		made := filepath.Join(dir, "made.json")
		code, _, errs := runFuse(t, "lockdown", "--box", made, "a reason")
		require.Equal(t, 0, code, "lockdown at a path with no box: %s", errs)
		require.FileExists(t, made)
		code, _, _ = runFuse(t, "check", "--box", made)
		assert.Equal(t, 1, code, "the box lockdown made is blown")

		// init makes an empty box there; path reads no box at all.
		fresh := filepath.Join(dir, "fresh.json")
		code, _, _ = runFuse(t, "init", "--box", fresh)
		assert.Equal(t, 0, code, "init at a path with no box makes the empty box")
		require.FileExists(t, fresh)
		code, echoed, _ := runFuse(t, "path", "--box", filepath.Join(dir, "nowhere.json"))
		assert.Equal(t, 0, code, "path reads no box at all")
		assert.Contains(t, echoed, "nowhere.json")
	})

	t.Run("lift lockdown names the command that replaces the box", func(t *testing.T) {
		t.Parallel()
		_, out, _ := runFuse(t, "help")
		said := foldBanner(out)
		assert.Contains(t, said, "REFUSED by design: the box is replaced: nova-fuse init --box <a new path>, and the harness pointed at it, by the person, never by this tool")
		assert.NotContains(t, said, "a blown fuse is REPLACED, only", "REPLACED named no command a reader could act on in one turn")

		// The named command runs: init makes the new clear box; the tool never
		// replaces the blown one itself.
		dir := t.TempDir()
		blown := filepath.Join(dir, "blown.json")
		writeRaw(t, blown, `{"lockdown":{"at":"2026-09-08T21:14:00Z","reason":"suspected compromise"},"quarantine":{}}`)
		fresh := filepath.Join(dir, "fresh.json")
		code, _, _ := runFuse(t, "init", "--box", fresh)
		assert.Equal(t, 0, code, "the replacement init runs at a new path")
		code, _, _ = runFuse(t, "check", "--box", fresh)
		assert.Equal(t, 0, code, "the new box is clear")
		code, _, _ = runFuse(t, "check", "--box", blown)
		assert.Equal(t, 1, code, "the old box stays blown until the person replaces it")
	})
}

func TestCheckHelpFlagStillFailsClosedAndNamesFocusedHelp(t *testing.T) {
	t.Parallel()
	box := freshBox(t)
	before := testkit.ReadFile(t, box)
	for _, flag := range []string{"-h", "--help"} {
		code, out, errs := runFuse(t, "check", "--box", box, flag)
		assert.Equal(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, errs, "so it can never read as CLEAR; run: nova-fuse help check")
	}
	assert.Equal(t, before, testkit.ReadFile(t, box))
	code, out, errs := runFuse(t, "help", "lift", "lockdown")
	assert.Zero(t, code)
	assert.Empty(t, errs)
	assert.Contains(t, out, "deliberate hand-edit")
	assert.Equal(t, before, testkit.ReadFile(t, box), "help never performs a replacement")
}

func TestTheHelpBoxExampleDecodesAsTheDescribedQuarantine(t *testing.T) {
	t.Parallel()
	_, out, _ := runFuse(t, "help")
	var example string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, `  {"lockdown": null, "quarantine": {"a-forum"`) {
			example = strings.TrimSpace(line)
		}
	}
	require.NotEmpty(t, example)
	var box fuse.Box
	require.NoError(t, json.Unmarshal([]byte(example), &box))
	assert.Nil(t, box.Lockdown)
	require.Contains(t, box.Quarantine, "a-forum")
	assert.Equal(t, "an attack is pervasive", box.Quarantine["a-forum"].Reason)
}

func TestMisspelledCommandsNameTheAvailableChoices(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--zzz"}, "nova-fuse status REFUSED: unknown flag --zzz; the flags of status are --box, --max; run: nova-fuse help status"},
		{[]string{"check", "--zzz"}, "nova-fuse check REFUSED: unknown flag --zzz; the flags of check are --box; run: nova-fuse help check"},
		{[]string{"check", "--bx", "x"}, "did you mean --box?"},
		{[]string{"lockdown", "--zzz"}, "the flags of lockdown are --box, --dry-run"},
		{[]string{"defuse"}, "nova-fuse REFUSED: unknown verb \"defuse\"; the verbs are init, status, check, lockdown, quarantine, lift, path, version, help; run: nova-fuse help"},
		{[]string{"stauts"}, "did you mean status?"},
		{[]string{"help", "defuse"}, "unknown verb"},
		{[]string{"version", "--unexpected"}, "run: nova-fuse help version"},
	} {
		code, out, errs := runFuse(t, row.args...)
		assert.Equal(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, errs, row.want)
	}
}

func TestLiftGroupHelpIncludesTheNothingToLiftExit(t *testing.T) {
	t.Parallel()
	code, out, errs := runFuse(t, "help", "lift")
	require.Zero(t, code, errs)
	assert.Empty(t, errs)
	assert.Contains(t, out, "1 no quarantine to lift")
	code, out, errs = runFuse(t, "help", "version")
	require.Zero(t, code, errs)
	assert.Empty(t, errs)
	assert.Contains(t, out, "usage: nova-fuse version")
}

// A verb's help (its only route: -h is refused) ends in the effect line every tool
// prints, and every verb that writes the box lists --dry-run.
func TestVerbHelpStatesItsEffectAndItsDryRun(t *testing.T) {
	t.Parallel()
	for verb, writes := range map[string]bool{
		"init": true, "lockdown": true, "quarantine": true, "lift quarantine": true,
		"status": false, "check": false, "path": false, "version": false, "lift lockdown": false,
	} {
		code, out, errs := runFuse(t, append([]string{"help"}, strings.Fields(verb)...)...)
		require.Zero(t, code, errs)
		want := "\neffect: inspection: "
		if writes {
			want = "\neffect: local write: "
		}
		assert.Contains(t, out, want, verb)
		assert.Equal(t, writes, strings.Contains(out, "\n  --dry-run "), "%s: --dry-run listed", verb)
	}
}
