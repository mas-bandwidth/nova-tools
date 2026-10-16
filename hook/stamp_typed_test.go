package hook

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestHookStampTyped pins the typed stamp (schemas/hook/stamp.schema): the
// stamp carries an int64 millisecond time in every form, a malformed stamp is
// refused by name and rides the push's status instead of failing closed in
// silence, the legacy text (and its RFC 3339 predecessor) still reads during
// the window, and the reads are counted per form.
func TestHookStampTyped(t *testing.T) {
	t.Parallel()

	const at = int64(1792000000000)

	t.Run("every form reads one stamp with an int64 millisecond time", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(stampCodec())
		legacy := `{"epoch":7,"at":1792000000000,"by":"nova-friend 4242"}`
		want := stamp{Epoch: 7, At: at, By: "nova-friend 4242"}
		got, err := readStamp(r, legacy)
		require.NoError(t, err)
		assert.Equal(t, want, got)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			raw, err := StampBytes(7, at, "nova-friend 4242", form)
			require.NoError(t, err, form.String())
			got, err := readStamp(r, string(raw))
			require.NoError(t, err, form.String())
			assert.Equal(t, want, got, "the %s form reads as the legacy text does", form)
		}
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy))
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormJSON))
	})

	t.Run("a legacy stamp with an RFC 3339 time still reads and is counted", func(t *testing.T) {
		t.Parallel()
		before := StampReads()["legacy_rfc3339"]
		got, err := readStamp(stampReader, `{"epoch":3,"at":"2026-10-11T12:00:00Z","by":"x"}`)
		require.NoError(t, err)
		assert.Equal(t, stamp{Epoch: 3, At: 1791720000000, By: "x"}, got)
		assert.Greater(t, StampReads()["legacy_rfc3339"], before)
	})

	t.Run("a malformed stamp is refused by name", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(stampCodec())
		good, err := StampBytes(7, at, "x", schemaread.FormBinary)
		require.NoError(t, err)
		for name, tc := range map[string]struct{ in, reason string }{
			"no stamp at all":               {"hello stamp", "malformed"},
			"a legacy time that is no time": {`{"epoch":7,"at":"yesterday","by":"x"}`, ReasonLegacyShape},
			"a legacy key nobody writes":    {`{"epoch":7,"at":1,"by":"x","ttl":5}`, ReasonLegacyShape},
			"a legacy stamp missing a key":  {`{"epoch":7,"at":1}`, ReasonLegacyShape},
			"a legacy fractional time":      {`{"epoch":7,"at":1.5,"by":"x"}`, ReasonLegacyShape},
			"trailing text":                 {`{"epoch":7,"at":1,"by":"x"} x`, ReasonLegacyShape},
			"a zero epoch":                  {`{"epoch":0,"at":1,"by":"x"}`, "absent_required (epoch)"},
			"a non-positive time":           {`{"epoch":7,"at":-5,"by":"x"}`, "absent_required (at_ms)"},
			"an empty holder":               {`{"epoch":7,"at":1,"by":""}`, "absent_required (by)"},
			"a holder over 128 bytes":       {`{"epoch":7,"at":1,"by":"` + strings.Repeat("h", 129) + `"}`, "clamped"},
			"a truncated wire":              {string(good[:len(good)-3]), ""},
			"damaged base64":                {base64.StdEncoding.EncodeToString(good)[:11] + "*", "malformed"},
		} {
			_, err := readStamp(r, tc.in)
			require.Error(t, err, name)
			assert.Contains(t, err.Error(), "STAMP REFUSED: "+tc.reason, name)
			assert.Contains(t, err.Error(), "; run: "+StampHelp, name)
		}
	})

	t.Run("a refused stamp rides the push status and the push reads down", func(t *testing.T) {
		t.Parallel()
		rec := hooked(Comms, 4)
		rs := reads(4, []Push{Bus}, nil)
		rs[0].Live = "hello stamp"
		st := Judge("ada", rec, rs)
		require.Len(t, st.Pushes, 1)
		assert.Equal(t, PushDown, st.Pushes[0].State)
		assert.Contains(t, st.Pushes[0].Refused, "STAMP REFUSED: malformed")
		assert.Contains(t, st.pushWords(), "bus=down (STAMP REFUSED: malformed")
		assert.NotNil(t, st.Gate(Want{Name: "ada", Need: []Push{Bus}, Role: Comms}))

		// a proof that is no stamp leaves the push armed, named
		rs = reads(4, []Push{Bus}, nil)
		rs[0].Proof, rs[0].ProofLeft = `{"epoch":4,"at":1}`, ProofTTL
		st = Judge("ada", rec, rs)
		assert.Equal(t, PushArmed, st.Pushes[0].State)
		assert.Contains(t, st.Pushes[0].Refused, ReasonLegacyShape)
	})

	t.Run("Judge reads every form and counts it", func(t *testing.T) {
		t.Parallel()
		rec := hooked(Comms, 4)
		before := StampReads()
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			lease, err := StampBytes(4, at, "holder", form)
			require.NoError(t, err)
			proof, err := StampBytes(4, at+1, "prover", form)
			require.NoError(t, err)
			rs := reads(4, nil, nil)
			rs[0] = PushRead{Live: string(lease), LiveLeft: LeaseTTL, Proof: string(proof), ProofLeft: ProofTTL}
			st := Judge("ada", rec, rs)
			require.Len(t, st.Pushes, 1, form.String())
			assert.Equal(t, PushProven, st.Pushes[0].State, form.String())
			assert.Equal(t, "holder", st.Pushes[0].By, form.String())
			assert.Empty(t, st.Pushes[0].Refused)
		}
		after := StampReads()
		for _, k := range []string{"binary", "base64", "json"} {
			assert.GreaterOrEqual(t, after[k], before[k]+2, "the %s reads are counted", k)
		}
	})

	t.Run("the writer refuses what is no stamp", func(t *testing.T) {
		t.Parallel()
		for name, f := range map[string]func() ([]byte, error){
			"a zero epoch":    func() ([]byte, error) { return StampBytes(0, at, "x", schemaread.FormJSON) },
			"a zero time":     func() ([]byte, error) { return StampBytes(1, 0, "x", schemaread.FormBinary) },
			"an empty holder": func() ([]byte, error) { return StampBytes(1, at, "", schemaread.FormBase64) },
			"a long holder":   func() ([]byte, error) { return StampBytes(1, at, strings.Repeat("h", 129), schemaread.FormBinary) },
		} {
			_, err := f()
			require.Error(t, err, name)
			assert.Contains(t, err.Error(), "STAMP REFUSED", name)
		}
	})
}
