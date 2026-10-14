package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// release_window_test.go pins the seat play's release window sentinel
// (docs/SPEC-RELEASE.md, "The seat adopts an approved build"): the one JSON
// file the seat play writes when its window opens and removes when it ends or
// is rolled back, at nova_release_window_file, so a verb that cannot reach the
// store can read it and say a release window is open. The file is advice for a
// human or an agent; nothing's behaviour depends on it. The two repositories
// state the same contract word for word and nova-tools is released first.
//
// The test parses fleet/tools.yml the way tools_test.go does: no ansible run,
// no network, no clock. The written document is the play's own template,
// rendered here with fixed variables; the dates in it are templated strings
// checked for shape (RFC3339), never taken from a real clock.

// The window's steps are named here so a rename is a single edit.
const (
	windowWriteName    = "window: the release window sentinel is written (open, reason, opened, until, back_in_s)"
	windowRemoveName   = "window: the release window sentinel is removed (the server answered, so the window is closed)"
	windowRollbackName = "rollback: the release window sentinel is removed first (a stale open never outlives the window)"
	// windowGuard is the guard the contract names: the sentinel is written and
	// removed on a real window, and a --check run writes and removes nothing.
	windowGuard = "seat_window and not ansible_check_mode"
	// windowRefused is the text a step that cannot remove the file carries.
	windowRefused = "ADOPT REFUSED step=window-sentinel"
)

// TestTheSeatPlayWritesTheReleaseWindowSentinel opens with t.Parallel so the
// unit tier never serializes it, and reads the play and the rollback block only.
func TestTheSeatPlayWritesTheReleaseWindowSentinel(t *testing.T) {
	t.Parallel()
	play := playNamed(t, readTools(t), "the seat adopts the build")
	tasks := flattenTasks(t, tasksOf(t, play))

	// (1) The write task exists, is guarded by seat_window and not
	// ansible_check_mode, and stands before the first task that stops an agent
	// of the window (the bootout of seat_stopped).
	write := taskNamed(t, tasks, windowWriteName)
	assert.Equal(t, windowGuard, asText(write["when"]),
		"the sentinel write is not guarded by seat_window and not ansible_check_mode")
	stop := taskNamed(t, tasks, "window: boot out each (a member drains on the SIGTERM)")
	require.Less(t, taskIndex(tasks, windowWriteName), taskIndex(tasks, asText(stop["name"])),
		"the sentinel is written after the first agent of the window is booted out")

	// (2) The remove task exists after the freshness check (and after the
	// server answers on its --listen addresses), guarded the same way.
	remove := taskNamed(t, tasks, windowRemoveName)
	assert.Equal(t, windowGuard, asText(remove["when"]),
		"the sentinel removal is not guarded by seat_window and not ansible_check_mode")
	assertRemovesReleaseWindow(t, remove)
	fresh := taskNamed(t, tasks, "server: every one started is there exactly once, loaded and not stale (lsof and ps, through live)")
	assert.Greater(t, taskIndex(tasks, windowRemoveName), taskIndex(tasks, asText(fresh["name"])),
		"the sentinel is removed before the restarted agents are fresh")
	answers := taskNamed(t, tasks, "server: it answers on each address it listens on")
	assert.Greater(t, taskIndex(tasks, windowRemoveName), taskIndex(tasks, asText(answers["name"])),
		"the sentinel is removed before the server answers on its --listen addresses")

	// A removal that cannot remove the file is a failed step naming
	// step=window-sentinel, never a silent leave. The refusal reads msg:
	// failed_when: false overwrites .failed (lib/ansible/executor/task_executor.py).
	assertRefusesOnAFailedRemoval(t, tasks, windowRemoveName)

	// (3) The rollback block's first task removes the file, before the
	// rollback's own tasks run, so a stale open never outlives the window.
	block := taskNamed(t, tasksOf(t, play), "seat: the adoption, step by step")
	rescue := mapsOf(t, block["rescue"])
	require.NotEmpty(t, rescue, "the seat block has no rollback (rescue)")
	assert.Equal(t, windowRollbackName, asText(rescue[0]["name"]),
		"the rollback's first task is not the sentinel removal")
	assertRemovesReleaseWindow(t, rescue[0])
	assert.Equal(t, windowGuard, asText(rescue[0]["when"]),
		"the rollback's sentinel removal is not guarded by seat_window and not ansible_check_mode")
	require.GreaterOrEqual(t, len(rescue), 2, "the rollback has no refusal after the sentinel removal")
	assertRefusalReadsTheSurvivingSignal(t, rescue[1])

	// (4) The play's own template, rendered with fixed variables, is the
	// contract's JSON: open, reason, opened, until, back_in_s, until after
	// opened.
	doc := renderWindowDocument(t, write)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(doc), &got), "the written sentinel is not JSON: %s", doc)
	assert.Equal(t, true, got["open"], "the sentinel does not say open")
	assert.Equal(t, "adopt v9.9.9", got["reason"], "the sentinel's reason is not \"adopt <version>\"")
	opened, _ := got["opened"].(string)
	until, _ := got["until"].(string)
	openedAt, errOpen := time.Parse(time.RFC3339, opened)
	require.NoError(t, errOpen, "opened is not an RFC3339 UTC stamp: %q", opened)
	untilAt, errUntil := time.Parse(time.RFC3339, until)
	require.NoError(t, errUntil, "until is not an RFC3339 UTC stamp: %q", until)
	assert.True(t, untilAt.After(openedAt), "until %s is not after opened %s", until, opened)
	assert.Equal(t, float64(360), got["back_in_s"], "back_in_s is not nova_member_stop_timeout + nova_seat_quiet + 120")

	// The bound the write reads: nova_seat_window_max is the play's own var,
	// default 1800 seconds, and back_in_s names its siblings.
	assert.Equal(t, "1800", fmt.Sprint(seatVar(t, play, tasks, "nova_seat_window_max")),
		"nova_seat_window_max does not default to 1800 seconds")
	assert.Contains(t, varOf(t, play, tasks, "nova_release_window_file"), ".nova/release-window.json",
		"nova_release_window_file does not default to $HOME/.nova/release-window.json")
	assert.Contains(t, taskVar(t, write, "seat_window_back"), "nova_member_stop_timeout",
		"back_in_s is not nova_member_stop_timeout + nova_seat_quiet + 120")
	assert.Contains(t, taskVar(t, write, "seat_window_back"), "nova_seat_quiet",
		"back_in_s is not nova_member_stop_timeout + nova_seat_quiet + 120")
	assert.Contains(t, taskVar(t, write, "seat_window_back"), "120",
		"back_in_s is not nova_member_stop_timeout + nova_seat_quiet + 120")

	// (5) --check writes and removes nothing: both guards carry
	// not ansible_check_mode.
	assert.Contains(t, asText(write["when"]), "not ansible_check_mode", "--check would write the sentinel")
	assert.Contains(t, asText(remove["when"]), "not ansible_check_mode", "--check would remove the sentinel")
	assert.Contains(t, asText(rescue[0]["when"]), "not ansible_check_mode", "--check would remove the sentinel in the rollback")
}

// taskIndex is the position of the task named name in tasks, or -1.
func taskIndex(tasks []map[string]any, name string) int {
	for i, task := range tasks {
		if asText(task["name"]) == name {
			return i
		}
	}
	return -1
}

// seatVar is the raw value the seat play's vars or a set_fact give key
// (varOf reads strings only; nova_seat_window_max is a number).
func seatVar(t *testing.T, play map[string]any, tasks []map[string]any, key string) any {
	t.Helper()
	if vars, ok := play["vars"].(map[string]any); ok {
		if v, ok := vars[key]; ok {
			return v
		}
	}
	for _, task := range tasks {
		sf, _ := task["ansible.builtin.set_fact"].(map[string]any)
		if sf == nil {
			continue
		}
		if v, ok := sf[key]; ok {
			return v
		}
	}
	require.FailNow(t, "no play var or set_fact of "+key)
	return nil
}

// assertRemovesReleaseWindow pins that task removes nova_release_window_file
// with ansible.builtin.file state absent, never a shell rm and never silently.
func assertRemovesReleaseWindow(t *testing.T, task map[string]any) {
	t.Helper()
	file, ok := task["ansible.builtin.file"].(map[string]any)
	require.True(t, ok, "the sentinel removal is not an ansible.builtin.file task")
	assert.Equal(t, "absent", asText(file["state"]), "the sentinel removal does not remove the file")
	assert.Equal(t, "{{ nova_release_window_file }}", asText(file["path"]),
		"the sentinel removal does not remove nova_release_window_file")
	assert.Equal(t, false, task["failed_when"],
		"the sentinel removal fails silently (failed_when is not false, so the refusal below is never read)")
}

// assertRefusesOnAFailedRemoval pins that the step after the removal fails with
// the contract's text, and that the refusal reads a signal failed_when: false
// does not erase. Task._validate_failed_when (lib/ansible/playbook/task.py)
// wraps a non-list scalar as a one-element list, and _evaluate_failed_when_result
// (lib/ansible/executor/task_executor.py) then sets result['failed'] to that
// result, so seat_window_removed.failed is always false. The file module sets
// msg only on failure (fail_json), and that field survives, so a that that
// reads .failed is the silent leave the contract forbids.
func assertRefusesOnAFailedRemoval(t *testing.T, tasks []map[string]any, removeName string) {
	t.Helper()
	i := taskIndex(tasks, removeName)
	require.GreaterOrEqual(t, i, 0, "no sentinel removal to check")
	for _, task := range tasks[i+1:] {
		if _, ok := task["ansible.builtin.assert"].(map[string]any); !ok {
			continue
		}
		assertRefusalReadsTheSurvivingSignal(t, task)
		return
	}
	require.FailNow(t, "the sentinel removal has no assertion naming "+windowRefused)
}

// assertRefusalReadsTheSurvivingSignal pins the refusal's that: it names msg,
// the signal the file module sets only on failure, and not .failed, which
// failed_when: false overwrites (lib/ansible/executor/task_executor.py,
// _evaluate_failed_when_result). Reverting that to the .failed form turns this
// red, so a permission denial cannot pass as a silent leave.
func assertRefusalReadsTheSurvivingSignal(t *testing.T, task map[string]any) {
	t.Helper()
	a, ok := task["ansible.builtin.assert"].(map[string]any)
	require.True(t, ok, "the step after the sentinel removal is not the refusal")
	assert.Contains(t, asText(a["fail_msg"]), windowRefused,
		"a removal that cannot remove the file does not fail with %q", windowRefused)
	that := asText(a["that"])
	assert.Contains(t, that, "msg",
		"the refusal does not read msg, the signal failed_when: false leaves; .failed is overwritten and a permission denial is a silent leave")
	assert.NotContains(t, that, ".failed",
		"the refusal reads .failed, which failed_when: false overwrites, so a permission denial is a silent leave")
}

// renderWindowDocument is the play's own content template, rendered with fixed
// variables: nova_version, the two date stamps the play's date commands would
// print, and the back_in_s expression's fixed siblings. No clock is read and no
// ansible runs.
func renderWindowDocument(t *testing.T, write map[string]any) string {
	t.Helper()
	cp, ok := write["ansible.builtin.copy"].(map[string]any)
	require.True(t, ok, "the sentinel write is not an ansible.builtin.copy task")
	doc := asText(cp["content"])
	require.NotEmpty(t, doc, "the sentinel write copies no content")
	for _, r := range []struct{ token, value string }{
		{"{{ nova_version }}", "v9.9.9"},
		{"{{ seat_window_now.stdout }}", "2026-10-14T00:00:00Z"},
		{"{{ seat_window_until.stdout }}", "2026-10-14T00:30:00Z"},
		{"{{ seat_window_back }}", "360"},
	} {
		require.Contains(t, doc, r.token, "the sentinel template is missing %s", r.token)
		doc = strings.ReplaceAll(doc, r.token, r.value)
	}
	return doc
}
