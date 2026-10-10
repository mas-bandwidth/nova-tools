package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePlay is a play runner that answers with one output and error, and
// keeps the argv it was given.
type fakePlay struct {
	out  string
	err  error
	argv []string
}

func (f *fakePlay) Play(_ context.Context, argv []string) (string, error) {
	f.argv = argv
	return f.out, f.err
}

// playOK is the seat play's output when every step printed its line.
const playOK = `TASK [store: the receipt] ***
ok: [seat-a] => {
    "msg": "ADOPT step=store host=seat-a before=aaa after=bbb want=bbb CHANGED"
}
TASK [seat: the receipt, one ADOPT line per step] ***
ok: [seat-a] => (item=ADOPT step=server host=seat-a before=v1 after=v2 revision=abc restarted=com.nova.loop.s CHANGED) => {
    "msg": "ADOPT step=server host=seat-a before=v1 after=v2 revision=abc restarted=com.nova.loop.s CHANGED"
}
ok: [seat-a] => (item=x) => {
    "msg": "ADOPT step=dashboard host=seat-a before=/old after=/bin/nova-sprint links=1 CHANGED"
}
ok: [seat-a] => (item=y) => {
    "msg": "ADOPT step=friends host=seat-a before=v1 after=v2 reinstalled=fa CHANGED"
}
`

// TestAdoptRunsThePlayAndRefusesAHalfMove: adopt runs the tools play for the
// seat (a fake runner) with the version, the checkout and the limit, prints
// each step's ADOPT line, and refuses a play that stops at a step or ends
// without a step's line, naming the step; --dry-run is the play's --check, and
// a built release directory names the version and the release output.
func TestAdoptRunsThePlayAndRefusesAHalfMove(t *testing.T) {
	t.Parallel()
	assert.Contains(t, notServed, "adopt", "the seat play must run locally, not through the sprint server")
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "fleet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "fleet", "tools.yml"), []byte("[]\n"), 0o644))
	built := filepath.Join(t.TempDir(), "release-build", "v1.2.0-dev.abc1234")
	require.NoError(t, os.MkdirAll(built, 0o755))
	run := func(play *fakePlay, args ...string) (int, string, string) {
		a := newApp(func(k string) string { return map[string]string{"HOME": "/home/x"}[k] })
		adoptPlayOf.Store(a, play)
		defer adoptPlayOf.Delete(a)
		var out, errs bytes.Buffer
		code := a.run(append([]string{"adopt"}, args...), &out, &errs)
		return code, out.String(), errs.String()
	}
	base := []string{"--source", src, "--inventory", "/inv/nova-inventory", "--reason", "the dashboard fix"}

	ok := &fakePlay{out: playOK}
	code, out, errs := run(ok, append([]string{"v1.2.0-dev.abc1234"}, base...)...)
	require.Equal(t, 0, code, errs)
	assert.Equal(t, []string{"-i", "/inv/nova-inventory", filepath.Join(src, "fleet", "tools.yml"), "-e", "nova_version=v1.2.0-dev.abc1234",
		"-e", "nova_source=" + src, "-e", "nova_dogfood_receipts=/home/x/nova-working/dogfood",
		"-e", `{"nova_release_build_args":["--incremental","--gate","report","--reason","the dashboard fix"]}`,
		"--limit", "coordinator,localhost,store_deployer"}, ok.argv)
	for _, step := range adoptPlaySteps {
		assert.Contains(t, out, "ADOPT step="+step+" host=seat-a ")
	}
	assert.Equal(t, 1, strings.Count(out, "ADOPT step=server "), "a loop item's line is said once")
	assert.Contains(t, out, "ADOPT ADOPTED version=v1.2.0-dev.abc1234 hosts=seat-a steps=store,server,dashboard,friends")

	// --dry-run is --check; a built release directory is <out>/<version>; --limit names the host
	dry := &fakePlay{out: strings.ReplaceAll(playOK, "CHANGED", "WOULD-CHANGE")}
	code, out, errs = run(dry, append([]string{built, "--dry-run", "--limit", "seat-a"}, base...)...)
	require.Equal(t, 0, code, errs)
	assert.True(t, slices.Contains(dry.argv, "--check"))
	assert.True(t, slices.Contains(dry.argv, "nova_version=v1.2.0-dev.abc1234"))
	assert.True(t, slices.Contains(dry.argv, "nova_release_out="+filepath.Dir(built)))
	assert.True(t, slices.Contains(dry.argv, "seat-a,localhost,store_deployer"))
	for _, step := range adoptPlaySteps {
		assert.Contains(t, out, "ADOPT WOULD step="+step+" host=seat-a")
	}
	assert.Contains(t, out, "ADOPT DRY-RUN OK steps=4")

	// the play stops at the dashboard: the steps before it are said, the refusal names the step
	stopped := &fakePlay{out: `TASK [store: the receipt] ***
ok: [seat-a] => {
    "msg": "ADOPT step=store host=seat-a before=aaa after=bbb want=bbb CHANGED"
}
TASK [dashboard: a summary, and each link names the installed nova-sprint] ***
fatal: [seat-a]: FAILED! => {"assertion": "x", "changed": false, "evaluated_to": false, "msg": "ADOPT REFUSED step=dashboard host=seat-a: the dashboard answered 500 with no summary"}
`, err: errors.New("exit status 2")}
	code, out, errs = run(stopped, append([]string{"v1.2.0-dev.abc1234"}, base...)...)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "ADOPT step=store host=seat-a")
	assert.NotContains(t, out, "ADOPTED")
	assert.Contains(t, errs, "adopt REFUSED step=dashboard host=seat-a: the dashboard answered 500 with no summary; the steps before it are done")

	// the window had opened: the failing check's refusal and the rescue's line saying what the
	// rollback did are both said, and the steps the window took are said rolled back, not done
	rolled := &fakePlay{out: `TASK [store: the receipt] ***
ok: [seat-a] => {
    "msg": "ADOPT step=store host=seat-a before=aaa after=bbb CHANGED"
}
TASK [server: the receipt] ***
ok: [seat-a] => {
    "msg": "ADOPT step=server host=seat-a before=v1 after=v2 restarted=com.nova.loop.s CHANGED"
}
TASK [dashboard: a summary] ***
fatal: [seat-a]: FAILED! => {"assertion": "x", "changed": false, "evaluated_to": false, "msg": "ADOPT REFUSED step=dashboard host=seat-a: the dashboard answered 500 with no summary"}
TASK [seat: the refusal, as the failed step said it, and what the rollback did] ***
fatal: [seat-a]: FAILED! => {"changed": false, "msg": "ADOPT REFUSED step=dashboard host=seat-a: the dashboard answered 500 with no summary; rollback: 3 tools of before put back; library aaa read back, the one of before; started again: com.nova.loop.s; the schema stays migrated"}
`, err: errors.New("exit status 2")}
	code, out, errs = run(rolled, append([]string{"v1.2.0-dev.abc1234"}, base...)...)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "ADOPT step=server host=seat-a")
	assert.Contains(t, errs, "adopt REFUSED step=dashboard host=seat-a: the dashboard answered 500 with no summary; "+
		"rollback: 3 tools of before put back; library aaa read back, the one of before; started again: com.nova.loop.s; the schema stays migrated; "+
		"rolled back: store,server; the ones after it did not run")
	assert.NotContains(t, errs, "the steps before it are done")
	assert.Equal(t, 1, strings.Count(errs, "the dashboard answered 500"), "the refusal is said once")

	// a task fails with no refusal of its own: the step is the task's
	failed := &fakePlay{out: "TASK [server: bootstrap each from its plist] ***\nfatal: [seat-a]: FAILED! => {\"rc\": 5}\n", err: errors.New("exit status 2")}
	code, _, errs = run(failed, append([]string{"v1.2.0-dev.abc1234"}, base...)...)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, `adopt REFUSED step=server task="server: bootstrap each from its plist"`)

	// a refusal is said verbatim, an escaped quote and all; a failed handler names its step
	quoted := &fakePlay{out: "TASK [store: the library is the build's] ***\nfatal: [seat-a]: FAILED! => {\"msg\": \"ADOPT REFUSED step=store host=seat-a: the library {\\\"loaded\\\": \\\"aaa\\\"} is not this build's\"}\n", err: errors.New("exit status 2")}
	code, _, errs = run(quoted, append([]string{"v1.2.0-dev.abc1234"}, base...)...)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, `adopt REFUSED step=store host=seat-a: the library {"loaded": "aaa"} is not this build's; the steps before it are done`)
	handler := &fakePlay{out: "RUNNING HANDLER [server: bootstrap each from its plist] ***\nfatal: [seat-a]: FAILED! => {\"rc\": 5}\n", err: errors.New("exit status 2")}
	code, _, errs = run(handler, append([]string{"v1.2.0-dev.abc1234"}, base...)...)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, `adopt REFUSED step=server task="server: bootstrap each from its plist"`)

	// a play that ends without the friends line is a half move, refused
	half := &fakePlay{out: strings.Split(playOK, "ok: [seat-a] => (item=y)")[0]}
	code, _, errs = run(half, append([]string{"v1.2.0-dev.abc1234"}, base...)...)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "adopt REFUSED step=friends host=seat-a: a half move")

	// no seat in the limit: nothing of the seat was adopted
	code, _, errs = run(&fakePlay{out: "PLAY RECAP\n"}, append([]string{"v1.2.0-dev.abc1234", "--limit", "bench-b"}, base...)...)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "adopt REFUSED step=seat: the play printed no ADOPT line")

	// a checkout with no play is an input that does not read: exit 1, nothing run
	none := &fakePlay{}
	code, _, errs = run(none, "v1.2.0-dev.abc1234", "--source", t.TempDir(), "--inventory", "/i", "--reason", "r")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "adopt REFUSED step=play: --source ")
	assert.Nil(t, none.argv, "no play ran")

	// usage: a word, the checkout, the reason; one machine in --limit
	for _, bad := range [][]string{
		base,
		{"v1.2.0-dev.abc1234", "--inventory", "/i", "--reason", "r"},
		{"v1.2.0-dev.abc1234", "--source", src, "--inventory", "/i"},
		append([]string{"v1.2.0-dev.abc1234", "--limit", "all,!x"}, base...),
		append([]string{"1.2.0"}, base...),
	} {
		code, _, errs = run(&fakePlay{}, bad...)
		assert.Equal(t, 2, code, "%v: %s", bad, errs)
		assert.Contains(t, errs, "nova-sprint adopt REFUSED")
	}
}
