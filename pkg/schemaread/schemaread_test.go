package schemaread_test

// The test-local table below is a small variable table in the shape the
// generated Go has: a form byte, type-length-value fields, a trailer; a JSON
// text form; and a legacy text form. The helper imports no generated package,
// so the test builds the three functions a generated table gives it.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type msg struct {
	ID    uint8
	Level uint8 // 0..10
	Kind  uint8 // enum ordinals 0..2
	Name  string
}

const (
	trailer  = 0xA5
	maxLevel = 10
	nKinds   = 3
	maxName  = 8
)

func tlv(id byte, b ...byte) []byte { return append([]byte{id, byte(len(b))}, b...) }

func wireOf(parts ...[]byte) []byte {
	w := []byte{1}
	for _, p := range parts {
		w = append(w, p...)
	}
	return append(w, trailer)
}

func good() []byte {
	return wireOf(tlv(1, 7), tlv(2, 3), tlv(3, 1), tlv(4, []byte("bob")...))
}

func loadWire(v *msg, in []byte) schemaread.Report {
	var r schemaread.Report
	body := in[1 : len(in)-1]
	for len(body) > 0 {
		if len(body) < 2 || int(body[1]) > len(body)-2 {
			r.Malformed = true
			return r
		}
		id, b := body[0], body[2:2+int(body[1])]
		body = body[2+int(body[1]):]
		switch {
		case id >= 1 && id <= 3 && len(b) != 1:
			r.KindMismatch++
		case id == 1:
			v.ID = b[0]
		case id == 2:
			v.Level = b[0]
		case id == 3:
			v.Kind = b[0]
		case id == 4:
			v.Name = string(b)
		default:
			r.Unknown++
		}
	}
	clamp(v, &r)
	return r
}

func clamp(v *msg, r *schemaread.Report) {
	if v.Level > maxLevel {
		v.Level, r.Clamped = maxLevel, r.Clamped+1
	}
	if v.Kind >= nKinds {
		v.Kind, r.Clamped = nKinds-1, r.Clamped+1
	}
	if len(v.Name) > maxName {
		v.Name, r.Clamped = v.Name[:maxName], r.Clamped+1
	}
}

func loadJSON(v *msg, in []byte) schemaread.Report {
	var r schemaread.Report
	var m map[string]json.RawMessage
	if json.Unmarshal(in, &m) != nil {
		r.Malformed = true
		return r
	}
	for k, raw := range m {
		var err error
		switch k {
		case "id":
			err = json.Unmarshal(raw, &v.ID)
		case "level":
			err = json.Unmarshal(raw, &v.Level)
		case "kind":
			err = json.Unmarshal(raw, &v.Kind)
		case "name":
			err = json.Unmarshal(raw, &v.Name)
		default:
			r.Unknown++
		}
		if err != nil {
			r.KindMismatch++
		}
	}
	clamp(v, &r)
	return r
}

func loadLegacy(v *msg, in []byte) schemaread.Report {
	f := strings.Fields(string(in))
	if len(f) != 5 {
		return schemaread.Report{Malformed: true}
	}
	v.ID, v.Level, v.Kind, v.Name = f[1][0]-'0', f[2][0]-'0', f[3][0]-'0', f[4]
	return schemaread.Report{}
}

func measure(wire []byte) error {
	if len(wire) < 2 || wire[len(wire)-1] != trailer {
		return errors.New("wire_damaged")
	}
	return nil
}

func constraints(v *msg, c *schemaread.Checker) {
	c.Required("id", v.ID != 0)
	c.Bound("level", int64(v.Level), 0, maxLevel)
	c.Enum("kind", int(v.Kind), nKinds)
	c.Text("name", v.Name, maxName)
}

var saved int

func codec() *schemaread.Codec[msg] {
	return &schemaread.Codec[msg]{
		Name:        "TESTMSG",
		Help:        "nova-doctor schema",
		Reset:       func(v *msg) { *v = msg{} },
		Forms:       []byte{1},
		Measure:     measure,
		LoadWire:    loadWire,
		LoadJSON:    loadJSON,
		Legacy:      func(in []byte) bool { return bytes.HasPrefix(in, []byte("legacy ")) },
		LoadLegacy:  loadLegacy,
		Constraints: constraints,
		SaveWire: func(v *msg) []byte {
			saved++
			return wireOf(tlv(1, v.ID), tlv(2, v.Level), tlv(3, v.Kind), tlv(4, []byte(v.Name)...))
		},
		SaveJSON: func(v *msg) []byte {
			saved++
			b, _ := json.Marshal(map[string]any{"id": v.ID, "level": v.Level, "kind": v.Kind, "name": v.Name})
			return b
		},
	}
}

func refusedAs(t *testing.T, err error, name string) {
	t.Helper()
	var ref *schemaread.Refusal
	require.ErrorAs(t, err, &ref, "want refusal %q", name)
	require.Equal(t, name, ref.Reason)
	assert.True(t, strings.HasPrefix(err.Error(), "TESTMSG REFUSED: "+name), "house style: %q", err.Error())
	assert.Contains(t, err.Error(), "; run: nova-doctor schema")
}

func TestStrictRead(t *testing.T) {
	t.Parallel()
	want := msg{ID: 7, Level: 3, Kind: 1, Name: "bob"}
	jsonText := []byte(`{"id":7,"level":3,"kind":1,"name":"bob"}`)

	t.Run("same value in every form", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		b64 := []byte(base64.StdEncoding.EncodeToString(good()))
		for _, c := range []struct {
			form schemaread.Form
			in   []byte
		}{
			{schemaread.FormBinary, good()},
			{schemaread.FormBase64, b64},
			{schemaread.FormBase64, append(b64, '\n')},
			{schemaread.FormJSON, jsonText},
			{schemaread.FormJSON, append([]byte("  "), jsonText...)},
			{schemaread.FormLegacy, []byte("legacy 7 3 1 bob")},
		} {
			got, res, err := r.Read(c.in)
			require.NoError(t, err, "%s %q", c.form, c.in)
			assert.Equal(t, want, got, "%s %q", c.form, c.in)
			assert.Equal(t, c.form, res.Form)
		}
		// each read moved one counter, the form read, once
		for f, n := range map[schemaread.Form]uint64{
			schemaread.FormBinary: 1, schemaread.FormBase64: 2, schemaread.FormJSON: 2, schemaread.FormLegacy: 1,
		} {
			assert.Equal(t, n, r.Counts().Reads(f), "reads of %s", f)
		}
		snap := r.Counts().Snapshot()
		assert.Equal(t, uint64(1), snap["binary"])
		assert.Equal(t, uint64(2), snap["base64"])
		assert.Equal(t, uint64(2), snap["json"])
		assert.Equal(t, uint64(1), snap["legacy"])
	})

	t.Run("refused by name", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		for _, c := range []struct {
			name string
			in   []byte
		}{
			{"clamped", wireOf(tlv(1, 7), tlv(2, 99))},
			{"clamped", []byte(`{"id":7,"level":99}`)},
			{"clamped", wireOf(tlv(1, 7), tlv(4, []byte("123456789")...))},
			{"kind_mismatch", wireOf(tlv(1, 7), tlv(2, 1, 2))},
			{"kind_mismatch", []byte(`{"id":7,"level":"high"}`)},
			{"malformed", []byte{1, 1, 9, trailer}},
			{"malformed", []byte(`{"id":`)},
			{"malformed", []byte("AAA")},
			{"malformed", []byte("legacy 1")},
			{"unknown_form", []byte{0, 1, trailer}},
			{"unknown_form", []byte{0xFF, 1, trailer}},
			{"unknown_form", []byte("???")},
			{"unknown_form", nil},
			{"wire_damaged", []byte{1, 1, 1, 7, 0}},
			{"wire_damaged", []byte(base64.StdEncoding.EncodeToString([]byte{1, 1, 1, 7, 0}))},
			{"absent_required", wireOf(tlv(2, 3))},
			{"absent_required", []byte(`{"level":3}`)},
			{"malformed", wireOf(tlv(1, 7), tlv(4, 'a', 0))}, // NUL in text
			{"malformed", wireOf(tlv(1, 7), tlv(4, 0xC3))},   // invalid UTF-8
			{"malformed", []byte("legacy 7 3 1 b\xffb")},     // invalid UTF-8
		} {
			_, _, err := r.Read(c.in)
			refusedAs(t, err, c.name)
		}
		assert.Equal(t, uint64(4), r.Counts().Refused("unknown_form"))
		assert.Equal(t, uint64(2), r.Counts().Refused("absent_required"))
	})

	t.Run("unknown field tolerated and counted", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		got, res, err := r.Read(wireOf(tlv(1, 7), tlv(2, 3), tlv(3, 1), tlv(4, []byte("bob")...), tlv(9, 1), tlv(10, 2, 3)))
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, 2, res.Unknown)
		_, res, err = r.Read([]byte(`{"id":7,"extra":1}`))
		require.NoError(t, err)
		assert.Equal(t, 1, res.Unknown)
		assert.Equal(t, uint64(3), r.Counts().Unknown())
	})

	t.Run("write side refuses what the reader refuses before any byte", func(t *testing.T) {
		t.Parallel()
		c := codec()
		r := schemaread.NewReader(c)
		saved = 0
		for _, v := range []struct {
			name string
			v    msg
		}{
			{"absent_required", msg{}},
			{"clamped", msg{ID: 1, Level: 11}},
			{"clamped", msg{ID: 1, Kind: 3}},
			{"clamped", msg{ID: 1, Name: "123456789"}},
			{"malformed", msg{ID: 1, Name: "a\x00"}},
			{"malformed", msg{ID: 1, Name: "\xc3"}},
		} {
			v := v
			refusedAs(t, r.Check(&v.v), v.name)
			for _, f := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
				b, err := r.Write(&v.v, f)
				assert.Nil(t, b, "%+v written as %s", v.v, f)
				refusedAs(t, err, v.name)
			}
		}
		assert.Zero(t, saved, "a writer ran before the check refused")
		// what Write emits, Read accepts, in every form
		for _, f := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			out, err := r.Write(&want, f)
			require.NoError(t, err)
			got, res, err := r.Read(out)
			require.NoError(t, err, "round trip %s", f)
			assert.Equal(t, want, got)
			assert.Equal(t, f, res.Form)
		}
		_, err := r.Write(&want, schemaread.FormLegacy)
		assert.Error(t, err, "legacy is read only")
	})
}
