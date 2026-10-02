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
		{"lift lockdown", "live conversation with your person", "no reset or override", "2 always refused"},
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
		{[]string{"status", "--zzz"}, "flags: --box, --max; run: nova-fuse help status"},
		{[]string{"check", "--zzz"}, "flags: --box; run: nova-fuse help check"},
		{[]string{"defuse"}, "verbs: init, status, check, lockdown, quarantine, lift, path, version, help"},
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
