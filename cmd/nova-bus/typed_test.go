package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendBuilderRefusals pins that nova-bus send constructs its buswire.Envelope
// through the generated builder, sets every field, and refuses on stderr at
// exit 2 in a single line without writing to Redis when any constraint fails.
func TestSendBuilderRefusals(t *testing.T) {
	t.Parallel()
	manyNames := make([]string, 65)
	for i := range manyNames {
		manyNames[i] = fmt.Sprintf("u%02d", i)
	}
	base := func(extra ...string) []string {
		return append([]string{"send", "--as", "ada", "--to", "bob", "--subject", "hello", "--body", "the body"}, extra...)
	}
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"missing from", []string{"send", "--to", "bob", "--subject", "s", "--body", "x"}, "", "--as is required"},
		{"missing to", []string{"send", "--as", "ada", "--subject", "s", "--body", "x"}, "", "--to is required"},
		{"empty to", []string{"send", "--as", "ada", "--to", " , ", "--subject", "s", "--body", "x"}, "", "no recipient"},
		{"missing subject", []string{"send", "--as", "ada", "--to", "bob", "--body", "x"}, "", "--subject is required"},
		{"empty subject", []string{"send", "--as", "ada", "--to", "bob", "--subject", "   ", "--body", "x"}, "", "--subject is required"},
		{"missing body", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s"}, "", "exactly one of --body"},
		{"empty body", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "   "}, "", "the body is empty"},
		{"empty kind", base("--kind", ""), "", "ENVELOPE REFUSED"},
		{"unknown kind", base("--kind", "gossip"), "", "ENVELOPE REFUSED: kind \"gossip\" is not one of"},
		{"ask without deadline", base("--kind", "ask"), "", "an ask wants --deadline"},
		{"oversize subject", []string{"send", "--as", "ada", "--to", "bob", "--subject", strings.Repeat("s", 201), "--body", "x"}, "", "ENVELOPE REFUSED: subject over 200 bytes"},
		{"oversize body flag", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", strings.Repeat("b", bus.MaxBody+1)}, "", "ENVELOPE REFUSED: body over 1048576 bytes"},
		{"oversize body stdin", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--stdin"}, strings.Repeat("b", bus.MaxBody+1), "ENVELOPE REFUSED: body over 1048576 bytes"},
		{"over 64 to repeated", []string{"send", "--as", "ada", "--to", strings.Join(slices.Repeat([]string{"bob"}, 65), ","), "--subject", "s", "--body", "x"}, "", "ENVELOPE REFUSED: to over 64 names"},
		{"over 64 to distinct", []string{"send", "--as", "ada", "--to", strings.Join(manyNames, ","), "--subject", "s", "--body", "x"}, "", "ENVELOPE REFUSED: to over 64 names"},
		{"over 64 cc", base("--cc", strings.Join(slices.Repeat([]string{"bob"}, 65), ",")), "", "ENVELOPE REFUSED: cc over 64 names"},
		{"oversize to name", []string{"send", "--as", "ada", "--to", strings.Repeat("b", 65), "--subject", "s", "--body", "x"}, "", "at most 64"},
		{"non-hex head", base("--head", "not-a-40-hex-sha"), "", `ENVELOPE REFUSED: head "not-a-40-hex-sha" is not 40 lowercase hex characters`},
		{"short hex head", base("--head", "3f9c"), "", `ENVELOPE REFUSED: head "3f9c" is not 40 lowercase hex characters`},
		{"uppercase hex head", base("--head", strings.Repeat("3F9C", 10)), "", "is not 40 lowercase hex characters"},
		{"oversize head", base("--head", strings.Repeat("a", 41)), "", "ENVELOPE REFUSED: head over 40 bytes"},
		{"oversize re", base("--re", strings.Repeat("r", 65)), "", "ENVELOPE REFUSED: re over 64 bytes"},
		{"oversize card", base("--card", strings.Repeat("c", 65)), "", "ENVELOPE REFUSED: card over 64 bytes"},
		{"oversize token", base("--token", strings.Repeat("t", 129)), "", "ENVELOPE REFUSED: token over 128 bytes"},
		{"nul in subject", []string{"send", "--as", "ada", "--to", "bob", "--subject", "a\x00b", "--body", "x"}, "", "ENVELOPE REFUSED: malformed (subject)"},
		{"non-utf8 subject", []string{"send", "--as", "ada", "--to", "bob", "--subject", "a\xffb", "--body", "x"}, "", "ENVELOPE REFUSED: malformed (subject)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(append([]string{"ada", "bob"}, manyNames...)...)
			res := r.cli().RunIn(tc.stdin, tc.args...)
			assert.Equal(t, 2, res.Code, "%s: stdout=%q stderr=%q", tc.name, res.Stdout, res.Stderr)
			assert.Empty(t, res.Stdout, "%s: a refused send prints nothing on stdout", tc.name)
			trimmed := strings.TrimSuffix(res.Stderr, "\n")
			assert.NotEmpty(t, trimmed, tc.name)
			assert.Equal(t, 0, strings.Count(trimmed, "\n"), "%s: one line on stderr: %q", tc.name, res.Stderr)
			assert.True(t, strings.HasPrefix(trimmed, "SEND REFUSED: "), "%s: %q", tc.name, res.Stderr)
			assert.Contains(t, trimmed, tc.want, tc.name)
			assert.Equal(t, 0, r.store.Len(bus.LogKey), "%s: writes nothing to the log", tc.name)
			assert.Equal(t, 0, r.store.Len(bus.StreamOf("bob")), "%s: writes nothing to the recipient stream", tc.name)
		})
	}

	t.Run("exact bounds land and carry card and head", func(t *testing.T) {
		t.Parallel()
		r := newRig("ada", "bob")
		head := strings.Repeat("3f9c", 10)
		card := strings.Repeat("c", 64)
		re := strings.Repeat("r", 64)
		subj := strings.Repeat("s", 200)
		tok := strings.Repeat("t", 128)
		sent := r.cli().OK(t, "send", "--as", "ada", "--to", "bob", "--subject", subj, "--body", "ok\n",
			"--re", re, "--card", card, "--head", head, "--token", tok)
		mid := id(t, sent.Stdout)
		log, err := (&bus.Bus{Store: r.store}).Log(context.Background(), "-")
		require.NoError(t, err)
		require.Len(t, log, 1)
		m := log[0].Message()
		assert.Equal(t, mid, m.ID)
		assert.Equal(t, card, m.Card)
		assert.Equal(t, head, m.Head)
		assert.Equal(t, re, m.Re)
		assert.Equal(t, subj, m.Subject)
	})
}

// TestShowRendersText pins that nova-bus show renders an envelope in the
// schema's name-keyed text form, from the store by --id (legacy or native
// binary) or from --stdin in any of the envelope's forms, and refuses damage at
// exit 2.
func TestShowRendersText(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob", "cy")
	cli := r.cli()
	head := "8f3c2a1b4d5e6f708192a3b4c5d6e7f8091a2b3c"
	card := "l1-nova-bus-reader-builder-and-show-2"
	sent := cli.OK(t, "send", "--as", "ada", "--to", "bob,cy", "--cc", "ada",
		"--re", "01JA2B3C4D5E6F7G8H9J0KMNPA", "--kind", "ask", "--deadline", "10m",
		"--card", card, "--head", head, "--subject", "#202 HOLD at 3f9c",
		"--body", "the reader found it broken:\n- a line\n")
	mid := id(t, sent.Stdout)

	got := cli.OK(t, "show", "--as", "bob", "--id", mid)
	assert.Empty(t, got.Stderr)
	for _, piece := range []string{
		`"id": "` + mid + `"`,
		`"from": "ada"`,
		`"s": "bob"`,
		`"s": "cy"`,
		`"s": "ada"`,
		`"re": "01JA2B3C4D5E6F7G8H9J0KMNPA"`,
		`"kind": "ask"`,
		`"subject": "#202 HOLD at 3f9c"`,
		`"card": "` + card + `"`,
		`"head": "` + head + `"`,
		`"at_ms": 1791028801000`,
		`"due_ms": 1791029401000`,
		`"body": "the reader found it broken:\n- a line\n"`,
	} {
		assert.Contains(t, got.Stdout, piece)
	}

	reader := bus.NewEnvelopeReader()
	parsed, form, err := reader.ReadText([]byte(got.Stdout))
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormJSON, form)
	assert.Equal(t, mid, parsed.ID)
	assert.Equal(t, card, parsed.Card)
	assert.Equal(t, head, parsed.Head)
	wantText, err := reader.WriteText(parsed, schemaread.FormJSON)
	require.NoError(t, err)
	assert.Equal(t, string(wantText), got.Stdout, "show prints the exact name-keyed text form")

	wire, err := bus.EnvelopeWire(parsed)
	require.NoError(t, err)
	b64 := base64.StdEncoding.EncodeToString(wire)
	openedBefore := r.opened
	for name, in := range map[string]string{
		"binary": string(wire),
		"base64": b64,
		"json":   got.Stdout,
	} {
		res := cli.OKIn(t, in, "show", "--stdin")
		assert.Equal(t, got.Stdout, res.Stdout, name)
	}
	assert.Equal(t, openedBefore, r.opened, "show --stdin does not dial the store")

	cli.DoIn(t, string(wire[:len(wire)-1]), "show", "--stdin").Exit(2).
		Err("SHOW REFUSED: ENVELOPE REFUSED: wire_damaged; run: nova-bus send -h")
	cli.Do(t, "show", "--as", "bob", "--id", "01ARZ3NDEKTSV4RRFFQ69G5FAV").Exit(1).
		Err("SHOW NONE: no message 01ARZ3NDEKTSV4RRFFQ69G5FAV")
}

func TestShowFindsBeyondLogWindow(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	for i := range 10000 {
		require.NoError(t, r.store.AddAll(context.Background(), []string{bus.LogKey}, map[string]string{"id": fmt.Sprintf("old-%d", i)}))
	}
	sent := r.cli().OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "later", "--body", "later body\n")
	got := r.cli().OK(t, "show", "--as", "bob", "--id", id(t, sent.Stdout))
	assert.Contains(t, got.Stdout, `"subject": "later"`)
}

func TestNativeViewsRefuseDamage(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	fields, err := bus.NativeFields(bus.Message{
		ID: "01JA2B3C4D5E6F7G8H9J0KMNPZ", From: "ada", To: []string{"bob"},
		Subject: "damaged", Kind: bus.KindStatus, At: r.clock, Body: "x\n",
	})
	require.NoError(t, err)
	wire := fields[bus.EnvelopeField]
	fields[bus.EnvelopeField] = wire[:len(wire)-1]
	require.NoError(t, r.store.AddAll(context.Background(), []string{bus.StreamOf("bob"), bus.LogKey}, fields))
	for _, args := range [][]string{{"peek", "--as", "bob"}, {"log", "--as", "bob"}, {"wait", "--as", "bob", "--after", "0-0", "--timeout", "1ms"}} {
		res := r.cli().Run(args...)
		assert.Equal(t, 2, res.Code, "%v: stdout=%q stderr=%q", args, res.Stdout, res.Stderr)
		assert.Contains(t, res.Stderr, "wire_damaged", "%v", args)
	}
	assert.Equal(t, uint64(3), r.envelopes.Snapshot()["refused_wire_damaged"], "each view counts the refusal")
}

// TestRecvDualRead pins that nova-bus recv routes every stream entry through
// the pkg/schemaread dual-read helper, reading both the legacy stream fields
// and the native binary envelope during the cutover window, incrementing the
// per-form read counter, and refusing a damaged native envelope at exit 2.
func TestRecvDualRead(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	ctx := context.Background()

	// 1. Legacy entry (no native envelope field on the stream).
	legacyID := id(t, cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "from legacy", "--body", "legacy body\n").Stdout)

	// 2. Native binary entry (carries EnvelopeField on the stream).
	nativeBus := &bus.Bus{Store: r.store, Native: true}
	nativeSent, err := nativeBus.Send(ctx, bus.Message{
		From: "ada", To: []string{"bob"}, Subject: "from native", Kind: bus.KindReport,
		Card: "l1-nova-bus-reader-builder-and-show-2", Head: strings.Repeat("abcd", 10),
		Body: "native body\n",
	})
	require.NoError(t, err)
	nativeEntries, err := r.store.Range(ctx, bus.StreamOf("bob"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, nativeEntries, 2)
	oldReader := bus.Parse(nativeEntries[1].Fields)
	assert.Equal(t, nativeSent.ID, oldReader.ID, "the old reader still sees a native writer's legacy fields")
	assert.Equal(t, nativeSent.Subject, oldReader.Subject)
	assert.Equal(t, nativeSent.Body, oldReader.Body)
	assert.Equal(t, nativeSent.Kind, oldReader.Kind)

	gotLegacy := cli.OK(t, "recv", "--as", "bob", "--ack")
	assert.Contains(t, gotLegacy.Stdout, "RECV OK id="+legacyID+" from=ada to=bob")
	assert.Contains(t, gotLegacy.Stdout, "subject=\"from legacy\"\n\nlegacy body\n")
	assert.Equal(t, uint64(1), r.envelopes.Reads(schemaread.FormLegacy), "legacy read counted")
	assert.Equal(t, uint64(0), r.envelopes.Reads(schemaread.FormBinary), "no binary read yet")

	gotNative := cli.OK(t, "recv", "--as", "bob", "--ack")
	assert.Contains(t, gotNative.Stdout, "RECV OK id="+nativeSent.ID+" from=ada to=bob")
	assert.Contains(t, gotNative.Stdout, "kind=report")
	assert.Contains(t, gotNative.Stdout, "subject=\"from native\"\n\nnative body\n")
	assert.Equal(t, uint64(1), r.envelopes.Reads(schemaread.FormLegacy))
	assert.Equal(t, uint64(1), r.envelopes.Reads(schemaread.FormBinary), "native binary read counted")

	// 3. Damaged native binary entry on the stream is refused at exit 2, never
	// patched from the legacy fields beside it.
	goodFields, err := bus.NativeFields(bus.Message{
		ID: "01JA2B3C4D5E6F7G8H9J0KMNPZ", From: "ada", To: []string{"bob"},
		Subject: "damaged", Kind: bus.KindStatus, At: r.clock.Add(time.Second), Body: "x\n",
	})
	require.NoError(t, err)
	badFields := maps.Clone(goodFields)
	raw := badFields[bus.EnvelopeField]
	badFields[bus.EnvelopeField] = raw[:len(raw)-1]
	require.NoError(t, r.store.AddAll(ctx, []string{bus.StreamOf("bob"), bus.LogKey}, badFields))

	res := cli.Do(t, "recv", "--as", "bob").Exit(2)
	trimmed := strings.TrimSuffix(res.Stderr, "\n")
	assert.Equal(t, 0, strings.Count(trimmed, "\n"), "one line on stderr: %q", res.Stderr)
	assert.Contains(t, trimmed, "RECV REFUSED: ENVELOPE REFUSED: wire_damaged; run: nova-bus send -h")
	assert.Positive(t, r.envelopes.Snapshot()["refused_wire_damaged"])
}
