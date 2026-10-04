package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

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
