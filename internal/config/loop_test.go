package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The loop kind (kind.go: Kinds, "loop"; docs/SPEC-CONFIG.md, "loop"): a
// supervised process on one machine, every value data in the row.

// loopRig is a store with the machines the loop tests name and nothing else.
type loopRig struct {
	ctx  context.Context
	st   *Mem
	kind *Kind
}

func newLoopRig(t *testing.T, machines ...string) loopRig {
	t.Helper()
	r := loopRig{ctx: context.Background(), st: NewMem()}
	r.kind, _ = Lookup(KindLoop)
	require.NotNil(t, r.kind, "the loop kind is registered")
	for _, m := range machines {
		_, err := r.st.Insert(r.ctx, KindMachine, Row{Name: m, Fields: map[string]string{"user": "u", "seat": "s-" + m, "slots": "4", "runners": "0"}}, "t")
		require.NoError(t, err)
	}
	return r
}

// add builds the row from raw flag values and inserts it.
func (r loopRig) add(t *testing.T, name string, raw map[string]string) Row {
	t.Helper()
	row, err := r.kind.NewRow(name, raw)
	require.NoError(t, err)
	_, err = r.st.Insert(r.ctx, KindLoop, row, "t")
	require.NoError(t, err)
	return row
}

// keptAlive is the raw of a long-running loop on m.
func keptAlive(m string) map[string]string {
	return map[string]string{"machine": m, "argv": `["/bin/prog","--loop"]`, "keepalive": "true"}
}

func TestTheLoopRowIsTheRecordThePlaysRead(t *testing.T) {
	t.Parallel()

	k, ok := Lookup(KindLoop)
	require.True(t, ok)
	assert.Equal(t, "loops", k.Table)
	assert.False(t, k.Singleton, "a loop kind is many rows")
	assert.Equal(t, "machine,argv,seat,keys,every,keepalive,enabled", strings.Join(k.FieldNames(), ","), "the plays render units from exactly these names")
	types := map[string]Type{"machine": TypeRef, "argv": TypeArgv, "seat": TypeText, "keys": TypeKeys, "every": TypeInt, "keepalive": TypeBool, "enabled": TypeBool}
	for _, f := range k.Fields {
		assert.Equal(t, types[f.Name], f.Type, "--%s", f.Name)
		assert.Equal(t, f.Name == "machine" || f.Name == "argv", f.Required, "--%s required", f.Name)
	}
	machine, _ := k.Field("machine")
	assert.Equal(t, KindMachine, machine.Ref)
	for _, invented := range []string{"log", "user", "host", "friend", "schedule", "note", "command", "width"} {
		_, has := k.Field(invented)
		assert.False(t, has, "loop has a field %s: the log is derived, the user is the machine's, the width is the machine row's, the rest is not a fact anything reads", invented)
	}
	assert.Equal(t, "~/nova-bench/loops/l1.log", LoopLog("~/nova-bench/loops", "l1"))
	names := strings.Join(KindNames(), ",")
	assert.Less(t, strings.Index(names, KindMachine), strings.Index(names, KindLoop), "loops apply after machines: each names one")
}

func TestLoopNewRowCanonicalisesAndRefusesEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	k, _ := Lookup(KindLoop)
	long := make([]string, MaxArgs+1)
	for i := range long {
		long[i] = fmt.Sprintf(`"w%d"`, i)
	}
	big := `["/bin/prog","` + strings.Repeat("x", MaxArgvBytes) + `"]`
	cases := []struct {
		name string
		raw  map[string]string
		want map[string]string // the canonical fields asserted, when the row is accepted
		errs []string          // every phrase the one refusal must say
	}{
		{
			name: "a periodic loop with defaults",
			raw:  map[string]string{"machine": "m1", "argv": `[ "/bin/prog" , "--once" ]`, "every": "60"},
			want: map[string]string{"argv": `["/bin/prog","--once"]`, "every": "60", "keepalive": "false", "enabled": "true", "seat": "", "keys": ""},
		},
		{
			name: "a member loop kept alive with secrets by name",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/member","--as","m1"]`, "keepalive": "TRUE", "seat": "s1", "keys": "Z_KEY, A_KEY,Z_KEY", "enabled": "0"},
			want: map[string]string{"keepalive": "true", "enabled": "false", "keys": "A_KEY,Z_KEY", "every": "0"},
		},
		{
			name: "an argument with spaces and an equals sign is kept exactly",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog","a b=c",""]`, "keepalive": "true"},
			want: map[string]string{"argv": `["/bin/prog","a b=c",""]`},
		},
		{
			name: "both every and keepalive",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog"]`, "every": "5", "keepalive": "true"},
			errs: []string{"a loop runs every n seconds or is kept alive"},
		},
		{
			name: "neither every nor keepalive",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog"]`},
			errs: []string{"has neither --every nor --keepalive"},
		},
		{
			name: "secret names with no seat",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog"]`, "every": "5", "keys": "A_KEY"},
			errs: []string{"no --seat to open them from"},
		},
		{
			name: "a secret value in place of a name",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog"]`, "every": "5", "seat": "s1", "keys": "A_KEY=hunter2"},
			errs: []string{"--keys", "never by value"},
		},
		{
			name: "a key that is no variable name",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog"]`, "every": "5", "seat": "s1", "keys": "9KEY"},
			errs: []string{`--keys "9KEY": want a comma list of variable names`},
		},
		{
			name: "argv as a shell line",
			raw:  map[string]string{"machine": "m1", "argv": "/bin/prog --loop", "every": "5"},
			errs: []string{"--argv: want the command as a JSON array of strings"},
		},
		{
			name: "argv with trailing text",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog"] ["x"]`, "every": "5"},
			errs: []string{"--argv: want the command as a JSON array"},
		},
		{
			name: "argv of no words",
			raw:  map[string]string{"machine": "m1", "argv": `[]`, "every": "5"},
			errs: []string{"--argv: the command names no program"},
		},
		{
			name: "argv with an empty program",
			raw:  map[string]string{"machine": "m1", "argv": `["","x"]`, "every": "5"},
			errs: []string{"--argv: the command names no program"},
		},
		{
			name: "argv with a line break in a word",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog","a\nb"]`, "every": "5"},
			errs: []string{"--argv: word 1 holds a line break"},
		},
		{
			name: "argv over the word bound",
			raw:  map[string]string{"machine": "m1", "argv": "[" + strings.Join(long, ",") + "]", "every": "5"},
			errs: []string{fmt.Sprintf("over the maximum of %d", MaxArgs)},
		},
		{
			name: "argv over the byte bound",
			raw:  map[string]string{"machine": "m1", "argv": big, "every": "5"},
			errs: []string{fmt.Sprintf("over the maximum of %d", MaxArgvBytes)},
		},
		{
			name: "a bad every and secrets with no seat, all at once",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog"]`, "every": "soon", "keys": "A_KEY"},
			errs: []string{`--every "soon"`, "no --seat to open them from"},
		},
		{
			name: "a bad keepalive and secrets with no seat, all at once",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog"]`, "every": "5", "keepalive": "perhaps", "keys": "A_KEY"},
			errs: []string{`--keepalive "perhaps"`, "no --seat to open them from"},
		},
		{
			name: "both every and keepalive and secrets with no seat, all at once",
			raw:  map[string]string{"machine": "m1", "argv": `["/bin/prog"]`, "every": "5", "keepalive": "true", "keys": "A_KEY"},
			errs: []string{"a loop runs every n seconds or is kept alive", "no --seat to open them from"},
		},
		{
			name: "a bad argv and neither every nor keepalive, all at once",
			raw:  map[string]string{"machine": "m1", "argv": "/bin/prog --loop"},
			errs: []string{"--argv: want the command as a JSON array of strings", "has neither --every nor --keepalive"},
		},
		{
			name: "a bool that is not one, a negative every, and no machine, all at once",
			raw:  map[string]string{"argv": `["/bin/prog"]`, "every": "-1", "enabled": "maybe"},
			errs: []string{"--machine is required", `--enabled "maybe": want true or false`, `--every "-1": want a non-negative integer`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row, err := k.NewRow("l1", tc.raw)
			if len(tc.errs) > 0 {
				require.Error(t, err)
				for _, want := range tc.errs {
					assert.Contains(t, err.Error(), want)
				}
				assert.NotContains(t, err.Error(), "\n", "one refusal line")
				return
			}
			require.NoError(t, err)
			for f, want := range tc.want {
				assert.Equal(t, want, row.Fields[f], "--%s", f)
			}
			words := Argv(row.Fields["argv"])
			require.NotEmpty(t, words, "the canonical argv decodes")
			assert.True(t, strings.HasPrefix(words[0], "/bin/"), "the program is the first word: %v", words)
		})
	}
}

func TestLoopSetIsCheckedOnTheRowItWouldLeave(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		changes map[string]string
		refuse  string // "" when the set is accepted
	}{
		{name: "every alone on a kept-alive loop", changes: map[string]string{"every": "30"}, refuse: "a loop runs every n seconds or is kept alive"},
		{name: "keepalive off with no every", changes: map[string]string{"keepalive": "false"}, refuse: "has neither --every nor --keepalive"},
		{name: "keys with no seat", changes: map[string]string{"keys": "A_KEY"}, refuse: "no --seat to open them from"},
		{name: "every and keepalive together turn it periodic", changes: map[string]string{"every": "30", "keepalive": "false"}},
		{name: "seat and keys together", changes: map[string]string{"seat": "s1", "keys": "A_KEY,B_KEY"}},
		{name: "disabled", changes: map[string]string{"enabled": "false"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLoopRig(t, "m1")
			r.add(t, "l1", keptAlive("m1"))
			before, _ := r.st.History(r.ctx, KindLoop, "l1")
			changes, err := r.kind.Changes(tc.changes)
			require.NoError(t, err, "each field is valid alone")
			after, _, err := r.st.Update(r.ctx, KindLoop, "l1", changes, "t")
			hist, _ := r.st.History(r.ctx, KindLoop, "l1")
			if tc.refuse != "" {
				require.Error(t, err)
				assert.True(t, errors.Is(err, ErrInvalid), "a refusal (exit 1), not a failure: %v", err)
				assert.Contains(t, err.Error(), tc.refuse)
				assert.Len(t, hist, len(before), "a refused set writes no history")
				return
			}
			require.NoError(t, err)
			for f, v := range changes {
				assert.Equal(t, v, after.Fields[f])
			}
			assert.Len(t, hist, len(before)+1, "the set is one history row")
		})
	}
}

func TestALoopNamesAMachineRowAndHoldsIt(t *testing.T) {
	t.Parallel()

	r := newLoopRig(t, "m1", "m2")
	row, err := r.kind.NewRow("l1", keptAlive("m9"))
	require.NoError(t, err)
	_, err = r.st.Insert(r.ctx, KindLoop, row, "t")
	require.ErrorIs(t, err, ErrNoRef)
	assert.Contains(t, err.Error(), "--machine m9 names no machine row")

	r.add(t, "l1", keptAlive("m1"))
	r.add(t, "l2", keptAlive("m1"))
	_, err = r.st.Delete(r.ctx, KindMachine, "m1", "t")
	require.ErrorIs(t, err, ErrReferenced)
	assert.Contains(t, err.Error(), "machine m1 is the --machine of loop l1,l2")
	_, err = r.st.Delete(r.ctx, KindMachine, "m2", "t")
	assert.NoError(t, err, "a machine no loop names is removed")

	_, _, err = r.st.Update(r.ctx, KindLoop, "l1", map[string]string{"machine": "m2"}, "t")
	require.ErrorIs(t, err, ErrNoRef, "a set is held to the ref as an add is")
}

func TestApplyWritesLoopsAfterMachinesAndReachesParity(t *testing.T) {
	t.Parallel()

	r := newLoopRig(t, "m1")
	r.add(t, "l1", keptAlive("m1"))
	r.add(t, "l2", map[string]string{"machine": "m1", "argv": `["/bin/once"]`, "every": "300", "seat": "s1", "keys": "A_KEY"})
	ap := newFake()
	var lines []string
	report := func(op Op) { lines = append(lines, OpLine("APPLY", KindLoop, op)) }

	res, err := Apply(r.ctx, r.st, ap, KindLoop, "t", false, report)
	require.NoError(t, err)
	assert.Equal(t, []string{"APPLY ADD kind=loop name=l1", "APPLY ADD kind=loop name=l2"}, lines)
	rev, _ := r.st.Rev(r.ctx, KindLoop)
	assert.Equal(t, rev, ap.revs[KindLoop], "the stamp is the loop kind's revision")
	assert.Equal(t, `["/bin/once"]`, ap.views[KindLoop]["l2"]["argv"])
	assert.Equal(t, 2, res.Add)

	lines = nil
	res, err = Apply(r.ctx, r.st, ap, KindLoop, "t", false, report)
	require.NoError(t, err)
	assert.Empty(t, lines, "a second apply of the same rows writes nothing")
	assert.Zero(t, res.Add+res.Set+res.Remove)

	_, _, err = r.st.Update(r.ctx, KindLoop, "l1", map[string]string{"enabled": "false"}, "t")
	require.NoError(t, err)
	_, err = r.st.Delete(r.ctx, KindLoop, "l2", "t")
	require.NoError(t, err)
	_, err = Apply(r.ctx, r.st, ap, KindLoop, "t", false, report)
	require.NoError(t, err)
	assert.Equal(t, []string{"APPLY SET kind=loop name=l1 changed=enabled", "APPLY REMOVE kind=loop name=l2"}, lines)
	rev, _ = r.st.Rev(r.ctx, KindLoop)
	assert.Equal(t, rev, ap.revs[KindLoop])
}

// A field that is refused on its own does not also trip the rule that reads
// it: the refusal says what is wrong with the field and nothing spurious
// about the pair it belongs to.
func TestLoopAddNamesEachProblemOnceAndNoSpuriousRule(t *testing.T) {
	t.Parallel()

	k, _ := Lookup(KindLoop)
	_, err := k.NewRow("l1", map[string]string{"machine": "m1", "argv": `["/bin/prog"]`, "every": "soon", "keys": "A_KEY"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, `--every "soon": want a non-negative integer`)
	assert.Contains(t, msg, "no --seat to open them from")
	assert.Equal(t, 1, strings.Count(msg, "--every"), "each problem once: %s", msg)
	assert.NotContains(t, msg, "neither --every nor --keepalive", "every is refused on its own; the pair rule waits for a valid every")
	assert.NotContains(t, msg, "\n")
}

// argv is stored as JSON with & < > as themselves, so list and show read as
// the command was typed, and the value decodes to the same words.
func TestLoopArgvKeepsHTMLCharactersReadable(t *testing.T) {
	t.Parallel()

	k, _ := Lookup(KindLoop)
	want := []string{"/bin/sh", "-c", "a && b > out.txt < in.txt; echo '<&>'"}
	raw, err := marshalArgv(want)
	require.NoError(t, err)
	row, err := k.NewRow("l1", map[string]string{"machine": "m1", "argv": string(raw), "every": "5"})
	require.NoError(t, err)
	got := row.Fields["argv"]
	assert.Equal(t, string(raw), got)
	for _, c := range []string{"&&", ">", "<"} {
		assert.Contains(t, got, c)
	}
	assert.NotContains(t, got, `\u00`, "no escape for & < >")
	assert.Equal(t, want, Argv(got), "the round trip keeps every word")

	// An escaped spelling given on the command line is stored readable.
	row, err = k.NewRow("l2", map[string]string{"machine": "m1", "argv": `["/bin/sh","-c","a \u0026\u0026 b \u003e c"]`, "every": "5"})
	require.NoError(t, err)
	assert.Equal(t, `["/bin/sh","-c","a && b > c"]`, row.Fields["argv"])
}
