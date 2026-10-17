package hook_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/hook/seatstamp"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/hookgate"
	"github.com/mas-bandwidth/nova-tools/pkg/hookwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// typedStamp is a Stamp table instance as the schema writes one.
func typedStamp(epoch, atMS int64, by string) hookwire.Stamp {
	var v hookwire.Stamp
	hookwire.StampReset(&v)
	v.Epoch, v.EpochPresent = epoch, true
	v.AtMs, v.AtMsPresent = atMS, true
	v.ByLength = int32(copy(v.By[:len(v.By)-1], by))
	return v
}

func wholeBytes(measure func() int64, save func([]byte) int64) []byte {
	n := measure()
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if save(buf) != n {
		return nil
	}
	return buf
}

// stampWire is the binary wire of a typed stamp, stampText its JSON text.
func stampWire(v hookwire.Stamp) []byte {
	return wholeBytes(func() int64 { return hookwire.StampMeasure(&v) }, func(b []byte) int64 { return hookwire.StampSave(&v, b) })
}

func stampText(v hookwire.Stamp) []byte {
	return wholeBytes(func() int64 { return hookwire.StampToJsonMeasure(&v) }, func(b []byte) int64 { return hookwire.StampToJson(&v, b) })
}

// legacyStamp is the stamp as the record's scripts write it (hook/redis.go,
// stampLua): compact JSON, the time in milliseconds.
func legacyStamp(epoch, atMS int64, by string) string {
	return fmt.Sprintf(`{"epoch":%d,"at":%d,"by":%q}`, epoch, atMS, by)
}

// forms is one stamp in each form a store may hold it in.
func forms(epoch, atMS int64, by string) map[schemaread.Form]string {
	v := typedStamp(epoch, atMS, by)
	return map[schemaread.Form]string{
		schemaread.FormLegacy: legacyStamp(epoch, atMS, by),
		schemaread.FormBinary: string(stampWire(v)),
		schemaread.FormBase64: base64.StdEncoding.EncodeToString(stampWire(v)),
		schemaread.FormJSON:   string(stampText(v)),
	}
}

var allForms = []schemaread.Form{schemaread.FormLegacy, schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

// refusedWith asserts err is the house-style refusal of a STAMP with reason.
func refusedWith(t *testing.T, err error, reason string) {
	t.Helper()
	require.Error(t, err)
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), "a refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), "STAMP REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: nova-hook status -h"), err.Error())
	if reason != "" {
		got := r.Reason
		if r.Field != "" {
			got += " (" + r.Field + ")"
		}
		assert.Equal(t, reason, got)
	}
}

// reads is a coordinator's two pushes read as live (lease) and proven (proof).
func coordinatorReads(lease, proof string) []hook.PushRead {
	out := make([]hook.PushRead, len(hook.Pushes))
	for i := range out {
		out[i] = hook.PushRead{Live: lease, Proof: proof, LiveLeft: hook.LeaseTTL, ProofLeft: hook.ProofTTL}
	}
	return out
}

func coordinatorRecord(epoch int64) map[string]string {
	return map[string]string{"role": string(hook.Coordinator), "state": "hooked", "epoch": fmt.Sprint(epoch), "armed": "1791000000000"}
}

// TestSeatHookTypedStamp pins the seat's hook reader: a lease or a proof is
// read alike as the typed wire (binary, padded standard base64, schema JSON
// text) and as the legacy JSON the record's scripts write, each read counted
// by its form, and a malformed one is refused by name and counts for nothing.
func TestSeatHookTypedStamp(t *testing.T) {
	t.Parallel()

	t.Run("every form reads the same stamp and is counted by its form", func(t *testing.T) {
		t.Parallel()
		r := seatstamp.New()
		const reads = 3
		want := hook.Stamp{Epoch: 7, AtMS: 1792000000000, By: "nova-hook studio pid 4242"}
		for range reads {
			for _, form := range allForms {
				got, err := r.ReadStamp(forms(7, 1792000000000, want.By)[form])
				require.NoError(t, err, form.String())
				assert.Equal(t, want, got, "the %s form reads the stamp", form)
			}
		}
		for _, form := range allForms {
			assert.Equal(t, uint64(reads), r.Counts().Reads(form), "the counter shows the %s reads", form)
		}
		assert.Equal(t, uint64(0), r.Counts().Refused(schemaread.ReasonMalformed))
	})

	t.Run("the schema's own corpus reads", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join("..", "schemas", "hook", "fuzz")
		wire, err := os.ReadFile(filepath.Join(dir, "stamp-typical.bin"))
		require.NoError(t, err)
		text, err := os.ReadFile(filepath.Join(dir, "stamp-typical.json"))
		require.NoError(t, err)
		r := seatstamp.New()
		want := hook.Stamp{Epoch: 7, AtMS: 1792000000000, By: "nova-friend 4242"}
		for _, in := range []string{string(wire), base64.StdEncoding.EncodeToString(wire), string(text)} {
			got, err := r.ReadStamp(in)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormJSON))
	})

	t.Run("a holder at the bound reads and one over it is refused", func(t *testing.T) {
		t.Parallel()
		r := seatstamp.New()
		at := strings.Repeat("h", 128)
		for form, in := range forms(1, 1, at) {
			got, err := r.ReadStamp(in)
			require.NoError(t, err, form.String())
			assert.Equal(t, at, got.By)
		}
		_, err := r.ReadStamp(legacyStamp(1, 1, at+"h"))
		refusedWith(t, err, "clamped")
	})

	t.Run("a malformed stamp is refused by name", func(t *testing.T) {
		t.Parallel()
		good := typedStamp(7, 1792000000000, "nova-friend 4242")
		goodWire := stampWire(good)
		goodText := string(stampText(good))
		edit := func(f func(*hookwire.Stamp)) string {
			v := good
			f(&v)
			return string(stampWire(v))
		}
		for _, tc := range []struct {
			name   string
			in     string
			reason string
		}{
			// the parse that failed closed on 2026-10-11 and 12: the old
			// reader read a value it could not parse as no stamp, silently
			{"a legacy stamp with an RFC 3339 time", `{"epoch":7,"at":"2026-10-11T12:00:00Z","by":"x"}`, "legacy_shape"},
			{"a legacy stamp with a key the scripts never write", `{"epoch":7,"at":1792000000000,"by":"x","ttl":1}`, "legacy_shape"},
			{"a legacy stamp spaced as the scripts never write it", `{"epoch":7, "at":1792000000000,"by":"x"}`, "legacy_shape"},
			{"a legacy stamp with an epoch of zero", legacyStamp(0, 1792000000000, "x"), "absent_required (epoch)"},
			{"a legacy stamp with no time", legacyStamp(7, 0, "x"), "absent_required (at_ms)"},
			{"a legacy stamp with no holder", legacyStamp(7, 1792000000000, ""), "absent_required (by)"},
			{"a typed stamp with epoch absent", edit(func(v *hookwire.Stamp) { v.Epoch, v.EpochPresent = 0, false }), "absent_required (epoch)"},
			{"a typed stamp with epoch zero", edit(func(v *hookwire.Stamp) { v.Epoch = 0 }), "absent_required (epoch)"},
			{"a typed stamp with a time before the epoch", edit(func(v *hookwire.Stamp) { v.AtMs = -5 }), "absent_required (at_ms)"},
			{"a typed stamp with no holder", edit(func(v *hookwire.Stamp) { v.ByLength = 0 }), "absent_required (by)"},
			{"a NUL in the holder", edit(func(v *hookwire.Stamp) { v.By[2] = 0 }), "malformed"},
			{"the JSON text with the time left out", strings.Replace(goodText, "  \"at_ms\": 1792000000000,\n", "", 1), "absent_required (at_ms)"},
			{"base64 that is not base64", "!!not base64!!", "unknown_form"},
			{"base64 with a missing pad", strings.TrimRight(base64.StdEncoding.EncodeToString(goodWire), "="), "malformed"},
			{"text that is no stamp", "hello stamp", "malformed"},
			{"a lone brace", "{", ""},
			{"a wire of an unknown form byte", "\x7f" + string(goodWire[1:]), "unknown_form"},
			{"a wire cut short", string(goodWire[:len(goodWire)-3]), ""},
			{"the wire's last bytes damaged", string(goodWire[:len(goodWire)-1]) + "\xff", ""},
		} {
			r := seatstamp.New()
			got, err := r.ReadStamp(tc.in)
			refusedWith(t, err, tc.reason)
			assert.Equal(t, hook.Stamp{}, got, "%s: a refused read returns no stamp", tc.name)
		}
		// a refusal is counted by its reason and a form's read count moves
		// on the form it arrived in even when refused
		r := seatstamp.New()
		_, err := r.ReadStamp(`{"epoch":7,"at":"x","by":"x"}`)
		refusedWith(t, err, "legacy_shape")
		assert.Equal(t, uint64(1), r.Counts().Refused(seatstamp.ReasonLegacyShape))
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy))
		assert.Equal(t, uint64(0), r.Counts().Reads(schemaread.FormJSON))
	})

	t.Run("a newer writer's stamp reads and counts the field it does not know", func(t *testing.T) {
		t.Parallel()
		wire, err := os.ReadFile(filepath.Join("..", "pkg", "hookwire", "testdata", "writers", "newer-stamp.bin"))
		require.NoError(t, err)
		r := seatstamp.New()
		got, err := r.ReadStamp(string(wire))
		require.NoError(t, err)
		assert.Equal(t, hook.Stamp{Epoch: 7, AtMS: 1792000000000, By: "nova-friend 4242"}, got)
		assert.Equal(t, uint64(1), r.Counts().Unknown())
	})

	t.Run("JudgeWith counts a typed lease and proof as it counts a legacy one", func(t *testing.T) {
		t.Parallel()
		const epoch = 5
		for _, leaseForm := range allForms {
			for _, proofForm := range allForms {
				r := seatstamp.New()
				lease, proof := forms(epoch, 1792000000000, "lease")[leaseForm], forms(epoch, 1792000000001, "proof")[proofForm]
				st := hook.JudgeWith(r, "ada", coordinatorRecord(epoch), coordinatorReads(lease, proof))
				assert.Equal(t, hook.StateProven, st.State, "a %s lease and a %s proof", leaseForm, proofForm)
				for _, p := range st.Pushes {
					assert.Equal(t, "lease", p.By, "the lease's holder is read from the %s form", leaseForm)
					assert.Empty(t, p.Refused)
				}
			}
		}
		// another epoch counts for nothing in any form
		for _, form := range allForms {
			st := hook.JudgeWith(seatstamp.New(), "ada", coordinatorRecord(epoch), coordinatorReads(forms(epoch+1, 1, "x")[form], forms(epoch+1, 1, "x")[form]))
			assert.Equal(t, hook.StateExpired, st.State, "a %s stamp of another epoch", form)
		}
	})

	t.Run("JudgeWith counts a refused stamp for nothing and says why", func(t *testing.T) {
		t.Parallel()
		const epoch = 5
		r := seatstamp.New()
		rfc := `{"epoch":5,"at":"2026-10-11T12:00:00Z","by":"x"}`
		st := hook.JudgeWith(r, "ada", coordinatorRecord(epoch), coordinatorReads(legacyStamp(epoch, 1, "x"), rfc))
		assert.Equal(t, hook.StateLive, st.State, "a lease with a refused proof is live and not proven")
		for _, p := range st.Pushes {
			assert.Equal(t, hook.PushArmed, p.State)
			assert.Contains(t, p.Refused, "STAMP REFUSED: legacy_shape")
		}
		assert.Contains(t, st.Words(), "[STAMP REFUSED: legacy_shape", "the status line names the refusal")
		st = hook.JudgeWith(r, "ada", coordinatorRecord(epoch), coordinatorReads(rfc, rfc))
		assert.Equal(t, hook.StateExpired, st.State, "a refused lease holds nothing")
		assert.Equal(t, uint64(4), r.Counts().Refused(seatstamp.ReasonLegacyShape), "each refused read is counted: two proofs, then two leases")
	})

	t.Run("the default reader keeps reading what the scripts write", func(t *testing.T) {
		t.Parallel()
		const epoch = 5
		legacy := legacyStamp(epoch, 1, "x")
		st := hook.JudgeWith(nil, "ada", coordinatorRecord(epoch), coordinatorReads(legacy, legacy))
		assert.Equal(t, hook.StateProven, st.State)
		typed := forms(epoch, 1, "x")[schemaread.FormBase64]
		st = hook.JudgeWith(nil, "ada", coordinatorRecord(epoch), coordinatorReads(typed, typed))
		assert.Equal(t, hook.StateExpired, st.State, "a store handed no typed reader reads a typed stamp as none")
		assert.NotEmpty(t, st.Pushes[0].Refused, "and says it was not a stamp")
	})

	t.Run("a store reads a typed lease it holds", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		rg := newRig(t)
		e, _, err := rg.s.Arm(ctx, "ada", hook.Coordinator, "test", false)
		require.NoError(t, err)
		for _, p := range []hook.Push{hook.Bus, hook.Sprint} {
			require.NoError(t, rg.s.Renew(ctx, "ada", hook.Coordinator, p, e, "legacy holder"))
		}
		assert.Equal(t, hook.StateLive, rg.state(t, "ada").State, "the scripts' own stamps")

		// a typed writer's lease replaces the bus lease
		typed := forms(e, 1792000000000, "typed holder")[schemaread.FormBase64]
		key := hook.LiveKey("ada", hook.Bus)
		require.NoError(t, rg.mr.Set(key, typed))
		rg.mr.SetTTL(key, hook.LeaseTTL)
		before := rg.state(t, "ada")
		assert.Equal(t, hook.StatePartial, before.State, "a store with no typed reader reads it as none")
		assert.Contains(t, before.Pushes[0].Refused, "STAMP REFUSED")

		r := seatstamp.New()
		s := rg.s
		s.Stamps = r
		sts, _, err := s.Read(ctx, "ada")
		require.NoError(t, err)
		assert.Equal(t, hook.StateLive, sts[0].State, "a store with the seat reader reads both forms")
		assert.Equal(t, "typed holder", sts[0].Pushes[0].By)
		assert.Equal(t, "legacy holder", sts[0].Pushes[1].By)
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy))
	})

	t.Run("the production store reads a typed lease it holds", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		rg := newRig(t)
		e, _, err := rg.s.Arm(ctx, "ada", hook.Coordinator, "test", false)
		require.NoError(t, err)
		for _, p := range []hook.Push{hook.Bus, hook.Sprint} {
			require.NoError(t, rg.s.Renew(ctx, "ada", hook.Coordinator, p, e, "legacy holder"))
		}
		// a typed writer's lease replaces the bus lease
		typed := forms(e, 1792000000000, "typed holder")[schemaread.FormBase64]
		key := hook.LiveKey("ada", hook.Bus)
		require.NoError(t, rg.Client.Set(ctx, key, typed, redis.KeepTTL).Err())
		assert.Equal(t, hook.StatePartial, rg.state(t, "ada").State, "a store built by hand reads it as none")

		// the store every tool opens: hookgate.Store over the bus store
		hs, err := hookgate.Store(bus.Redis{C: rg.Client})
		require.NoError(t, err)
		before := hookgate.StampCounts().Reads(schemaread.FormBase64)
		sts, _, err := hs.Read(ctx, "ada")
		require.NoError(t, err)
		assert.Equal(t, hook.StateLive, sts[0].State, "the production store reads the typed lease")
		assert.Equal(t, "typed holder", sts[0].Pushes[0].By)
		assert.Equal(t, "legacy holder", sts[0].Pushes[1].By)
		assert.Equal(t, before+1, hookgate.StampCounts().Reads(schemaread.FormBase64), "and counts it by its form on the one shared reader")
		hs2, err := hookgate.Store(bus.Redis{C: rg.Client})
		require.NoError(t, err)
		assert.Same(t, hs.Stamps, hs2.Stamps, "every store the factory makes shares the one reader, so the counters accumulate")
	})
}
