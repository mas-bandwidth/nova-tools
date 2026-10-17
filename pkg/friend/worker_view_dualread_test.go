package friend

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFriendWorkerViewDualRead pins the worker view reader (ParseView) reading
// both table forms, S1 and S2: one value written as the binary wire, as its
// padded standard base64, and as the schema JSON text form (the form the
// sprint server writes today) reads back equal through ParseView, each read
// counted under its form (UNION.md C13); and a truncated, a byte-flipped and
// a clamped value of each form is refused by name (<RECORD> REFUSED:
// <reason>; run: <help>), never read as absent, empty or zero.
func TestFriendWorkerViewDualRead(t *testing.T) {
	t.Parallel()

	want := []HeldCard{
		{Card: "a11", Job: "a11.w1~21", Col: "working", Why: ViewWhy},
		{Card: "a12", Job: "a12.w1~21", Col: "holding", Why: ViewWhy},
	}
	legacy := `{"view":"worker","as":"rowan","kind":"friend","epoch":21,"cards":[{"id":"a11","st":"working","brief":"/home/inbox/a11.w1~21/BRIEF.md"},{"id":"a12","st":"holding","brief":"/home/inbox/a12.w1~21/BRIEF.md"}]}`

	binary := workerViewWire(t, want)
	b64 := []byte(base64.StdEncoding.EncodeToString(binary))
	jsonForm := workerViewJSON(t, want)

	readsBefore := map[string]uint64{
		"binary": workerViewReader.Counts().Reads(schemaread.FormBinary),
		"base64": workerViewReader.Counts().Reads(schemaread.FormBase64),
		"json":   workerViewReader.Counts().Reads(schemaread.FormJSON),
	}
	refusedBefore := refusedTotal(workerViewReader.Counts().Snapshot())

	wantLegacy, err := ParseView("rowan", legacy)
	require.NoError(t, err)
	require.Equal(t, want, wantLegacy)

	for _, tc := range []struct {
		name string
		in   string
	}{
		{"binary", string(binary)},
		{"base64", string(b64)},
		{"json", string(jsonForm)},
		{"legacy", legacy},
	} {
		got, err := ParseView("rowan", tc.in)
		require.NoError(t, err, tc.name)
		assert.Equal(t, want, got, tc.name)
	}

	assert.GreaterOrEqual(t, workerViewReader.Counts().Reads(schemaread.FormBinary), readsBefore["binary"]+1, "binary read counted")
	assert.GreaterOrEqual(t, workerViewReader.Counts().Reads(schemaread.FormBase64), readsBefore["base64"]+1, "base64 read counted")
	assert.GreaterOrEqual(t, workerViewReader.Counts().Reads(schemaread.FormJSON), readsBefore["json"]+3, "json reads counted: legacy and the json form, twice")

	// the refusal reads: truncated, byte-flipped and clamped values of each
	// form are refused by name (<RECORD> REFUSED: <reason>; run: <help>),
	// never read as absent, empty or zero.
	refuse := func(name, in string, wantReason string) {
		t.Helper()
		_, err := ParseView("rowan", in)
		require.Error(t, err, name)
		require.Contains(t, err.Error(), "WorkerView REFUSED", name)
		require.Contains(t, err.Error(), "run:", name)
		if wantReason != "" {
			require.Contains(t, err.Error(), wantReason, name)
		}
	}
	refuse("binary truncated", string(binary[:len(binary)-1]), "")
	refuse("binary flipped form byte", string(flipByte(binary, 0)), "")
	refuse("base64 truncated", string(b64[:len(b64)-2]), "")
	refuse("base64 flipped", string(flipByte(b64, 0)), "")
	refuse("json truncated", string(jsonForm[:len(jsonForm)-8]), "")
	refuse("clamped as", `{"view":"worker","as":"`+strings.Repeat("a", 65)+`","kind":"friend"}`, schemaread.ReasonClamped)
	refuse("clamped kind", `{"view":"worker","as":"rowan","kind":"`+strings.Repeat("k", 33)+`"}`, schemaread.ReasonClamped)
	refuse("absent view", `{"as":"rowan","kind":"friend"}`, schemaread.ReasonAbsentRequired)
	assert.True(t, refusedTotal(workerViewReader.Counts().Snapshot()) > refusedBefore, "refusals counted")
}

// workerViewWire builds the worker view row from want and returns its binary
// wire image, the one the generated writer writes.
func workerViewWire(t *testing.T, want []HeldCard) []byte {
	t.Helper()
	var b answerwire.WorkerViewBuilder
	require.True(t, b.Init())
	defer b.Shutdown()
	root := b.GetRoot()
	viewKey(root, "worker", "rowan", "friend")
	for _, c := range want {
		row := answerwire.WorkerViewCardsAdd(&b.Main, &root.Cards)
		setStr(row.Id[:], &row.IdLength, c.Card)
		setStr(row.St[:], &row.StLength, c.Col)
		setStr(row.Brief[:], &row.BriefLength, "/home/inbox/"+c.Job+"/BRIEF.md")
	}
	require.True(t, b.Lock())
	row := b.AsConst()
	require.NotNil(t, row)
	n := answerwire.WorkerViewMeasure(row)
	require.True(t, n > 0)
	buf := make([]byte, n)
	require.Equal(t, n, answerwire.WorkerViewSave(row, buf))
	return buf
}

// workerViewJSON builds the worker view row from want and returns its schema
// JSON text image.
func workerViewJSON(t *testing.T, want []HeldCard) []byte {
	t.Helper()
	var b answerwire.WorkerViewBuilder
	require.True(t, b.Init())
	defer b.Shutdown()
	root := b.GetRoot()
	viewKey(root, "worker", "rowan", "friend")
	for _, c := range want {
		row := answerwire.WorkerViewCardsAdd(&b.Main, &root.Cards)
		setStr(row.Id[:], &row.IdLength, c.Card)
		setStr(row.St[:], &row.StLength, c.Col)
		setStr(row.Brief[:], &row.BriefLength, "/home/inbox/"+c.Job+"/BRIEF.md")
	}
	require.True(t, b.Lock())
	row := b.AsConst()
	require.NotNil(t, row)
	n := answerwire.WorkerViewToJsonMeasure(row)
	require.True(t, n > 0)
	buf := make([]byte, n)
	require.Equal(t, n, answerwire.WorkerViewToJson(row, buf))
	return buf
}

func viewKey(root *answerwire.WorkerView, view, as, kind string) {
	setStr(root.View[:], &root.ViewLength, view)
	setStr(root.AsName[:], &root.AsNameLength, as)
	setStr(root.Kind[:], &root.KindLength, kind)
}

func setStr(dst []byte, length *int32, s string) {
	n := copy(dst, s)
	*length = int32(n)
}

func flipByte(b []byte, at int) []byte {
	out := append([]byte(nil), b...)
	out[at] ^= 0x01
	return out
}

func refusedTotal(s map[string]uint64) uint64 {
	var n uint64
	for k, v := range s {
		if strings.HasPrefix(k, "refused_") {
			n += v
		}
	}
	return n
}
