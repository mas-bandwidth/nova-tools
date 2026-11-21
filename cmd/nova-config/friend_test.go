package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friends is a store with two friends: one at its defaults, one with every field.
func friends(t *testing.T) *harness {
	h := newHarness()
	h.ok(t, "friend", "add", "alex", "--harness", "codex", "--actor", "t")
	h.ok(t, "friend", "add", "alice", "--harness", "claude", "--model", "opus",
		"--pronouns", "she/her", "--note", "kin", "--enabled", "false", "--actor", "t")
	return h
}

func TestFriendListAndShow(t *testing.T) {
	t.Parallel()
	h := friends(t)
	assert.Equal(t, "FRIEND name=alex harness=codex model=- pronouns=- note=- enabled=true\n"+
		`FRIEND name=alice harness=claude model=opus pronouns=she/her note="kin" enabled=false`+"\n"+
		"CONFIG LIST kind=friend rows=2\n", h.ok(t, "friend", "list"))
	assert.Equal(t, `FRIEND name=alice harness=claude model=opus pronouns=she/her note="kin" enabled=false created=2026-01-01T00:00:00Z updated=2026-01-02T00:00:00Z`+"\n",
		h.ok(t, "friend", "show", "alice"), "a friend's show names no services")
	assert.Equal(t, `{"result":{"verb":"friend list","status":"ok","exit":0},"facts":{"kind":"friend","rows":2},"items":[`+
		`{"kind":"friend","fields":{"name":"alex","harness":"codex","model":"","pronouns":"","note":"","enabled":true}},`+
		`{"kind":"friend","fields":{"name":"alice","harness":"claude","model":"opus","pronouns":"she/her","note":"kin","enabled":false}}]}`+"\n",
		h.ok(t, "friend", "list", "--json"))
	var got struct {
		Items []struct{ Fields map[string]any }
	}
	require.NoError(t, json.Unmarshal([]byte(h.ok(t, "friend", "show", "alex", "--json")), &got))
	assert.Equal(t, "2026-01-01T00:00:00Z", got.Items[0].Fields["created"])
	assert.NotContains(t, got.Items[0].Fields, "services")
}

func TestFriendWrites(t *testing.T) {
	t.Parallel()
	h := friends(t)
	assert.Equal(t, "CONFIG ADD kind=friend name=zhi rev=3\n", h.ok(t, "friend", "add", "zhi", "--harness", "muse", "--actor", "a1"))
	c := h.st.history[2]
	assert.Equal(t, map[string]string{"harness": "muse", "model": "", "pronouns": "", "note": "", "enabled": "true"}, c.after)
	assert.Equal(t, "CONFIG SET kind=friend name=alex rev=4 changed=model\n", h.ok(t, "friend", "set", "alex", "--model", "gpt", "--harness", "codex", "--actor", "a1", "--reason", "newer"))
	c = h.st.history[3]
	assert.Equal(t, []string{"friend", "alex", "set", "a1"}, []string{c.kind, c.name, c.op, c.actor})
	assert.Equal(t, "", c.before["model"])
	assert.Equal(t, "newer", c.after["reason"])
	assert.Equal(t, "CONFIG SET kind=friend name=alex changed=none rev=unchanged\n", h.ok(t, "friend", "set", "alex", "--model", "gpt", "--actor", "a1"))
	assert.Equal(t, "CONFIG REMOVE kind=friend name=zhi rev=5\n", h.ok(t, "friend", "remove", "zhi", "--actor", "a1"))
	assert.NotContains(t, h.st.rows["friend"], "zhi")
	assert.Contains(t, h.ok(t, "history", "friend", "alex"), "HISTORY at=")
}

func TestFriendDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	h := friends(t)
	n := len(h.st.history)
	assert.Equal(t, "CONFIG DRY-RUN op=set kind=friend name=alex actor=a1 wrote=nothing\n  model: \"\" -> \"gpt\"\n  pronouns: \"\" -> \"he/him\"\n",
		h.ok(t, "friend", "set", "alex", "--model", "gpt", "--pronouns", "he/him", "--actor", "a1", "--dry-run"))
	assert.Contains(t, h.ok(t, "friend", "add", "new", "--harness", "grok", "--actor", "a1", "--dry-run"), "CONFIG DRY-RUN op=add kind=friend name=new actor=a1 wrote=nothing\n  harness: \"grok\"\n")
	assert.Contains(t, h.ok(t, "friend", "remove", "alice", "--actor", "a1", "--dry-run"), "  model: \"opus\"\n")
	assert.Len(t, h.st.history, n)
}

func TestFriendRefusals(t *testing.T) {
	t.Parallel()
	h := friends(t)
	for _, c := range []struct {
		code int
		args []string
		want string
	}{
		{2, []string{"friend", "add", "f9", "--actor", "a"}, "missing required flag --harness; friend fields: name, harness, model, pronouns, note, enabled; run: nova-config friend add -h"},
		{2, []string{"friend", "add", "Bad_Name", "--harness", "claude", "--actor", "a"}, "want lower-case letters"},
		{1, []string{"friend", "add", "alex", "--harness", "claude", "--actor", "a"}, "friend alex exists; use: nova-config friend set alex"},
		{1, []string{"friend", "show", "nope"}, "friend nope not found; run: nova-config friend list"},
		{1, []string{"friend", "set", "nope", "--model", "m", "--actor", "a"}, "friend nope not found"},
		{1, []string{"friend", "remove", "nope", "--actor", "a"}, "friend nope not found"},
		// width and tiers are a sprint's own, kept by nova-sprint: no friend flag sets them
		{2, []string{"friend", "set", "alex", "--width", "2", "--actor", "a"}, "unknown flag --width; friend fields: name, harness, model, pronouns, note, enabled"},
		{2, []string{"friend", "add", "f8", "--harness", "claude", "--tiers", "pro", "--actor", "a"}, "unknown flag --tiers; friend fields:"},
		{2, []string{"friend", "set", "alex", "--enabled", "maybe", "--actor", "a"}, `--enabled "maybe": want true or false`},
		{2, []string{"friend", "set", "alex", "--harness", "", "--actor", "a"}, "--harness: want a value"},
		{2, []string{"friend", "set", "alex", "--slots", "4", "--actor", "a"}, "unknown flag --slots; friend fields:"},
		{2, []string{"friend", "set", "alex", "--actor", "a"}, "no field given to set"},
		{2, []string{"friend", "set", "alex", "--model", "m"}, "missing required flag --actor"},
		{2, []string{"friend", "set", "alex", "--model", "m", "--actor", "a", "--reason", "a\nb"}, "--reason wants one line"},
		{2, []string{"friend", "show"}, "nova-config friend show: wants a name"},
		{2, []string{"friend", "list", "x"}, "nova-config friend list: wants no name"},
		{2, []string{"friend"}, "nova-config friend: wants a verb; the verbs are list, show, add, set, remove"},
	} {
		code, out, errs := h.run(t, c.args...)
		assert.Equal(t, c.code, code, "%v: %s", c.args, errs)
		assert.Empty(t, out, "%v", c.args)
		assert.Contains(t, errs, c.want, "%v", c.args)
	}
	h.st.failure = errors.New("postgres: down")
	code, _, errs := h.run(t, "friend", "list")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "postgres: down")
}

// An empty pronouns is NULL in the store, so an older table's check (she/her,
// he/him, they/them) takes a friend whose pronouns are unstated.
func TestFriendUnstatedPronounsWriteNull(t *testing.T) {
	t.Parallel()
	row := Row{Name: "f1", F: map[string]string{"harness": "claude", "model": "", "pronouns": "", "note": "", "enabled": "true"}}
	q, args := writeSQL(kinds["friend"], "add", "f1", Row{}, row)
	assert.Equal(t, `INSERT INTO config.friends (name, "harness", "model", "pronouns", "note", "enabled") VALUES ($1, $2, $3, $4, $5, $6)`, q, "width and tiers keep their column defaults")
	assert.Equal(t, []any{"f1", "claude", "", nil, "", true}, args)
}

func TestDiagnoseFriendStillNamed(t *testing.T) {
	t.Parallel()
	p := pe("23503", "update or delete on table \"friends\" violates foreign key constraint")
	p.TableName = "sprint"
	assert.EqualError(t, diagnose(p, kinds["friend"], "remove", "coord", Row{}, nil, nil),
		"friend coord is still named by config.sprint; change that row first, then: nova-config friend remove coord --actor <you>")
}
