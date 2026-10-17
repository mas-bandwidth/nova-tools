package config_test

// TestConfigRowsDualRead is the reader half of the nova-config rows' table
// adoption (v1.5 aspect 9; the owner's rules S2 and S4): every record
// pkg/config reads back from a store (a history row's before and after, the
// file twin, a loop's Redis view, a machine's member beat and bench hash, a
// canonical argv) is read alike from its binary image (the table wire and a
// fixed batch), the padded standard base64 of each, and the legacy form its
// writer writes today; every damaged value of each form is refused by name
// (`<RECORD> REFUSED: <reason>; run: <help>`) and never read as absent; a
// value past its bound is refused as clamped; and each read is counted under
// its record and its form.
//
// Unit tier: no clock, no socket, no program, no store. The Postgres and Redis
// reads are tested through their decode functions (Records.ReadSide,
// ReadMemberBeat, ReadBenchBeat) on fixed inputs; the file twin is read
// through OpenFile, the house's store with no server.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	_ "github.com/mas-bandwidth/nova-tools/pkg/config/rowsread"
	"github.com/mas-bandwidth/nova-tools/pkg/configwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

func TestConfigRowsDualRead(t *testing.T) {
	t.Parallel()

	t.Run("change side", func(t *testing.T) {
		t.Parallel()
		want := map[string]string{"note": "a line", "slots": "8", "argv": `["/bin/true"]`}
		v := rowFieldsOf(want)
		legacy, err := json.Marshal(want)
		require.NoError(t, err)
		c := dual[map[string]string]{name: "CHANGE", want: want, wire: rowFieldsImage.wire(t, &v), fixed: rowFieldsImage.fixed(t, v), legacy: legacy, legacyBytes: true,
			read:   func(r *config.Records, in []byte, _ bool) (map[string]string, error) { return r.ReadSide(in) },
			counts: func(r *config.Records) *schemaread.Counts { return r.Side.Counts() }}
		c.run(t)
		r := config.NewRecords()
		over := map[string]string{"note": strings.Repeat("n", config.MaxFieldValue+1)}
		raw, err := json.Marshal(over)
		require.NoError(t, err)
		_, err = r.ReadSide(raw)
		assert.Equal(t, schemaread.ReasonClamped, refusal(t, err, "CHANGE").Reason, "a value past MaxFieldValue")
		// the write's reason rides on the side (pg.go record), bounded by MaxReason
		got, err := r.ReadSide([]byte(`{"note":"x","reason":"why"}`))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"note": "x", "reason": "why"}, got)
		_, err = r.ReadSide([]byte(`{"note":1}`))
		assert.Equal(t, schemaread.ReasonMalformed, refusal(t, err, "CHANGE").Reason, "a field that is not text")
		// a pair out of name order is not the unique image of a row
		bad := rowFieldsOf(map[string]string{"a": "1", "b": "2"})
		bad.Pairs[0], bad.Pairs[1] = bad.Pairs[1], bad.Pairs[0]
		_, err = r.ReadSide(rowFieldsImage.wire(t, &bad))
		assert.Equal(t, schemaread.ReasonMalformed, refusal(t, err, "CHANGE").Reason)
	})

	t.Run("file twin", func(t *testing.T) {
		t.Parallel()
		all, err := config.Migrations()
		require.NoError(t, err)
		want := sampleFile(len(all))
		legacy, err := json.Marshal(want)
		require.NoError(t, err)
		image := fileImage(t, want, true)
		c := dual[config.FileState]{name: "FILE", want: want, fixed: image, legacy: legacy, legacyBytes: true,
			read:   func(r *config.Records, in []byte, _ bool) (config.FileState, error) { return r.ReadFile(in) },
			counts: func(r *config.Records) *schemaread.Counts { return r.File.Counts() }}
		c.run(t)

		r := config.NewRecords()
		// a change that names a side the image does not hold
		_, err = r.ReadFile(fileImage(t, want, false))
		assert.Equal(t, schemaread.ReasonMalformed, refusal(t, err, "FILE").Reason, "a missing side")
		// the canonical kind and op words: a history row of no kind is refused
		bad := sampleFile(len(all))
		bad.History[0].Op = "replace"
		raw, err := json.Marshal(bad)
		require.NoError(t, err)
		_, err = r.ReadFile(raw)
		ref := refusal(t, err, "FILE")
		assert.Equal(t, schemaread.ReasonClamped, ref.Reason, "an op no word names")
		assert.Equal(t, "history.op", ref.Field)
		bad = sampleFile(len(all))
		bad.History[0].Actor = strings.Repeat("a", config.MaxActor+1)
		raw, err = json.Marshal(bad)
		require.NoError(t, err)
		_, err = r.ReadFile(raw)
		assert.Equal(t, schemaread.ReasonClamped, refusal(t, err, "FILE").Reason, "an actor past MaxActor")

		// the read site: OpenFile reads the typed twin as it reads the JSON
		dir := t.TempDir()
		for name, body := range map[string][]byte{"binary": image, "base64": []byte(base64.StdEncoding.EncodeToString(image)), "legacy": legacy} {
			path := filepath.Join(dir, name)
			require.NoError(t, os.WriteFile(path, body, 0o600))
			f, err := config.OpenFile(path)
			require.NoError(t, err, name)
			row, found, err := f.Get(t.Context(), config.KindLoop, "l1")
			require.NoError(t, err, name)
			require.True(t, found, name)
			assert.Equal(t, `["/bin/true","-x"]`, row.Fields["argv"], name)
		}
		path := filepath.Join(dir, "damaged")
		require.NoError(t, os.WriteFile(path, image[:len(image)-1], 0o600))
		_, err = config.OpenFile(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not a nova-config store file (FILE REFUSED: ")
	})

	t.Run("loop view", func(t *testing.T) {
		t.Parallel()
		want := config.View{"machine": "host-a", "name": "l1", "argv": `["/bin/true","-x"]`, "seat": "host-a", "keys": "A_KEY,B_KEY",
			"every": "60", "keepalive": "false", "enabled": "true", "log": "/srv/loops/l1.log"}
		v := inventoryLoopOf(want)
		legacy, err := json.Marshal(want)
		require.NoError(t, err)
		c := dual[config.View]{name: "LOOP", want: want, wire: inventoryLoopImage.wire(t, &v), fixed: inventoryLoopImage.fixed(t, v), legacy: legacy,
			read:   readHash(func(r *config.Records, h config.View) (config.View, error) { return r.ReadLoop(h) }),
			counts: func(r *config.Records) *schemaread.Counts { return r.Loop.Counts() }}
		c.run(t)
		over := with(want, "seat", strings.Repeat("s", config.MaxFieldValue+1))
		_, err = config.NewRecords().ReadLoop(over)
		ref := refusal(t, err, "LOOP")
		assert.Equal(t, schemaread.ReasonClamped, ref.Reason, "a seat past MaxFieldValue")
		assert.Equal(t, "seat", ref.Field)
		// the typed image patched past every's bound (one year of seconds)
		v.Every = 31536000
		for _, img := range [][]byte{inventoryLoopImage.wire(t, &v), inventoryLoopImage.fixed(t, v)} {
			_, err = config.NewRecords().ReadLoop(config.View{config.WireField: string(patch32(t, img, 31536000, 31536001))})
			assert.Equal(t, schemaread.ReasonClamped, refusal(t, err, "LOOP").Reason, "every past its bound")
		}

		// the read site: the inventory reads the typed view as it reads the hash
		v.Every = 60
		snap := loopSnapshot(config.View{config.WireField: base64.StdEncoding.EncodeToString(inventoryLoopImage.wire(t, &v))})
		inv, err := config.BuildInventory(snap, "")
		require.NoError(t, err)
		loops := inv.Meta.Hostvars["host-a"]["nova_loops"].([]config.InventoryLoop)
		require.Len(t, loops, 1)
		assert.Equal(t, []string{"/bin/true", "-x"}, loops[0].Argv)
		assert.Equal(t, []string{"A_KEY", "B_KEY"}, loops[0].Keys)
		snap.Loops["l1"] = config.View{config.WireField: "AQ=="}
		_, err = config.BuildInventory(snap, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "loop l1: LOOP REFUSED: ")
	})

	t.Run("member beat", func(t *testing.T) {
		t.Parallel()
		want := config.MemberBeat{AtMS: 1790000000000, Cores: 32}
		v := configwire.MemberBeat{AtMs: want.AtMS, Cores: int32(want.Cores)}
		// the sprint's other fields are the sprint's, and are passed over
		legacy := []byte(`{"at":"2026-09-21T14:13:20Z","cores":32,"host":"m1","lanes":[1,2]}`)
		c := dual[config.MemberBeat]{name: "MEMBER_BEAT", want: want, wire: memberImage.wire(t, &v), fixed: memberImage.fixed(t, v), legacy: legacy, legacyBytes: true,
			read: func(r *config.Records, in []byte, _ bool) (config.MemberBeat, error) {
				return r.ReadMemberBeat(string(in))
			},
			counts: func(r *config.Records) *schemaread.Counts { return r.Member.Counts() }}
		c.run(t)
		r := config.NewRecords()
		for in, reason := range map[string]string{
			`{"at":"yesterday","cores":32}`:              schemaread.ReasonMalformed,
			`{"at":"2026-09-21T14:13:20Z","cores":"32"}`: schemaread.ReasonMalformed,
			`{"cores":100001}`:                           schemaread.ReasonClamped,
			`{"at":"1969-12-31T00:00:00Z"}`:              schemaread.ReasonClamped,
			`not json`:                                   schemaread.ReasonMalformed,
		} {
			_, err := r.ReadMemberBeat(in)
			assert.Equal(t, reason, refusal(t, err, "MEMBER_BEAT").Reason, in)
		}
		v.Cores = 100000
		for _, img := range [][]byte{memberImage.wire(t, &v), memberImage.fixed(t, v)} {
			_, err := r.ReadMemberBeat(string(patch32(t, img, 100000, 100001)))
			assert.Equal(t, schemaread.ReasonClamped, refusal(t, err, "MEMBER_BEAT").Reason, "cores past its bound")
		}
	})

	t.Run("bench hash", func(t *testing.T) {
		t.Parallel()
		want := config.View{"host": "m1", "os": "linux", "arch": "amd64", "ncpu": "64", "memory_gb": "251", "load1": "0.52", "cpu": "3.1", "at": "1790000000000"}
		var v configwire.BenchBeat
		put(v.Host[:], &v.HostLength, want["host"])
		put(v.Os[:], &v.OsLength, want["os"])
		put(v.Arch[:], &v.ArchLength, want["arch"])
		put(v.Ncpu[:], &v.NcpuLength, want["ncpu"])
		put(v.MemoryGb[:], &v.MemoryGbLength, want["memory_gb"])
		put(v.Load1[:], &v.Load1Length, want["load1"])
		put(v.Cpu[:], &v.CpuLength, want["cpu"])
		v.AtMs = 1790000000000
		legacy, err := json.Marshal(want)
		require.NoError(t, err)
		c := dual[config.View]{name: "BENCH_BEAT", want: want, wire: benchImage.wire(t, &v), fixed: benchImage.fixed(t, v), legacy: legacy,
			read:   readHash(func(r *config.Records, h config.View) (config.View, error) { return r.ReadBenchBeat(h) }),
			counts: func(r *config.Records) *schemaread.Counts { return r.Bench.Counts() }}
		c.run(t)
		r := config.NewRecords()
		for field, val := range map[string]string{"os": strings.Repeat("o", 33), "ncpu": "123456789", "at": "-1"} {
			_, err := r.ReadBenchBeat(with(want, field, val))
			ref := refusal(t, err, "BENCH_BEAT")
			assert.Equal(t, schemaread.ReasonClamped, ref.Reason, field)
			assert.Equal(t, field, ref.Field)
		}
		_, err = r.ReadBenchBeat(with(want, "at", "noon"))
		assert.Equal(t, schemaread.ReasonMalformed, refusal(t, err, "BENCH_BEAT").Reason, "an at that is not milliseconds")
	})

	t.Run("canonical argv", func(t *testing.T) {
		t.Parallel()
		r := config.NewRecords()
		got, err := r.ReadArgv(`["/bin/sh","-c","a && b"]`)
		require.NoError(t, err)
		assert.Equal(t, []string{"/bin/sh", "-c", "a && b"}, got)
		in := []byte(`["/bin/sh","-c","a && b"]`)
		for _, m := range damaged(in) {
			_, err := r.ReadArgv(string(m))
			assert.NotNil(t, refusal(t, err, "ARGV"), "a damaged argv")
		}
		// no table carries an argv alone: a typed image is no argv
		_, err = r.ReadArgv("AQ==")
		assert.Equal(t, schemaread.ReasonUnknownForm, refusal(t, err, "ARGV").Reason)
		over, err := json.Marshal(make([]string, config.MaxArgs+1))
		require.NoError(t, err)
		_, err = r.ReadArgv(string(over))
		assert.Equal(t, schemaread.ReasonClamped, refusal(t, err, "ARGV").Reason, "past MaxArgs words")
		// the good read, every damaged one but the empty (no form), and the one past MaxArgs
		assert.Equal(t, uint64(len(damaged(in))+1), r.Argv.Counts().Reads(schemaread.FormLegacy), "every read is counted under its form")
		// the read site keeps its rule: an unreadable argv is no command
		assert.Nil(t, config.Argv("not an argv"))
	})
}

// dual is one record's dual read: its value, its images and its legacy form,
// and how a store hands each to the reader.
type dual[T any] struct {
	name        string
	want        T
	wire, fixed []byte // the table wire and the fixed batch; nil when the record has none
	legacy      []byte
	legacyBytes bool // the legacy form is bytes a store holds (a hash is a map and cannot be damaged as bytes)
	read        func(r *config.Records, in []byte, typed bool) (T, error)
	counts      func(r *config.Records) *schemaread.Counts
}

func (d dual[T]) run(t *testing.T) {
	t.Helper()
	r := config.NewRecords()
	type input struct {
		form  string
		in    []byte
		typed bool
	}
	var inputs []input
	for _, img := range []struct {
		form string
		in   []byte
	}{{"binary", d.wire}, {"binary fixed", d.fixed}} {
		if img.in != nil {
			inputs = append(inputs, input{img.form, img.in, true}, input{"base64 of the " + img.form, []byte(base64.StdEncoding.EncodeToString(img.in)), true})
		}
	}
	inputs = append(inputs, input{"legacy", d.legacy, false})
	images := 0
	for _, in := range inputs {
		got, err := d.read(r, in.in, in.typed)
		require.NoError(t, err, "%s %s", d.name, in.form)
		assert.Equal(t, d.want, got, "%s read alike from its %s", d.name, in.form)
		if in.typed {
			images++
		}
	}
	counts := d.counts(r)
	assert.Equal(t, uint64(images/2), counts.Reads(schemaread.FormBinary), "%s: each read counted once under its form", d.name)
	assert.Equal(t, uint64(images/2), counts.Reads(schemaread.FormBase64), d.name)
	assert.Equal(t, uint64(1), counts.Reads(schemaread.FormLegacy), d.name)
	assert.Zero(t, counts.Reads(schemaread.FormJSON), d.name)

	// every damaged value of every form is refused by name, never read as
	// absent, empty or zero: each prefix of the value and the value with bytes
	// after its end
	r = config.NewRecords()
	tries := 0
	for _, in := range inputs {
		if !in.typed && !d.legacyBytes {
			continue
		}
		for _, m := range damaged(in.in) {
			got, err := d.read(r, m, in.typed)
			require.NotNil(t, refusal(t, err, d.name), "%s: a damaged %s (%d bytes) was read as %v", d.name, in.form, len(m), got)
			var zero T
			assert.Equal(t, zero, got, "%s: a refused read hands back nothing", d.name)
			tries++
		}
	}
	refusals := uint64(0)
	for k, n := range d.counts(r).Snapshot() {
		if strings.HasPrefix(k, "refused_") {
			refusals += n
		}
	}
	assert.Equal(t, uint64(tries), refusals, "%s: every damaged read is counted as refused, by reason", d.name)
}

// readHash hands a hash record to its reader: a typed image in the hash's one
// WireField, the legacy fields as the hash itself.
func readHash(read func(*config.Records, config.View) (config.View, error)) func(*config.Records, []byte, bool) (config.View, error) {
	return func(r *config.Records, in []byte, typed bool) (config.View, error) {
		if typed {
			return read(r, config.View{config.WireField: string(in)})
		}
		var h config.View
		if err := json.Unmarshal(in, &h); err != nil {
			return nil, err
		}
		return read(r, h)
	}
}

var refusalText = regexp.MustCompile(`^[A-Z_]+ REFUSED: [^;]+; run: \S.*$`)

// refusal is err as the reader's named refusal of record name.
func refusal(t *testing.T, err error, name string) *schemaread.Refusal {
	t.Helper()
	var ref *schemaread.Refusal
	if !assert.True(t, errors.As(err, &ref), "%s: want a named refusal, got %v", name, err) {
		return nil
	}
	assert.Equal(t, name, ref.Name)
	assert.Regexp(t, refusalText, err.Error())
	return ref
}

// damaged is in cut at each length (every length for a short value, the first
// and last 256 and a stride between for a long one) and in with bytes after
// its end.
func damaged(in []byte) [][]byte {
	var cuts []int
	if len(in) <= 768 {
		for n := 0; n < len(in); n++ {
			cuts = append(cuts, n)
		}
	} else {
		for n := 0; n < 256; n++ {
			cuts = append(cuts, n, len(in)-256+n)
		}
		for n := 256; n < len(in)-256; n += len(in)/97 + 1 {
			cuts = append(cuts, n)
		}
	}
	out := make([][]byte, 0, len(cuts)+3)
	for _, n := range cuts {
		out = append(out, bytes.Clone(in[:n]))
	}
	for _, tail := range []string{"\x00", "x", "AAAA"} {
		out = append(out, append(bytes.Clone(in), tail...))
	}
	return out
}

// patch32 is img with the one 32-bit little-endian old turned into new: a
// valid image patched one past a bound the writer would refuse to write.
func patch32(t *testing.T, img []byte, old, new uint32) []byte {
	t.Helper()
	var o, n [4]byte
	binary.LittleEndian.PutUint32(o[:], old)
	binary.LittleEndian.PutUint32(n[:], new)
	require.Equal(t, 1, bytes.Count(img, o[:]), "the image holds %d once", old)
	return bytes.Replace(img, o[:], n[:], 1)
}

func with(v config.View, k, val string) config.View {
	out := config.View{}
	for a, b := range v {
		out[a] = b
	}
	out[k] = val
	return out
}

func put(dst []byte, n *int32, s string) { *n = int32(copy(dst, s)) }

// rowFieldsOf is a row's fields as the table holds them: pairs in name
// order, the argv in its own slot.
func rowFieldsOf(fields map[string]string) configwire.RowFields {
	var r configwire.RowFields
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if k == "argv" {
			put(r.Argv[:], &r.ArgvLength, fields[k])
			continue
		}
		p := &r.Pairs[r.PairsCount]
		put(p.Name[:], &p.NameLength, k)
		put(p.Value[:], &p.ValueLength, fields[k])
		r.PairsCount++
	}
	return r
}

func inventoryLoopOf(v config.View) configwire.InventoryLoop {
	var l configwire.InventoryLoop
	put(l.Machine[:], &l.MachineLength, v["machine"])
	put(l.Name[:], &l.NameLength, v["name"])
	put(l.Argv[:], &l.ArgvLength, v["argv"])
	put(l.Seat[:], &l.SeatLength, v["seat"])
	put(l.Log[:], &l.LogLength, v["log"])
	for _, k := range strings.Split(v["keys"], ",") {
		put(l.Keys[l.KeysCount].Name[:], &l.Keys[l.KeysCount].NameLength, k)
		l.KeysCount++
	}
	l.Every, l.Keepalive, l.Enabled = 60, false, true
	return l
}

// image is a generated table's two writers: the table wire and a fixed batch.
type image[W any] struct {
	measure      func(*W) int64
	save         func(*W, []byte) int64
	fixedMeasure func(int64) int64
	fixedSave    func([]W, []byte) int64
}

func (im image[W]) wire(t *testing.T, v *W) []byte {
	t.Helper()
	n := im.measure(v)
	require.Positive(t, n)
	b := make([]byte, n)
	require.Equal(t, n, im.save(v, b))
	return b
}

func (im image[W]) fixed(t *testing.T, vs ...W) []byte {
	t.Helper()
	b := make([]byte, im.fixedMeasure(int64(len(vs))))
	require.Equal(t, int64(len(b)), im.fixedSave(vs, b))
	return b
}

var (
	rowFieldsImage     = image[configwire.RowFields]{configwire.RowFieldsMeasure, configwire.RowFieldsSave, configwire.RowFieldsFixedMeasure, configwire.RowFieldsFixedSave}
	configRowImage     = image[configwire.ConfigRow]{configwire.ConfigRowMeasure, configwire.ConfigRowSave, configwire.ConfigRowFixedMeasure, configwire.ConfigRowFixedSave}
	configChangeImage  = image[configwire.ConfigChange]{configwire.ConfigChangeMeasure, configwire.ConfigChangeSave, configwire.ConfigChangeFixedMeasure, configwire.ConfigChangeFixedSave}
	fileHeaderImage    = image[configwire.FileHeader]{configwire.FileHeaderMeasure, configwire.FileHeaderSave, configwire.FileHeaderFixedMeasure, configwire.FileHeaderFixedSave}
	inventoryLoopImage = image[configwire.InventoryLoop]{configwire.InventoryLoopMeasure, configwire.InventoryLoopSave, configwire.InventoryLoopFixedMeasure, configwire.InventoryLoopFixedSave}
	memberImage        = image[configwire.MemberBeat]{configwire.MemberBeatMeasure, configwire.MemberBeatSave, configwire.MemberBeatFixedMeasure, configwire.MemberBeatFixedSave}
	benchImage         = image[configwire.BenchBeat]{configwire.BenchBeatMeasure, configwire.BenchBeatSave, configwire.BenchBeatFixedMeasure, configwire.BenchBeatFixedSave}
)

func sampleFile(schema int) config.FileState {
	return config.FileState{
		Schema: schema,
		Rows: map[string]map[string]config.Row{
			config.KindMachine: {"host-a": {Name: "host-a", Fields: map[string]string{"user": "nova", "seat": "host-a", "slots": "4"}, CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-02T00:00:00Z"}},
			config.KindLoop:    {"l1": {Name: "l1", Fields: map[string]string{"machine": "host-a", "argv": `["/bin/true","-x"]`, "every": "60"}, CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z"}},
		},
		History: []config.Change{
			{ID: 1, Kind: config.KindMachine, Name: "host-a", Op: config.OpAdd, After: map[string]string{"user": "nova", "slots": "2"}, Actor: "operator", Reason: "first", At: "2026-10-01T00:00:00Z"},
			{ID: 2, Kind: config.KindMachine, Name: "host-a", Op: config.OpSet, Before: map[string]string{"slots": "2"}, After: map[string]string{"slots": "4"}, Actor: "operator", At: "2026-10-02T00:00:00Z"},
			{ID: 3, Kind: config.KindLoop, Name: "l0", Op: config.OpRemove, Before: map[string]string{"argv": `["/bin/false"]`}, Actor: "operator", At: "2026-10-03T00:00:00Z"},
		},
	}
}

// fileImage is the file twin as its four fixed batches (rowsread file); with
// sides false the sides batch is left empty.
func fileImage(t *testing.T, st config.FileState, sides bool) []byte {
	t.Helper()
	kinds := config.KindNames()
	kindOf := func(k string) configwire.ConfigKind {
		for i, w := range kinds {
			if w == k {
				return configwire.ConfigKind(i + 1)
			}
		}
		t.Fatalf("no kind %s", k)
		return 0
	}
	ops := map[string]configwire.ChangeOp{config.OpAdd: configwire.ChangeOpAdd, config.OpSet: configwire.ChangeOpSet, config.OpRemove: configwire.ChangeOpRemove}
	var rows []configwire.ConfigRow
	for kind, byName := range st.Rows {
		for _, r := range byName {
			var v configwire.ConfigRow
			v.Kind = kindOf(kind)
			put(v.Name[:], &v.NameLength, r.Name)
			v.Fields = rowFieldsOf(r.Fields)
			put(v.CreatedAt[:], &v.CreatedAtLength, r.CreatedAt)
			put(v.UpdatedAt[:], &v.UpdatedAtLength, r.UpdatedAt)
			rows = append(rows, v)
		}
	}
	var changes []configwire.ConfigChange
	var rowSides []configwire.RowFields
	for _, c := range st.History {
		var v configwire.ConfigChange
		v.Id, v.Kind, v.Op = c.ID, kindOf(c.Kind), ops[c.Op]
		put(v.Name[:], &v.NameLength, c.Name)
		put(v.Actor[:], &v.ActorLength, c.Actor)
		put(v.Reason[:], &v.ReasonLength, c.Reason)
		put(v.At[:], &v.AtLength, c.At)
		v.HasBefore, v.HasAfter = c.Before != nil, c.After != nil
		for _, s := range []map[string]string{c.Before, c.After} {
			if s != nil && sides {
				rowSides = append(rowSides, rowFieldsOf(s))
			}
		}
		changes = append(changes, v)
	}
	out := fileHeaderImage.fixed(t, configwire.FileHeader{Schema: int32(st.Schema)})
	out = append(out, configRowImage.fixed(t, rows...)...)
	out = append(out, configChangeImage.fixed(t, changes...)...)
	return append(out, rowFieldsImage.fixed(t, rowSides...)...)
}

// loopSnapshot is one machine running one loop, the loop's view given.
func loopSnapshot(loop config.View) *config.Snapshot {
	return &config.Snapshot{
		Machines: map[string]config.View{"host-a": {"user": "nova", "seat": "host-a", "slots": "1", "runners": "0", "roles": "bench,coordination,services", "tla": "false"}},
		Fleet:    config.View{"store": "host-a", "coordinator": "host-a", "redis_port": "6379", "pg_dsn": "postgres://nova@host-a:5432/config", "loops_dir": "/srv/loops"},
		Loops:    map[string]config.View{"l1": loop},
		Revs:     map[string]int64{config.KindFleet: 1, config.KindMachine: 1, config.KindLoop: 1},
	}
}
