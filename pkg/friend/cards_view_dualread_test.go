package friend

import (
	"encoding/base64"
	"strings"
	"testing"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	RegisterHoldersWire(
		func(wire []byte) error {
			_, err := answerwire.HoldersViewLoadMeasureReason(wire)
			return err
		},
		holdersLoadWireTest,
		holdersLoadJSONTest,
		nil, nil,
	)
}

func holdersLoadWireTest(v *Holders, in []byte) schemaread.Report {
	n := answerwire.HoldersViewLoadMeasure(in)
	if n < 0 {
		return schemaread.Report{Malformed: true}
	}
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	region := raw[off : off+uintptr(n)]
	var rep answerwire.TableReport
	root := answerwire.HoldersViewLoad(region, in, &rep)
	return holdersReduceTest(v, root, &rep)
}

func holdersLoadJSONTest(v *Holders, in []byte) schemaread.Report {
	var b answerwire.HoldersViewBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	defer b.Shutdown()
	var rep answerwire.TableReport
	if !answerwire.HoldersViewFromJson(&b, in, &rep) || !b.Lock() {
		return schemaread.Report{Malformed: true, Reason: rep.Reason}
	}
	return holdersReduceTest(v, b.AsConst(), &rep)
}

func holdersReduceTest(v *Holders, root *answerwire.HoldersView, rep *answerwire.TableReport) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || rep.Duplicate > 0 || root == nil}
	if rep.Verdict == answerwire.TableOpenRefused || rep.Verdict == answerwire.TableOpenDamaged {
		if err := rep.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	if root == nil || out.Reason != "" || out.Malformed || out.KindMismatch > 0 || out.Clamped > 0 {
		return out
	}
	if string(root.View[:root.ViewLength]) != "cards" || root.Schema != 1 {
		return schemaread.Report{Reason: schemaread.ReasonKindMismatch}
	}
	m := map[string]string{}
	seen := map[string]bool{}
	for i := int32(0); i < root.Cards.Count; i++ {
		c := root.Cards.At(i)
		if c.IdLength <= 0 {
			return schemaread.Report{Malformed: true}
		}
		id := string(c.Id[:c.IdLength])
		if seen[id] {
			return schemaread.Report{Malformed: true}
		}
		seen[id] = true
		if c.HolderLength > 0 {
			m[id] = string(c.Holder[:c.HolderLength])
		}
	}
	*v = m
	return out
}

const holdersGoodJSON = `{"view":"cards","schema":1,"cards":[{"id":"a11","holder":"friend.rowan"},{"id":"a12"}]}`

// holdersWire builds the binary wire of a HoldersView from its JSON text.
func holdersWire(t *testing.T, js string) []byte {
	t.Helper()
	var b answerwire.HoldersViewBuilder
	require.True(t, b.Init())
	defer b.Shutdown()
	var rep answerwire.TableReport
	require.True(t, answerwire.HoldersViewFromJson(&b, []byte(js), &rep))
	require.True(t, b.Lock())
	v := b.AsConst()
	n := answerwire.HoldersViewMeasure(v)
	require.Positive(t, n)
	buf := make([]byte, n)
	require.Equal(t, n, answerwire.HoldersViewSave(v, buf))
	return buf
}

func holdersJSON(t *testing.T, wire []byte) string {
	t.Helper()
	var b answerwire.HoldersViewBuilder
	require.True(t, b.Init())
	defer b.Shutdown()
	var rep answerwire.TableReport
	require.True(t, answerwire.HoldersViewLoadBuilder(&b, wire, &rep))
	require.True(t, b.Lock())
	v := b.AsConst()
	n := answerwire.HoldersViewToJsonMeasure(v)
	require.Positive(t, n)
	buf := make([]byte, n)
	require.Equal(t, n, answerwire.HoldersViewToJson(v, buf))
	return string(buf)
}

func TestFriendCardHoldersDualRead(t *testing.T) {
	t.Parallel()
	wire := holdersWire(t, holdersGoodJSON)
	base64Form := base64.StdEncoding.EncodeToString(wire)
	jsonForm := holdersJSON(t, wire)
	legacyForm := holdersGoodJSON

	want := Holders{"a11": "friend.rowan"}

	t.Run("every form reads equal", func(t *testing.T) {
		t.Parallel()
		r := NewHoldersReader()
		for _, c := range []struct{ name, in string }{
			{"binary", string(wire)},
			{"base64", base64Form},
			{"json", jsonForm},
			{"legacy", legacyForm},
		} {
			c := c
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				got, form, err := r.Read([]byte(c.in))
				require.NoError(t, err)
				assert.Equal(t, want, got)
				assert.NotEqual(t, schemaread.FormNone, form)
			})
		}
	})

	t.Run("a malformed value of each form is refused by name and never read as empty", func(t *testing.T) {
		t.Parallel()
		r := NewHoldersReader()
		bad := []struct{ name, in string }{
			{"binary truncated", string(wire[:len(wire)-3])},
			{"binary flipped", string(flipByte(wire))},
			{"base64 truncated", base64Form[:len(base64Form)-4]},
			{"json truncated", jsonForm[:len(jsonForm)-2]},
			{"legacy truncated", legacyForm[:len(legacyForm)-2]},
			{"legacy flipped", strings.Replace(legacyForm, "\"view\":\"cards\"", "\"view\":\"carx\"", 1)},
		}
		for _, c := range bad {
			c := c
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				got, _, err := r.Read([]byte(c.in))
				require.Error(t, err)
				var refusal *schemaread.Refusal
				require.ErrorAs(t, err, &refusal)
				assert.Equal(t, "CARD_HOLDERS", refusal.Name)
				assert.Empty(t, got)
			})
		}
	})

	t.Run("a clamped value is refused", func(t *testing.T) {
		t.Parallel()
		r := NewHoldersReader()
		big := strings.Repeat("h", 65)
		clamped := `{"view":"cards","schema":1,"cards":[{"id":"a","holder":"` + big + `"}]}`
		_, _, err := r.Read([]byte(clamped))
		require.Error(t, err)
		var refusal *schemaread.Refusal
		require.ErrorAs(t, err, &refusal)
		assert.Equal(t, "CARD_HOLDERS", refusal.Name)
	})

	t.Run("reads are counted per record and per form", func(t *testing.T) {
		t.Parallel()
		r := NewHoldersReader()
		for _, c := range []string{string(wire), base64Form, jsonForm, legacyForm} {
			_, _, err := r.Read([]byte(c))
			require.NoError(t, err)
		}
		counts := r.Counts()
		assert.Equal(t, uint64(1), counts.Reads(schemaread.FormBinary))
		assert.Equal(t, uint64(1), counts.Reads(schemaread.FormBase64))
		assert.Equal(t, uint64(2), counts.Reads(schemaread.FormJSON))
		assert.Zero(t, counts.Reads(schemaread.FormLegacy))
	})
}

func flipByte(b []byte) []byte {
	out := append([]byte(nil), b...)
	out[len(out)/2] ^= 0xFF
	return out
}
