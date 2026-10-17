package seatcred_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	wire "github.com/mas-bandwidth/nova-tools/pkg/seatrecwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seat environment record LoadSeatEnvironment reads is read alike from the
// binary image of the seatrec SeatEnvironment table, the padded standard
// base64 of that image, the table's schema JSON text and the legacy JSON
// object every writer writes today (S2), refused by name when damaged and
// counted per form (C13). Every writer still writes the legacy form.

// seatTableModel is the schema JSON text shape of the SeatEnvironment table.
type seatTableModel struct {
	Version          *int64              `json:"version,omitempty"`
	ForgeIdentity    *string             `json:"forge_identity,omitempty"`
	NovaSprintRedis  *string             `json:"nova_sprint_redis,omitempty"`
	NovaSprintServer *string             `json:"nova_sprint_server,omitempty"`
	NovaBusRedis     *string             `json:"nova_bus_redis,omitempty"`
	Login            *seatcred.SeatLogin `json:"login,omitempty"`
}

func sp(s string) *string { return &s }
func ip(v int64) *int64   { return &v }

// seatForms is one value in every form: the binary image and its base64 made
// by the generated writer from the schema JSON text, the JSON text as the
// generated writer writes it (absent fields as null), and the legacy fixture
// exactly as nova-up --seat writes it (internal/up/seat_environment.go:
// json.MarshalIndent with two-space indent, then a trailing newline).
type seatForms struct {
	binary, base64, json, legacy []byte
}

func tableImage(t testing.TB, model seatTableModel) (image, text []byte) {
	t.Helper()
	in, err := json.Marshal(model)
	require.NoError(t, err)
	var b wire.SeatEnvironmentBuilder
	var r wire.TableReport
	require.True(t, b.Init() && wire.SeatEnvironmentFromJson(&b, in, &r) && b.Lock(), "schema JSON text %s loads", in)
	v := b.AsConst()
	n := wire.SeatEnvironmentMeasure(v)
	require.Positive(t, n)
	image = make([]byte, n)
	require.Equal(t, n, wire.SeatEnvironmentSave(v, image))
	m := wire.SeatEnvironmentToJsonMeasure(v)
	require.Positive(t, m)
	text = make([]byte, m)
	require.Equal(t, m, wire.SeatEnvironmentToJson(v, text))
	return image, text
}

func formsOf(t testing.TB, rec seatcred.SeatEnvironment) seatForms {
	t.Helper()
	model := seatTableModel{Version: ip(int64(rec.Version)), Login: rec.Login}
	if rec.ForgeIdentity != "" {
		model.ForgeIdentity = sp(rec.ForgeIdentity)
	}
	if v, ok := rec.Environment["NOVA_SPRINT_REDIS"]; ok {
		model.NovaSprintRedis = sp(v)
	}
	if v, ok := rec.Environment["NOVA_SPRINT_SERVER"]; ok {
		model.NovaSprintServer = sp(v)
	}
	if v, ok := rec.Environment["NOVA_BUS_REDIS"]; ok {
		model.NovaBusRedis = sp(v)
	}
	image, text := tableImage(t, model)
	legacy, err := json.MarshalIndent(rec, "", "  ")
	require.NoError(t, err)
	legacy = append(legacy, '\n')
	return seatForms{binary: image, base64: []byte(base64.StdEncoding.EncodeToString(image)), json: text, legacy: legacy}
}

func (f seatForms) each() []struct {
	form schemaread.Form
	in   []byte
} {
	return []struct {
		form schemaread.Form
		in   []byte
	}{{schemaread.FormBinary, f.binary}, {schemaread.FormBase64, f.base64}, {schemaread.FormJSON, f.json}, {schemaread.FormLegacy, f.legacy}}
}

// writeSeat writes one private seat file in dir, as a seat holds it.
func writeSeat(t *testing.T, dir string, body []byte) string {
	t.Helper()
	path := filepath.Join(dir, seatcred.EnvironmentFile)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	require.NoError(t, os.Chmod(path, 0o600))
	return path
}

// refusedByName is true when err is the named seat environment refusal.
func refusedByName(err error) bool {
	var r *schemaread.Refusal
	return errors.As(err, &r) && r.Name == "SEAT_ENVIRONMENT" && strings.Contains(r.Error(), "SEAT_ENVIRONMENT REFUSED: ")
}

func reads(c *schemaread.Counts) map[schemaread.Form]uint64 {
	return map[schemaread.Form]uint64{
		schemaread.FormBinary: c.Reads(schemaread.FormBinary), schemaread.FormBase64: c.Reads(schemaread.FormBase64),
		schemaread.FormJSON: c.Reads(schemaread.FormJSON), schemaread.FormLegacy: c.Reads(schemaread.FormLegacy),
	}
}

func TestSeatEnvironmentDualRead(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nova-sprint")
	require.NoError(t, os.Mkdir(dir, 0o700))
	forge := filepath.Join(t.TempDir(), "gh-example")
	require.NoError(t, os.Mkdir(forge, 0o700))

	full := seatcred.SeatEnvironment{
		Version:       1,
		ForgeIdentity: forge,
		Environment: map[string]string{
			"NOVA_SPRINT_REDIS": "127.0.0.1:6381", "NOVA_SPRINT_SERVER": "sprint.example:7000", "NOVA_BUS_REDIS": "[::1]:6390",
		},
		Login: &seatcred.SeatLogin{Redis: "store.example:6379", User: "seat-example", Store: "/srv/example/store",
			As: "example", Key: "/srv/example/age.key", Sops: "/srv/example/sops", Secret: "EXAMPLE_PASSWORD"},
	}
	bare := seatcred.SeatEnvironment{Version: 1}
	fullForms, bareForms := formsOf(t, full), formsOf(t, bare)
	require.Contains(t, string(fullForms.legacy), "\n  \"environment\": {\n", "the legacy fixture is indented as the writer writes it")
	require.True(t, strings.HasSuffix(string(fullForms.legacy), "}\n"), "the legacy fixture ends in the writer's newline")
	require.Contains(t, string(bareForms.json), `"nova_bus_redis": null`, "the generated JSON writer names an absent field as null")

	r := seatcred.NewSeatEnvironmentReader()

	t.Run("every form reads to one equal record", func(t *testing.T) {
		for _, tc := range []struct {
			want  seatcred.SeatEnvironment
			forms seatForms
		}{{full, fullForms}, {bare, bareForms}} {
			for _, f := range tc.forms.each() {
				got, err := r.Load(writeSeat(t, dir, f.in))
				require.NoError(t, err, "%s form", f.form)
				assert.Equal(t, tc.want, got, "%s form reads to the record", f.form)
			}
		}
		// The generated JSON text without whitespace, and the legacy object
		// compact and with no trailing newline, read alike: the form is
		// decided by the object's shape, never by its layout.
		compactJSON := []byte(strings.Join(strings.Fields(string(fullForms.json)), ""))
		compactLegacy, err := json.Marshal(full)
		require.NoError(t, err)
		for _, in := range [][]byte{compactJSON, compactLegacy} {
			got, err := r.Load(writeSeat(t, dir, in))
			require.NoError(t, err)
			assert.Equal(t, full, got)
		}
		// LoadSeatEnvironment is the same read through the package's reader.
		got, err := seatcred.LoadSeatEnvironment(writeSeat(t, dir, fullForms.binary))
		require.NoError(t, err)
		assert.Equal(t, full, got)
	})
	before := reads(r.Counts)
	assert.Equal(t, map[schemaread.Form]uint64{
		schemaread.FormBinary: 2, schemaread.FormBase64: 2, schemaread.FormJSON: 3, schemaread.FormLegacy: 3,
	}, before, "each read is counted once, under the form read")

	t.Run("damage, clamps and unknown fields are refused by name in every form", func(t *testing.T) {
		clampImage, clampJSON := tableImage(t, seatTableModel{Version: ip(2), NovaBusRedis: sp("127.0.0.1:6390")})
		_, hugeJSON := tableImage(t, seatTableModel{Version: ip(1), NovaBusRedis: sp("127.0.0.1:6390")})
		hugeJSON = []byte(strings.Replace(string(hugeJSON), `"version": 1`, `"version": 99999999999`, 1))
		noVersionImage, _ := tableImage(t, seatTableModel{NovaBusRedis: sp("127.0.0.1:6390")})
		emptyImage, emptyJSON := tableImage(t, seatTableModel{Version: ip(1), NovaBusRedis: sp("")})
		mid := len(fullForms.binary) / 2
		flipped := append([]byte(nil), fullForms.binary...)
		flipped[mid] ^= 0xff
		for _, tc := range []struct {
			name, want string
			form       schemaread.Form
			in         []byte
		}{
			{"binary truncated", "", schemaread.FormBinary, fullForms.binary[:len(fullForms.binary)-1]},
			{"binary flipped", "", schemaread.FormBinary, flipped},
			{"binary form byte", "unknown_form", schemaread.FormBinary, append([]byte{9}, fullForms.binary[1:]...)},
			{"binary clamped version", "clamped (version)", schemaread.FormBinary, clampImage},
			{"binary absent version", "absent_required (version)", schemaread.FormBinary, noVersionImage},
			{"binary empty endpoint is never absent", "invalid endpoint", schemaread.FormBinary, emptyImage},
			{"base64 truncated", "malformed", schemaread.FormBase64, fullForms.base64[:len(fullForms.base64)-3]},
			{"base64 of the flipped image", "", schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(flipped))},
			{"base64 clamped version", "clamped (version)", schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(clampImage))},
			{"json truncated", "malformed", schemaread.FormJSON, fullForms.json[:len(fullForms.json)-3]},
			{"json flipped", "", schemaread.FormJSON, []byte(strings.Replace(string(fullForms.json), `"version": 1`, `"version": "1"`, 1))},
			{"json clamped version", "clamped (version)", schemaread.FormJSON, clampJSON},
			{"json clamped past the field", "clamped", schemaread.FormJSON, hugeJSON},
			{"json unknown key", "unknown_field (unknown key)", schemaread.FormJSON, []byte(strings.Replace(string(fullForms.json), `"version"`, `"extra": 1, "version"`, 1))},
			{"json unknown login key", "unknown_field (unknown key)", schemaread.FormJSON, []byte(strings.Replace(string(fullForms.json), `"redis"`, `"extra": "x", "redis"`, 1))},
			{"json legacy nest beside the table fields", "unknown_field (unknown key)", schemaread.FormJSON,
				[]byte(`{"version":1,"nova_bus_redis":"127.0.0.1:6390","environment":{"NOVA_BUS_REDIS":"127.0.0.1:6390"}}`)},
			{"json duplicate key", "malformed", schemaread.FormJSON, []byte(`{"version":1,"version":1,"nova_bus_redis":null}`)},
			{"json empty endpoint is never absent", "invalid endpoint", schemaread.FormJSON, emptyJSON},
			{"legacy truncated", "invalid JSON", schemaread.FormLegacy, fullForms.legacy[:len(fullForms.legacy)-3]},
			{"legacy flipped", "unknown key", schemaread.FormLegacy, []byte(strings.Replace(string(fullForms.legacy), `"version"`, `"versiom"`, 1))},
			{"legacy clamped version", "not a supported v1 record", schemaread.FormLegacy, []byte(`{"version":99999999999}`)},
			{"legacy unknown key", "unknown key", schemaread.FormLegacy, []byte(`{"version":1,"extra":true}`)},
			{"legacy null", "null value", schemaread.FormLegacy, []byte(`{"version":1,"login":null}`)},
			{"empty file", "unknown_form", schemaread.FormNone, nil},
		} {
			before := reads(r.Counts)
			got, err := r.Load(writeSeat(t, dir, tc.in))
			require.Error(t, err, tc.name)
			assert.True(t, refusedByName(err), "%s: %v", tc.name, err)
			assert.Contains(t, err.Error(), tc.want, tc.name)
			assert.Equal(t, seatcred.SeatEnvironment{}, got, "%s is never read as a record", tc.name)
			after := reads(r.Counts)
			for f, n := range after {
				want := before[f]
				if f == tc.form {
					want++
				}
				assert.Equal(t, want, n, "%s counted under %s", tc.name, f)
			}
		}
		assert.Positive(t, r.Counts.Refused("clamped"))
		assert.Positive(t, r.Counts.Refused("unknown_field"))
		assert.Positive(t, r.Counts.Refused("malformed"))
	})

	t.Run("every flip and truncation of every form is refused by name or reads a v1 record", func(t *testing.T) {
		// Each damaged value is refused by name or reads as a whole v1 record,
		// never as absent, empty or zero. A flip that changes a text in place
		// without breaking the structure is the wire's limit (no checksum):
		// counted as a silent change. Each JSON text load allocates and clears a
		// 1 MiB arena segment (the generated builder), so the sweep is two masks
		// per byte and 300 seeded multi-byte damages per form.
		rng := rand.New(rand.NewPCG(1000, 1000))
		for _, f := range fullForms.each() {
			var refused, same, silent int
			read := func(in []byte) {
				got, err := r.Read(in)
				if err != nil {
					require.True(t, refusedByName(err), "%s: %q: %v", f.form, in, err)
					require.Equal(t, seatcred.SeatEnvironment{}, got)
					refused++
					return
				}
				require.Equal(t, 1, got.Version, "%s: a read record is a v1 record", f.form)
				if assert.ObjectsAreEqual(full, got) {
					same++
				} else {
					silent++
				}
			}
			for i := range f.in {
				for _, mask := range []byte{0x01, 0xff} {
					in := append([]byte(nil), f.in...)
					in[i] ^= mask
					read(in)
				}
				read(f.in[:i])
			}
			for range 300 {
				in := append([]byte(nil), f.in...)
				for range 1 + rng.IntN(3) {
					in[rng.IntN(len(in))] ^= byte(1 + rng.IntN(255))
				}
				if rng.IntN(4) == 0 {
					in = in[:rng.IntN(len(in))]
				}
				read(in)
			}
			assert.Positive(t, refused, f.form.String())
			t.Logf("%s: damaged reads refused=%d unchanged=%d silent-value-change=%d", f.form, refused, same, silent)
		}
	})
}

// FuzzSeatEnvironmentDualRead: any input, in any form, is refused by name or
// reads as a v1 record, the same on a second read.
func FuzzSeatEnvironmentDualRead(f *testing.F) {
	forms := formsOf(f, seatcred.SeatEnvironment{Version: 1, Environment: map[string]string{"NOVA_BUS_REDIS": "127.0.0.1:6390"}})
	for _, seed := range [][]byte{forms.binary, forms.base64, forms.json, forms.legacy, []byte("{oops"), nil} {
		f.Add(seed)
	}
	r := seatcred.NewSeatEnvironmentReader()
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := r.Read(in)
		if err != nil {
			require.True(t, refusedByName(err), "%v", err)
			require.Equal(t, seatcred.SeatEnvironment{}, got)
			return
		}
		require.Equal(t, 1, got.Version)
		again, err := r.Read(in)
		require.NoError(t, err)
		require.Equal(t, got, again)
	})
}
