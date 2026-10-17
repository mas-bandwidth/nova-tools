package member

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemberTakeAnswerDualRead pins the take answer's one read site (Member.take,
// pkg/member/member.go): the TakeOut table's wire, the padded standard base64 of it,
// the schema JSON text and the legacy JSON the take verb writes today all read to one
// equal value; a malformed, damaged or clamped form is refused by name and never read as
// absent or zero; reads are counted per form (owner's rules S1, S2, S4; schemas/answer).
func TestMemberTakeAnswerDualRead(t *testing.T) {
	t.Parallel()

	one := func(kind, id string) Packet {
		return Packet{Kind: kind, Card: id, As: "m", Primary: "p", Stream: "v15", Attempt: 3, Gen: 1, Epoch: 7, Branch: "b", Worker: "w", Notes: []string{}}
	}
	v := takeOut{Packets: []Packet{one("work", "c1"), one("read", "c2")}}

	wire, err := takeReads.Write(&v, schemaread.FormBinary)
	require.NoError(t, err)
	b64, err := takeReads.Write(&v, schemaread.FormBase64)
	require.NoError(t, err)
	js, err := takeReads.Write(&v, schemaread.FormJSON)
	require.NoError(t, err)
	legacy, err := json.Marshal(v)
	require.NoError(t, err)

	cases := []struct {
		name string
		in   []byte
	}{
		{"binary", wire},
		{"base64", b64},
		{"json", js},
		{"legacy", legacy},
	}
	for _, tc := range cases {
		t.Run("read_"+tc.name, func(t *testing.T) {
			before := takeReads.Counts().Snapshot()
			got, res, err := takeReads.Read(tc.in)
			require.NoError(t, err)
			assert.Equal(t, v, got)
			after := takeReads.Counts().Snapshot()
			assert.Equal(t, uint64(1), after[tc.name]-before[tc.name], "read counted under form %s", tc.name)
			assert.NotZero(t, res.Form)
		})
	}

	require.Equal(t, base64.StdEncoding.EncodeToString(wire), string(b64))

	t.Run("refused", func(t *testing.T) {
		bad := []struct {
			name string
			in   []byte
			what string
		}{
			{"truncated_wire", wire[:len(wire)-3], "REFUSED"},
			{"flipped_wire", flipByte(wire, 4), "REFUSED"},
			{"flipped_base64", flipByte(b64, 5), "REFUSED"},
			{"truncated_base64", b64[:len(b64)-4], "REFUSED"},
			{"empty", nil, "REFUSED"},
			{"nonsense", []byte("not a take"), "REFUSED"},
		}
		for _, tc := range bad {
			t.Run(tc.name, func(t *testing.T) {
				before := takeReads.Counts().Snapshot()
				got, _, err := takeReads.Read(tc.in)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "TAKE_OUT")
				assert.Contains(t, err.Error(), strings.ToUpper(tc.what))
				assert.Equal(t, takeOut{}, got, "a refused read is never read as a value")
				after := takeReads.Counts().Snapshot()
				refusedBefore := sumRefused(before)
				refusedAfter := sumRefused(after)
				assert.Greater(t, refusedAfter, refusedBefore, "refusal counted")
			})
		}
	})

	t.Run("clamped", func(t *testing.T) {
		before := takeReads.Counts().Snapshot()
		got, _, err := takeReads.Read([]byte("{\n  \"packets\": [\n    {\"kind\":\"work\",\"attempt\":999999}\n  ]\n}"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "clamped")
		assert.Equal(t, takeOut{}, got)
		after := takeReads.Counts().Snapshot()
		assert.Greater(t, sumRefused(after), sumRefused(before))
	})
}

func flipByte(in []byte, at int) []byte {
	out := append([]byte(nil), in...)
	out[at] ^= 0x01
	return out
}

func sumRefused(m map[string]uint64) uint64 {
	var n uint64
	for k, v := range m {
		if strings.HasPrefix(k, "refused_") {
			n += v
		}
	}
	return n
}
