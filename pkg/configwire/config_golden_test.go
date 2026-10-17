package configwire

// TestConfigRowsGolden is the golden test of the config definitions
// (schemas/config): every table of the area round-trips as the binary wire and
// as padded standard base64 of that wire (and as the schema JSON text form),
// each read strictly through pkg/schemaread; a value past each declared bound
// and a damaged image of each form are refused by name, never clamped; the
// bounds the code enforces today (pkg/config) are the bounds the tables
// declare; and the live rows, changes, beats and inventory of pkg/config
// round-trip through their tables.
//
// The test is unit tier: no clock, no socket, no program, no store. The wire of
// each sample is pinned by its SHA-256 below; the pin moves only when the
// schema changes, and the schema change moves schemas/config/schema.lock with
// it (the owner's rule S1: a fixed table stands on its lock).
//
// How a bound is probed (the owner's rule S2, refuse by name, never clamp):
//
//   - write side: the generated measure refuses an over-bound value, and the
//     reader's write refuses it before a byte is written (clamped, with the
//     field's path);
//   - read side: an integer or enum field is patched in a valid wire to one past
//     its bound (a binary image and its base64), and a string or an array in the
//     JSON text form is lengthened by one; the strict read refuses each as
//     clamped. The binary image of an over-bound string or array cannot be
//     written without the compiler (the writer refuses to), so the JSON text
//     stands in for it.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const configHelp = "nova-config help"

// wirePins is the SHA-256 of the binary wire of each table's sample. A pin
// that moves is a change of the wire: read schemas/config/schema.lock first.
var wirePins = map[string]string{
	"FieldPair":     "59ec5e459a5a3377c269748d98e140640f3a5461d331bfc05e45872eb0b15406",
	"RowFields":     "647cb1d7faf107cced57aefe3a0567256f69827ed5f7eab3ebcfb21599cf8b90",
	"ConfigRow":     "6786bc07b0ccd60373ae1d191a98a81498fd264a39d3794c7463f460197c8c1f",
	"ConfigChange":  "e69ca43f301434c866c94935985cfd67797f98e44c9d73b48aebeb6e39cc9a39",
	"FileHeader":    "c5452e2d4faf77df5075a9bd48bf45034dfc3b56eccd727494dd79d4a2cac554",
	"CanonicalWord": "b06b0463c43169dbad127b595d81e5c0a99c4172fb0304b759d2d5bb152dbafb",
	"MachineView":   "8222283aba4b227f5c1905584e4f1b35da9eb090feeb2a4b7b63a3697c5734dd",
	"HostName":      "d34eadb68d2b9a0a360745151d917044841806588a1bdd713472e9e883db76e7",
	"EnvName":       "23f58cf1b88876c7e39d5bb6adc6e2431a2ae0a86ecc496135f4e2ebce81bf97",
	"InventoryLoop": "cf1fd316831ae5842ba36963544eee7384c113c8aa00b79262c1d42b6494eee1",
	"HostView":      "ba9509014a35642b8d78d76b5923284b0530f3d982fa69cd09e9e7517a8a890a",
	"HostGroup":     "eb29166e44c1ca320a1d2656f928b34b61ebf33f5753e5586dc157bf2a70012a",
	"KindRev":       "0c43208b58796410cbd73d20c3df86c4b599ae8be84dd14abc85b60fb2ce5bd5",
	"Inventory":     "d6398656d76dae005b7d86fb906dd99584c5f43d57de6dd6bf2a13cf28e66065",
	"BenchBeat":     "f53027e39a737e94cda9b8be21058c5f3ce433c46de86132a3ecce70d3481b07",
	"MemberBeat":    "8f068e4810f770546de7a1bcb3e25e693016ef97b3c7510c8226458e7e11d70f",
	"MachineBeat":   "9f99b37f396f18f7ca7b4a8ee643e73c29f01f20e6d3615e5434e889da106f6e",
}

// ---- samples -----------------------------------------------------------

func put(dst []byte, n *int32, s string) {
	if len(s) > len(dst) {
		panic("sample past its bound: " + s)
	}
	*n = int32(copy(dst, s))
}

// rowFieldsFrom is how a row's fields enter the table: in name order, the
// argv in its own slot.
func rowFieldsFrom(fields map[string]string) RowFields {
	var r RowFields
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

// fieldsOf is the inverse: the map a store holds.
func fieldsOf(r *RowFields) map[string]string {
	out := map[string]string{}
	for i := int32(0); i < r.PairsCount; i++ {
		p := &r.Pairs[i]
		out[string(p.Name[:p.NameLength])] = string(p.Value[:p.ValueLength])
	}
	if r.ArgvLength > 0 {
		out["argv"] = string(r.Argv[:r.ArgvLength])
	}
	return out
}

func sampleFieldPair() FieldPair {
	var v FieldPair
	put(v.Name[:], &v.NameLength, "note")
	put(v.Value[:], &v.ValueLength, "one short line")
	return v
}

func machineFields() map[string]string {
	return map[string]string{"user": "nova", "seat": "hetzner2", "slots": "8", "runners": "2", "roles": "bench,runner", "tla": "true", "width": "4"}
}

func loopFields() map[string]string {
	return map[string]string{"machine": "hetzner2", "argv": `["/usr/local/bin/nova-sprint","loop","--once"]`, "seat": "hetzner2", "keys": "GH_TOKEN,REDIS_PASSWORD", "every": "60", "keepalive": "false", "enabled": "true"}
}

func sampleConfigRow() ConfigRow {
	var v ConfigRow
	v.Kind = ConfigKindMachine
	put(v.Name[:], &v.NameLength, "hetzner2")
	v.Fields = rowFieldsFrom(machineFields())
	put(v.CreatedAt[:], &v.CreatedAtLength, "2026-10-01T12:00:00Z")
	put(v.UpdatedAt[:], &v.UpdatedAtLength, "2026-10-16T04:07:01Z")
	return v
}

func sampleConfigChange() ConfigChange {
	var v ConfigChange
	v.Id = 4217
	v.Kind = ConfigKindLoop
	put(v.Name[:], &v.NameLength, "sprint-loop")
	v.Op = ChangeOpSet
	v.HasBefore, v.HasAfter = true, true
	v.Before = rowFieldsFrom(loopFields())
	after := loopFields()
	after["every"] = "120"
	v.After = rowFieldsFrom(after)
	put(v.Actor[:], &v.ActorLength, "rowan")
	put(v.Reason[:], &v.ReasonLength, "slow the loop while the store moves")
	put(v.At[:], &v.AtLength, "2026-10-16T04:07:01Z")
	return v
}

func sampleMachineView() MachineView {
	var v MachineView
	put(v.Name[:], &v.NameLength, "hetzner2")
	put(v.User[:], &v.UserLength, "nova")
	put(v.Seat[:], &v.SeatLength, "hetzner2")
	v.Slots, v.Runners = 8, 2
	put(v.Roles[:], &v.RolesLength, "bench,runner")
	v.Tla = true
	v.Width, v.WidthPresent = 4, true
	return v
}

func sampleInventoryLoop() InventoryLoop {
	var v InventoryLoop
	put(v.Machine[:], &v.MachineLength, "hetzner2")
	put(v.Name[:], &v.NameLength, "sprint-loop")
	put(v.Argv[:], &v.ArgvLength, loopFields()["argv"])
	put(v.Seat[:], &v.SeatLength, "hetzner2")
	for i, k := range []string{"GH_TOKEN", "REDIS_PASSWORD"} {
		put(v.Keys[i].Name[:], &v.Keys[i].NameLength, k)
		v.KeysCount++
	}
	v.Every, v.Enabled = 60, true
	put(v.Log[:], &v.LogLength, "/home/nova/nova-bench/loops/sprint-loop.log")
	return v
}

func sampleHostView() HostView {
	var v HostView
	put(v.AnsibleHost[:], &v.AnsibleHostLength, "hetzner2")
	v.Kind = ConfigKindMachine
	v.Slots, v.SlotsCapacity, v.Runners = 8, 8, 2
	v.SlotsShare, v.SlotsSharePresent = 4, true
	v.Cores, v.CoresPresent = 16, true
	put(v.BenchOs[:], &v.BenchOsLength, "linux")
	put(v.NovaArch[:], &v.NovaArchLength, "amd64")
	v.NovaTla = true
	put(v.RegistrySeat[:], &v.RegistrySeatLength, "hetzner2")
	put(v.RegistryRoles[:], &v.RegistryRolesLength, "bench,runner")
	put(v.AnsibleUser[:], &v.AnsibleUserLength, "nova")
	put(v.NovaSeat[:], &v.NovaSeatLength, "hetzner2")
	put(v.NovaRedisAddr[:], &v.NovaRedisAddrLength, "studio:6379")
	put(v.NovaBusAddr[:], &v.NovaBusAddrLength, "studio:6381")
	v.NovaRedisPort = 6379
	put(v.NovaPgDsn[:], &v.NovaPgDsnLength, "postgres://nova@studio:5432/config")
	v.Local = true
	return v
}

func hostGroup(names ...string) HostGroup {
	var g HostGroup
	for i, n := range names {
		put(g.Hosts[i].Name[:], &g.Hosts[i].NameLength, n)
		g.HostsCount++
	}
	return g
}

func sampleInventory() Inventory {
	var v Inventory
	v.All = hostGroup("hetzner1", "hetzner2", "studio")
	v.Adopt = hostGroup("studio", "hetzner1", "hetzner2")
	v.Benches = hostGroup("hetzner1", "hetzner2")
	v.Coordinator = hostGroup("studio")
	v.Store = hostGroup("studio")
	v.StoreDeployer = hostGroup("studio")
	v.Runners = hostGroup("hetzner2")
	v.Tla = hostGroup()
	put(v.NovaStore[:], &v.NovaStoreLength, "studio")
	for i, k := range []ConfigKind{ConfigKindMachine, ConfigKindFleet, ConfigKindLoop} {
		v.Revs[i] = KindRev{Kind: k, Rev: int64(10 * (i + 1))}
		v.RevsCount++
	}
	return v
}

func sampleBenchBeat() BenchBeat {
	var v BenchBeat
	put(v.Host[:], &v.HostLength, "hetzner2")
	v.AtMs = 1792123621000
	put(v.Load1[:], &v.Load1Length, "0.24")
	put(v.Ncpu[:], &v.NcpuLength, "16")
	put(v.Cpu[:], &v.CpuLength, "12.5")
	put(v.Os[:], &v.OsLength, "linux")
	put(v.Arch[:], &v.ArchLength, "amd64")
	put(v.MemoryGb[:], &v.MemoryGbLength, "64")
	return v
}

func sampleMachineBeat() MachineBeat {
	var v MachineBeat
	put(v.Os[:], &v.OsLength, "linux")
	put(v.Arch[:], &v.ArchLength, "amd64")
	put(v.Cores[:], &v.CoresLength, "16")
	put(v.MemoryGb[:], &v.MemoryGbLength, "64")
	put(v.At[:], &v.AtLength, "2026-10-16T04:07:01Z")
	return v
}

// ---- the strict codec over the generated functions --------------------

// table is one table's generated functions and its sample.
type table[T any] struct {
	name   string
	sample func() T
	fields []TableFieldInfo
	reset  func(*T)
	// the generated writers and readers
	measureReason func(*T) (int64, error)
	save          func(*T, []byte) int64
	load          func(*T, []byte, *TableReport) bool
	fromJSON      func(*T, []byte, *TableReport) bool
	toJSONMeasure func(*T) int64
	toJSON        func(*T, []byte) int64
}

func (tb *table[T]) codec() *schemaread.Codec[T] {
	rep := func(ok bool, r TableReport) schemaread.Report {
		out := schemaread.Report{Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped), Malformed: r.Malformed || !ok}
		if r.Verdict == TableOpenRefused || r.Verdict == TableOpenDamaged {
			out.Reason = r.Err().Error()
		}
		return out
	}
	return &schemaread.Codec[T]{
		Name:  strings.ToUpper(tb.name),
		Help:  configHelp,
		Reset: tb.reset,
		LoadWire: func(v *T, wire []byte) schemaread.Report {
			var r TableReport
			ok := tb.load(v, wire, &r)
			return rep(ok, r)
		},
		LoadJSON: func(v *T, text []byte) schemaread.Report {
			var r TableReport
			ok := tb.fromJSON(v, text, &r)
			return rep(ok, r)
		},
		// The facade's constraints (UNION.md section 4.6): every bound the
		// definition declares, from the value's own storage, so a value past one
		// is refused on the read and before the write.
		Constraints: func(v *T, c *schemaread.Checker) { constrain(reflect.ValueOf(v).Elem(), "", c) },
		SaveWire:    tb.wire,
		SaveJSON: func(v *T) []byte {
			n := tb.toJSONMeasure(v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if tb.toJSON(v, buf) != n {
				return nil
			}
			return buf
		},
	}
}

// wire is the generated Measure and Save, unchecked: the reader's write checks
// before it calls this.
func (tb *table[T]) wire(v *T) []byte {
	n, err := tb.measureReason(v)
	if err != nil || n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if tb.save(v, buf) != n {
		return nil
	}
	return buf
}

// constrain refuses a string or an array past its declared bound, found by
// the generated storage's shape: a [N]byte beside its Length is string(N); a
// [N]T beside its Count is [..N]T. The integer and enum bounds are the
// descriptor's (checkRoot).
func constrain(v reflect.Value, path string, c *schemaread.Checker) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f, fv := t.Field(i), v.Field(i)
		name := path + snake(f.Name)
		if f.Type.Kind() != reflect.Array {
			if f.Type.Kind() == reflect.Struct {
				constrain(fv, name+".", c)
			}
			continue
		}
		companion := v.FieldByName(f.Name + "Length")
		if f.Type.Elem().Kind() == reflect.Uint8 && companion.IsValid() {
			n := int(companion.Int())
			raw := fv.Slice(0, fv.Len()).Bytes()
			s := string(raw)
			if n >= 0 && n <= len(raw) {
				s = string(raw[:n])
			} else if n > len(raw) {
				s += strings.Repeat("?", n-len(raw))
			}
			c.Text(name, s, fv.Len())
			continue
		}
		if companion = v.FieldByName(f.Name + "Count"); companion.IsValid() {
			n := int(companion.Int())
			c.Bound(name, int64(n), 0, int64(fv.Len()))
			for j := 0; j < n && j < fv.Len(); j++ {
				constrain(fv.Index(j), fmt.Sprintf("%s[%d].", name, j), c)
			}
		}
	}
}

// checkRoot is the integer and enum bounds of the table's own fields.
func checkRoot(v reflect.Value, fields []TableFieldInfo, c *schemaread.Checker) {
	for _, f := range fields {
		fv := v.FieldByName(pascal(f.Name))
		if !fv.IsValid() || f.IsArray {
			continue
		}
		if f.Optional {
			if p := v.FieldByName(pascal(f.Name) + "Present"); p.IsValid() && !p.Bool() {
				continue
			}
		}
		switch {
		case f.HasRange:
			x := fv.Int()
			c.Bound(f.Name, x, int64(f.RangeMin), int64(f.RangeMax))
		case f.EnumMax >= 0:
			// An enum is on the wire as the hash of its variant\'s name, and a
			// word this reader does not know reads as None and is counted, never
			// refused by the load: None is no word of any table here, so the
			// reader refuses it (absent_required) and the writer never writes it.
			c.Bound(f.Name, int64(fv.Uint()), 0, f.EnumMax)
			c.Required(f.Name, fv.Uint() != 0)
		}
	}
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func pascal(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r == '_' {
			up = true
			continue
		}
		if up {
			r = unicode.ToUpper(r)
			up = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// reader is the strict reader with the root bounds added to its constraints.
func (tb *table[T]) reader() *schemaread.Reader[T] {
	c := tb.codec()
	inner := c.Constraints
	c.Constraints = func(v *T, ck *schemaread.Checker) {
		inner(v, ck)
		checkRoot(reflect.ValueOf(v).Elem(), tb.fields, ck)
	}
	return schemaread.NewReader(c)
}

// ---- the probes ----------------------------------------------------------

// maxed fills every string and every array of v to its declared bound, and
// every bounded integer and enum of the root to its declared maximum.
func maxed(v reflect.Value, root []TableFieldInfo) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f, fv := t.Field(i), v.Field(i)
		switch {
		case f.Type.Kind() == reflect.Struct:
			maxed(fv, nil)
		case f.Type.Kind() == reflect.Array:
			if l := v.FieldByName(f.Name + "Length"); f.Type.Elem().Kind() == reflect.Uint8 && l.IsValid() {
				b := fv.Slice(0, fv.Len()).Bytes()
				for j := range b {
					b[j] = 'm'
				}
				l.SetInt(int64(fv.Len()))
			} else if n := v.FieldByName(f.Name + "Count"); n.IsValid() {
				n.SetInt(int64(fv.Len()))
				for j := 0; j < fv.Len(); j++ {
					if fv.Index(j).Kind() == reflect.Struct {
						maxed(fv.Index(j), nil)
					}
				}
			}
		}
	}
	for _, f := range root {
		fv := v.FieldByName(pascal(f.Name))
		if !fv.IsValid() || f.IsArray {
			continue
		}
		if p := v.FieldByName(pascal(f.Name) + "Present"); p.IsValid() {
			p.SetBool(true)
		}
		switch {
		case f.HasRange:
			fv.SetInt(int64(f.RangeMax))
		case f.EnumMax >= 0:
			fv.SetUint(uint64(f.EnumMax))
		}
	}
}

// step is one move down the Go storage: a field by name, then an element.
type step struct {
	name  string
	index int // -1 for the field itself
}

// probe is one bound of one table: where it is and how to go one past it.
type probe struct {
	path  string
	steps []step
	kind  string // string or array
	bound int64
	json  []string // the JSON path to the value
}

// over sets the probe's value one past its bound in root.
func (p probe) over(root reflect.Value) {
	v := root
	for i, s := range p.steps {
		if i == len(p.steps)-1 {
			suffix := "Length"
			if p.kind == "array" {
				suffix = "Count"
			}
			v.FieldByName(s.name + suffix).SetInt(p.bound + 1)
			return
		}
		v = v.FieldByName(s.name)
		if s.index >= 0 {
			v = v.Index(s.index)
		}
	}
}

// probes lists the string and array bounds of v's storage, nested ones too.
func probes(v reflect.Value, path string, steps []step, jsonPath []string, out *[]probe) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f, fv := t.Field(i), v.Field(i)
		name := snake(f.Name)
		here := append(append([]step{}, steps...), step{f.Name, -1})
		jp := append(append([]string{}, jsonPath...), name)
		switch {
		case f.Type.Kind() == reflect.Struct:
			probes(fv, path+name+".", here, jp, out)
		case f.Type.Kind() == reflect.Array:
			if l := v.FieldByName(f.Name + "Length"); f.Type.Elem().Kind() == reflect.Uint8 && l.IsValid() {
				*out = append(*out, probe{path: path + name, steps: here, kind: "string", bound: int64(fv.Len()), json: jp})
			} else if n := v.FieldByName(f.Name + "Count"); n.IsValid() {
				*out = append(*out, probe{path: path + name, steps: here, kind: "array", bound: int64(fv.Len()), json: jp})
				if fv.Index(0).Kind() == reflect.Struct {
					elem := append(append([]step{}, steps...), step{f.Name, 0})
					probes(fv.Index(0), path+name+"[0].", elem, append(append([]string{}, jp...), "0"), out)
				}
			}
		}
	}
}

// patchInt returns wire with the field at its bound changed to one past it
// (above = true) or one below its minimum, found by writing the bound and its
// neighbour and reading where the two wires differ: the field is stored as its
// little-endian value after its kind byte.
func patchInt(t *testing.T, size int, wireAt, wireNext []byte, to int64) []byte {
	t.Helper()
	require.Equal(t, len(wireAt), len(wireNext))
	p := -1
	for i := range wireAt {
		if wireAt[i] != wireNext[i] {
			p = i
			break
		}
	}
	require.GreaterOrEqual(t, p, 0, "the bound and its neighbour write the same wire")
	out := append([]byte(nil), wireAt...)
	for i := 0; i < size; i++ {
		out[p+i] = byte(uint64(to) >> (8 * i))
	}
	return out
}

func readRefusal(t *testing.T, err error, name string) *schemaread.Refusal {
	t.Helper()
	require.Error(t, err)
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), strings.ToUpper(name)+" REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+configHelp), err.Error())
	return r
}

// run is the whole proof for one table.
func (tb *table[T]) run(t *testing.T) {
	t.Run(tb.name, func(t *testing.T) {
		t.Parallel()
		r := tb.reader()
		sample := tb.sample()
		tb.reset(new(T))

		maxv := tb.sample()
		maxed(reflect.ValueOf(&maxv).Elem(), tb.fields)

		// 1. every form round-trips, strictly, at the sample and at every bound
		for label, v := range map[string]T{"sample": sample, "bounds": maxv} {
			v := v
			for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
				in, err := r.Write(&v, form)
				require.NoError(t, err, "%s %s write", label, form)
				require.NotEmpty(t, in)
				got, res, err := r.Read(in)
				require.NoError(t, err, "%s %s read", label, form)
				assert.Equal(t, form, res.Form)
				assert.Equal(t, 0, res.Unknown)
				assert.Equal(t, v, got, "%s %s round trip", label, form)
			}
		}
		counts := r.Counts()
		assert.Equal(t, uint64(2), counts.Reads(schemaread.FormBinary), "the per-form counters count each read once")
		assert.Equal(t, uint64(2), counts.Reads(schemaread.FormBase64))
		assert.Equal(t, uint64(2), counts.Reads(schemaread.FormJSON))

		// 2. the wire is deterministic and pinned; base64 is the wire, padded
		wire := tb.wire(&sample)
		require.NotNil(t, wire)
		assert.Equal(t, wire, tb.wire(&sample), "the same value writes the same wire")
		sum := sha256.Sum256(wire)
		assert.Equal(t, wirePins[tb.name], hex.EncodeToString(sum[:]), "the sample's wire is pinned: %s", tb.name)
		text, err := r.Write(&sample, schemaread.FormBase64)
		require.NoError(t, err)
		assert.Equal(t, base64.StdEncoding.EncodeToString(wire), string(text))
		assert.Equal(t, byte(1), wire[0], "the form byte leads the wire")

		// 3. a value past a string or array bound is refused before a byte is
		// written, and the read of an over-bound text is refused by name
		var ps []probe
		probes(reflect.ValueOf(&maxv).Elem(), "", nil, nil, &ps)
		for _, p := range ps {
			p := p
			over := maxv
			p.over(reflect.ValueOf(&over).Elem())
			// the generated measure refuses the over-bound value by name
			_, err := tb.measureReason(&over)
			assert.Error(t, err, "%s: the generated measure accepts %s past its bound", tb.name, p.path)
			out, werr := r.Write(&over, schemaread.FormBinary)
			assert.Nil(t, out, "nothing is written past a bound: %s", p.path)
			ref := readRefusal(t, werr, tb.name)
			assert.Equal(t, schemaread.ReasonClamped, ref.Reason, p.path)
		}
		tb.overBoundText(t, r, maxv, ps)

		// 4. an integer or enum past its bound: refused on the write, and the
		// binary wire and its base64 patched to one past the bound are refused
		tb.overBoundNumbers(t, r)

		// 5. a damaged image of each form is refused by name
		tb.damaged(t, r, wire)
	})
}

// overBoundText lengthens each string and array in the JSON text form by one.
func (tb *table[T]) overBoundText(t *testing.T, r *schemaread.Reader[T], maxv T, ps []probe) {
	t.Helper()
	text, err := r.Write(&maxv, schemaread.FormJSON)
	require.NoError(t, err)
	for _, p := range ps {
		doc := decode(t, text)
		node := doc
		for i, key := range p.json {
			if key == "0" {
				node = node.([]any)[0]
				continue
			}
			m := node.(map[string]any)
			if i < len(p.json)-1 {
				node = m[key]
				continue
			}
			switch p.kind {
			case "string":
				m[key] = strings.Repeat("m", int(p.bound)+1)
			case "array":
				arr := m[key].([]any)
				m[key] = append(arr, arr[len(arr)-1])
			}
		}
		raw, err := json.Marshal(doc)
		require.NoError(t, err)
		_, _, err = r.Read(raw)
		ref := readRefusal(t, err, tb.name)
		assert.Equal(t, schemaread.ReasonClamped, ref.Reason, "%s past its bound in the text form", p.path)
	}
}

func decode(t *testing.T, text []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	var doc any
	require.NoError(t, dec.Decode(&doc))
	return doc
}

func (tb *table[T]) overBoundNumbers(t *testing.T, r *schemaread.Reader[T]) {
	t.Helper()
	for _, f := range tb.fields {
		if f.IsArray || (!f.HasRange && f.EnumMax < 0) {
			continue
		}
		f := f
		var lo, hi int64
		var size int
		switch {
		case f.HasRange:
			lo, hi = int64(f.RangeMin), int64(f.RangeMax)
		default:
			lo, hi = 0, f.EnumMax
		}
		set := func(v *T, x int64) {
			fv := reflect.ValueOf(v).Elem().FieldByName(pascal(f.Name))
			size = int(fv.Type().Size())
			if p := reflect.ValueOf(v).Elem().FieldByName(pascal(f.Name) + "Present"); p.IsValid() {
				p.SetBool(true)
			}
			switch fv.Kind() {
			case reflect.Int32, reflect.Int64:
				fv.SetInt(x)
			default:
				fv.SetUint(uint64(x))
			}
		}
		// write side: one past the maximum is refused before a byte is written
		over := tb.sample()
		set(&over, hi+1)
		out, err := r.Write(&over, schemaread.FormBinary)
		assert.Nil(t, out, "%s past its bound writes nothing", f.Name)
		assert.Equal(t, schemaread.ReasonClamped, readRefusal(t, err, tb.name).Reason, f.Name)
		// read side: the wire at the maximum and its neighbour locate the field
		at, next := tb.sample(), tb.sample()
		set(&at, hi)
		set(&next, hi-1)
		wireAt, wireNext := tb.wire(&at), tb.wire(&next)
		require.NotNil(t, wireAt)
		require.NotNil(t, wireNext)
		if f.EnumMax >= 0 {
			// read side: the variant id of the last word, replaced by an id no word has
			id := make([]byte, 8)
			for i := range id {
				id[i] = byte(f.VariantId(uint64(hi)) >> (8 * i))
			}
			require.Equal(t, 1, bytes.Count(wireAt, id), "%s: the variant id occurs once", f.Name)
			image := bytes.Replace(wireAt, id, bytes.Repeat([]byte{0xaa}, 8), 1)
			for _, in := range [][]byte{image, []byte(base64.StdEncoding.EncodeToString(image))} {
				_, _, err := r.Read(in)
				assert.Equal(t, schemaread.ReasonAbsentRequired, readRefusal(t, err, tb.name).Reason, "%s: a word no reader knows", f.Name)
			}
			continue
		}
		bad := []int64{hi + 1}
		if f.HasRange && lo > -1<<31 {
			bad = append(bad, lo-1)
		}
		for _, x := range bad {
			image := patchInt(t, size, wireAt, wireNext, x)
			for _, in := range [][]byte{image, []byte(base64.StdEncoding.EncodeToString(image))} {
				_, _, err := r.Read(in)
				assert.Equal(t, schemaread.ReasonClamped, readRefusal(t, err, tb.name).Reason, "%s patched to %d", f.Name, x)
			}
		}
		_ = lo
	}
}

// damaged: the binary wire cut short or with a form it does not know, and its
// base64 broken in each way a text transport breaks it.
func (tb *table[T]) damaged(t *testing.T, r *schemaread.Reader[T], wire []byte) {
	t.Helper()
	known := map[string]bool{
		schemaread.ReasonMalformed: true, schemaread.ReasonUnknownForm: true, schemaread.ReasonWireDamaged: true,
		schemaread.ReasonClamped: true, schemaread.ReasonKindMismatch: true,
	}
	refused := func(label string, in []byte) {
		_, _, err := r.Read(in)
		ref := readRefusal(t, err, tb.name)
		assert.True(t, known[ref.Reason] || strings.Contains(ref.Reason, "_"), "%s: the refusal %q is a name", label, ref.Reason)
		assert.NotEmpty(t, ref.Reason, label)
	}
	b64 := base64.StdEncoding.EncodeToString(wire)
	for _, n := range []int{1, 2, 3, len(wire) / 2, len(wire) - 1} {
		refused(fmt.Sprintf("binary cut to %d", n), wire[:n])
		cut := wire[:n]
		refused(fmt.Sprintf("base64 of the wire cut to %d", n), []byte(base64.StdEncoding.EncodeToString(cut)))
	}
	unknown := append([]byte{0x7e}, wire[1:]...)
	refused("binary with an unknown form byte", unknown)
	refused("base64 with an unknown form byte", []byte(base64.StdEncoding.EncodeToString(unknown)))
	refused("base64 with a character outside the alphabet", []byte("!"+b64[1:]))
	refused("base64 cut mid-quantum", []byte(b64[:len(b64)-1]))
	if strings.HasSuffix(b64, "=") {
		refused("base64 without its padding", []byte(strings.TrimRight(b64, "=")))
	}
	refused("base64 of nothing but padding", []byte("===="))
	refused("text that is no form", []byte("%"))
	refused("empty", []byte{})
	refused("json that is not the table", []byte(`{"unknown_only": 1, "x": [`))
}

// ---- the cases ---------------------------------------------------------

type runner interface{ run(t *testing.T) }

func cases() []runner {
	return []runner{
		&table[FieldPair]{name: "FieldPair", sample: sampleFieldPair, fields: FieldPairTableFields, reset: FieldPairReset,
			measureReason: FieldPairMeasureReason, save: FieldPairSave, load: FieldPairLoad, fromJSON: FieldPairFromJson, toJSONMeasure: FieldPairToJsonMeasure, toJSON: FieldPairToJson},
		&table[RowFields]{name: "RowFields", sample: func() RowFields { return rowFieldsFrom(loopFields()) }, fields: RowFieldsTableFields, reset: RowFieldsReset,
			measureReason: RowFieldsMeasureReason, save: RowFieldsSave, load: RowFieldsLoad, fromJSON: RowFieldsFromJson, toJSONMeasure: RowFieldsToJsonMeasure, toJSON: RowFieldsToJson},
		&table[ConfigRow]{name: "ConfigRow", sample: sampleConfigRow, fields: ConfigRowTableFields, reset: ConfigRowReset,
			measureReason: ConfigRowMeasureReason, save: ConfigRowSave, load: ConfigRowLoad, fromJSON: ConfigRowFromJson, toJSONMeasure: ConfigRowToJsonMeasure, toJSON: ConfigRowToJson},
		&table[ConfigChange]{name: "ConfigChange", sample: sampleConfigChange, fields: ConfigChangeTableFields, reset: ConfigChangeReset,
			measureReason: ConfigChangeMeasureReason, save: ConfigChangeSave, load: ConfigChangeLoad, fromJSON: ConfigChangeFromJson, toJSONMeasure: ConfigChangeToJsonMeasure, toJSON: ConfigChangeToJson},
		&table[FileHeader]{name: "FileHeader", sample: func() FileHeader { return FileHeader{Schema: 46} }, fields: FileHeaderTableFields, reset: FileHeaderReset,
			measureReason: FileHeaderMeasureReason, save: FileHeaderSave, load: FileHeaderLoad, fromJSON: FileHeaderFromJson, toJSONMeasure: FileHeaderToJsonMeasure, toJSON: FileHeaderToJson},
		&table[CanonicalWord]{name: "CanonicalWord", sample: func() CanonicalWord {
			return CanonicalWord{Kind: ConfigKindRoute, FieldType: FieldTypeArgv, Op: ChangeOpRemove, Singleton: true}
		}, fields: CanonicalWordTableFields, reset: CanonicalWordReset,
			measureReason: CanonicalWordMeasureReason, save: CanonicalWordSave, load: CanonicalWordLoad, fromJSON: CanonicalWordFromJson, toJSONMeasure: CanonicalWordToJsonMeasure, toJSON: CanonicalWordToJson},
		&table[MachineView]{name: "MachineView", sample: sampleMachineView, fields: MachineViewTableFields, reset: MachineViewReset,
			measureReason: MachineViewMeasureReason, save: MachineViewSave, load: MachineViewLoad, fromJSON: MachineViewFromJson, toJSONMeasure: MachineViewToJsonMeasure, toJSON: MachineViewToJson},
		&table[HostName]{name: "HostName", sample: func() HostName { var v HostName; put(v.Name[:], &v.NameLength, "hetzner2"); return v }, fields: HostNameTableFields, reset: HostNameReset,
			measureReason: HostNameMeasureReason, save: HostNameSave, load: HostNameLoad, fromJSON: HostNameFromJson, toJSONMeasure: HostNameToJsonMeasure, toJSON: HostNameToJson},
		&table[EnvName]{name: "EnvName", sample: func() EnvName { var v EnvName; put(v.Name[:], &v.NameLength, "GH_TOKEN"); return v }, fields: EnvNameTableFields, reset: EnvNameReset,
			measureReason: EnvNameMeasureReason, save: EnvNameSave, load: EnvNameLoad, fromJSON: EnvNameFromJson, toJSONMeasure: EnvNameToJsonMeasure, toJSON: EnvNameToJson},
		&table[InventoryLoop]{name: "InventoryLoop", sample: sampleInventoryLoop, fields: InventoryLoopTableFields, reset: InventoryLoopReset,
			measureReason: InventoryLoopMeasureReason, save: InventoryLoopSave, load: InventoryLoopLoad, fromJSON: InventoryLoopFromJson, toJSONMeasure: InventoryLoopToJsonMeasure, toJSON: InventoryLoopToJson},
		&table[HostView]{name: "HostView", sample: sampleHostView, fields: HostViewTableFields, reset: HostViewReset,
			measureReason: HostViewMeasureReason, save: HostViewSave, load: HostViewLoad, fromJSON: HostViewFromJson, toJSONMeasure: HostViewToJsonMeasure, toJSON: HostViewToJson},
		&table[HostGroup]{name: "HostGroup", sample: func() HostGroup { return hostGroup("hetzner1", "hetzner2") }, fields: HostGroupTableFields, reset: HostGroupReset,
			measureReason: HostGroupMeasureReason, save: HostGroupSave, load: HostGroupLoad, fromJSON: HostGroupFromJson, toJSONMeasure: HostGroupToJsonMeasure, toJSON: HostGroupToJson},
		&table[KindRev]{name: "KindRev", sample: func() KindRev { return KindRev{Kind: ConfigKindFleet, Rev: 7} }, fields: KindRevTableFields, reset: KindRevReset,
			measureReason: KindRevMeasureReason, save: KindRevSave, load: KindRevLoad, fromJSON: KindRevFromJson, toJSONMeasure: KindRevToJsonMeasure, toJSON: KindRevToJson},
		&table[Inventory]{name: "Inventory", sample: sampleInventory, fields: InventoryTableFields, reset: InventoryReset,
			measureReason: InventoryMeasureReason, save: InventorySave, load: InventoryLoad, fromJSON: InventoryFromJson, toJSONMeasure: InventoryToJsonMeasure, toJSON: InventoryToJson},
		&table[BenchBeat]{name: "BenchBeat", sample: sampleBenchBeat, fields: BenchBeatTableFields, reset: BenchBeatReset,
			measureReason: BenchBeatMeasureReason, save: BenchBeatSave, load: BenchBeatLoad, fromJSON: BenchBeatFromJson, toJSONMeasure: BenchBeatToJsonMeasure, toJSON: BenchBeatToJson},
		&table[MemberBeat]{name: "MemberBeat", sample: func() MemberBeat { return MemberBeat{AtMs: 1792123621000, Cores: 16} }, fields: MemberBeatTableFields, reset: MemberBeatReset,
			measureReason: MemberBeatMeasureReason, save: MemberBeatSave, load: MemberBeatLoad, fromJSON: MemberBeatFromJson, toJSONMeasure: MemberBeatToJsonMeasure, toJSON: MemberBeatToJson},
		&table[MachineBeat]{name: "MachineBeat", sample: sampleMachineBeat, fields: MachineBeatTableFields, reset: MachineBeatReset,
			measureReason: MachineBeatMeasureReason, save: MachineBeatSave, load: MachineBeatLoad, fromJSON: MachineBeatFromJson, toJSONMeasure: MachineBeatToJsonMeasure, toJSON: MachineBeatToJson},
	}
}

func TestConfigRowsGolden(t *testing.T) {
	t.Parallel()

	for _, c := range cases() {
		c.run(t)
	}

	// The bounds the tables declare are the bounds the code enforces.
	t.Run("bounds are the code's", func(t *testing.T) {
		t.Parallel()
		assert.EqualValues(t, config.MaxArgvBytes, MaxArgvBytes, "pkg/config MaxArgvBytes")
		assert.Equal(t, int32(MaxArgvBytes), fieldInfo(t, RowFieldsTableFields, "argv").ArrayBound)
		assert.Equal(t, int32(MaxArgvBytes), fieldInfo(t, InventoryLoopTableFields, "argv").ArrayBound)
		widest, longest := 0, 0
		for _, k := range config.Kinds {
			widest = max(widest, len(k.Fields))
			for _, f := range k.Fields {
				longest = max(longest, len(f.Name))
			}
		}
		assert.GreaterOrEqual(t, MaxRowFields, widest, "a row of the widest kind fits")
		assert.GreaterOrEqual(t, MaxFieldName, longest, "the longest field name fits")
		assert.Equal(t, int32(MaxRowFields), fieldInfo(t, RowFieldsTableFields, "pairs").ArrayBound)
		assert.Less(t, ConfigChangeCookMeasure(new(ConfigChange)), int64(65536), "a change holds two rows under the record limit")
		// a name the store accepts fits
		assert.LessOrEqual(t, len(strings.Repeat("a", MaxName)), MaxName)
	})

	// The canonical kind, op and field type words are the code's, in order.
	t.Run("canonical words", func(t *testing.T) {
		t.Parallel()
		var kinds []string
		for k := ConfigKindMachine; k <= ConfigKindMax; k++ {
			kinds = append(kinds, strings.ToLower(EnumNameConfigKind(k)))
		}
		assert.Equal(t, config.KindNames(), kinds, "the kind words, in apply order")
		var ops []string
		for o := ChangeOpAdd; o <= ChangeOpMax; o++ {
			ops = append(ops, strings.ToLower(EnumNameChangeOp(o)))
		}
		assert.Equal(t, []string{config.OpAdd, config.OpSet, config.OpRemove}, ops)
		var types []string
		for f := FieldTypeText; f <= FieldTypeMax; f++ {
			types = append(types, strings.ToLower(EnumNameFieldType(f)))
		}
		assert.Equal(t, []string{string(config.TypeText), string(config.TypeInt), string(config.TypeEnum), string(config.TypeList),
			string(config.TypeNames), string(config.TypeRef), string(config.TypeBool), string(config.TypeKeys),
			string(config.TypeArgv), string(config.TypeSeq), string(config.TypeDecimal)}, types)
		// every field type any kind uses is a word of the table
		used := map[string]bool{}
		for _, w := range types {
			used[w] = true
		}
		for _, k := range config.Kinds {
			for _, f := range k.Fields {
				assert.True(t, used[string(f.Type)], "kind %s field %s type %s", k.Name, f.Name, f.Type)
			}
		}
	})

	// The live rows of every kind round-trip as ConfigRow and as the change
	// that adds them, removes them, or sets them.
	t.Run("rows and changes of every kind", func(t *testing.T) {
		t.Parallel()
		rows := (&table[ConfigRow]{name: "ConfigRow", fields: ConfigRowTableFields}).bare(ConfigRowReset, ConfigRowMeasureReason, ConfigRowSave, ConfigRowLoad)
		for i, k := range config.Kinds {
			fields := map[string]string{}
			for _, f := range k.Fields {
				fields[f.Name] = "x"
			}
			if _, ok := fields["argv"]; ok {
				fields["argv"] = `["/bin/true"]`
			}
			row := config.Row{Name: "a-" + k.Name, Fields: fields, CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-02T00:00:00Z"}
			var v ConfigRow
			v.Kind = ConfigKind(i + 1)
			put(v.Name[:], &v.NameLength, row.Name)
			v.Fields = rowFieldsFrom(row.Fields)
			put(v.CreatedAt[:], &v.CreatedAtLength, row.CreatedAt)
			put(v.UpdatedAt[:], &v.UpdatedAtLength, row.UpdatedAt)
			in, err := rows.Write(&v, schemaread.FormBinary)
			require.NoError(t, err, k.Name)
			got, _, err := rows.Read(in)
			require.NoError(t, err, k.Name)
			assert.Equal(t, strings.ToLower(EnumNameConfigKind(got.Kind)), k.Name)
			assert.Equal(t, row.Fields, fieldsOf(&got.Fields), "the fields of a %s row come back as the store holds them", k.Name)
		}
		changes := (&table[ConfigChange]{name: "ConfigChange", fields: ConfigChangeTableFields}).bare(ConfigChangeReset, ConfigChangeMeasureReason, ConfigChangeSave, ConfigChangeLoad)
		for _, op := range []string{config.OpAdd, config.OpSet, config.OpRemove} {
			var v ConfigChange
			v.Id, v.Kind = 9, ConfigKindTier
			put(v.Name[:], &v.NameLength, "tiers")
			v.Op = map[string]ChangeOp{config.OpAdd: ChangeOpAdd, config.OpSet: ChangeOpSet, config.OpRemove: ChangeOpRemove}[op]
			if op != config.OpAdd {
				v.HasBefore, v.Before = true, rowFieldsFrom(map[string]string{"note": "before"})
			}
			if op != config.OpRemove {
				v.HasAfter, v.After = true, rowFieldsFrom(map[string]string{"note": "after"})
			}
			put(v.Actor[:], &v.ActorLength, "rowan")
			in, err := changes.Write(&v, schemaread.FormBase64)
			require.NoError(t, err, op)
			got, _, err := changes.Read(in)
			require.NoError(t, err, op)
			assert.Equal(t, v, got, op)
			assert.Equal(t, op != config.OpAdd, got.HasBefore, "an add has no before")
			assert.Equal(t, op != config.OpRemove, got.HasAfter, "a remove has no after")
		}
	})

	// The inventory the code builds, written as the tables and read back.
	t.Run("inventory and beats", func(t *testing.T) {
		t.Parallel()
		snap := &config.Snapshot{
			Machines: map[string]config.View{
				"studio":   {"user": "rowan", "seat": "studio", "slots": "4", "runners": "0", "roles": "bench,coordination,services", "tla": "false"},
				"hetzner2": {"user": "nova", "seat": "hetzner2", "slots": "8", "runners": "2", "roles": "bench,runner", "tla": "true", "width": "4"},
			},
			Fleet: config.View{"store": "studio", "coordinator": "studio", "redis_port": "6379", "pg_dsn": "postgres://nova@studio:5432/config", "loops_dir": "/home/nova/loops", "bus": "studio:6381"},
			Beats: map[string]*config.Beat{"hetzner2": {OS: "linux", Arch: "amd64", Cores: "16", MemoryGB: "64", At: "2026-10-16T04:07:01Z"}},
			Revs:  map[string]int64{config.KindMachine: 3, config.KindFleet: 2},
		}
		inv, err := config.BuildInventory(snap, "hetzner2")
		require.NoError(t, err)

		var v Inventory
		for g, hosts := range map[*HostGroup][]string{&v.All: inv.All.Hosts, &v.Adopt: inv.Adopt.Hosts, &v.Benches: inv.Benches.Hosts,
			&v.Coordinator: inv.Coordinator.Hosts, &v.Store: inv.Store.Hosts, &v.StoreDeployer: inv.StoreDeployer.Hosts,
			&v.Runners: inv.Runners.Hosts, &v.Tla: inv.TLA.Hosts} {
			*g = hostGroup(hosts...)
		}
		put(v.NovaStore[:], &v.NovaStoreLength, inv.All.Vars["nova_store"].(string))
		for i, k := range config.KindNames() {
			if rev, ok := inv.All.Vars["nova_config_rev"].(map[string]int64)[k]; ok && rev > 0 {
				v.Revs[v.RevsCount] = KindRev{Kind: ConfigKind(i + 1), Rev: rev}
				v.RevsCount++
			}
		}
		invT := (&table[Inventory]{name: "Inventory", fields: InventoryTableFields}).bare(InventoryReset, InventoryMeasureReason, InventorySave, InventoryLoad)
		in, err := invT.Write(&v, schemaread.FormBinary)
		require.NoError(t, err)
		got, _, err := invT.Read(in)
		require.NoError(t, err)
		names := func(g *HostGroup) []string {
			out := []string{}
			for i := int32(0); i < g.HostsCount; i++ {
				out = append(out, string(g.Hosts[i].Name[:g.Hosts[i].NameLength]))
			}
			return out
		}
		assert.Equal(t, inv.All.Hosts, names(&got.All))
		assert.Equal(t, inv.Adopt.Hosts, names(&got.Adopt))
		assert.Equal(t, inv.Benches.Hosts, names(&got.Benches))
		assert.Equal(t, inv.Coordinator.Hosts, names(&got.Coordinator))
		assert.Equal(t, inv.Runners.Hosts, names(&got.Runners))
		assert.Equal(t, inv.TLA.Hosts, names(&got.Tla))
		assert.Equal(t, int32(2), got.RevsCount)

		// a host's variables as the inventory writes them
		hv := inv.Meta.Hostvars["hetzner2"]
		var h HostView
		put(h.AnsibleHost[:], &h.AnsibleHostLength, hv["ansible_host"].(string))
		h.Kind = ConfigKindMachine
		h.Slots, h.SlotsCapacity, h.Runners = int32(hv["slots"].(int)), int32(hv["slots_capacity"].(int)), int32(hv["runners"].(int))
		h.SlotsShare, h.SlotsSharePresent = int32(hv["slots_share"].(int)), true
		h.Cores, h.CoresPresent = int32(hv["cores"].(int)), true
		put(h.BenchOs[:], &h.BenchOsLength, hv["bench_os"].(string))
		put(h.NovaArch[:], &h.NovaArchLength, hv["nova_arch"].(string))
		h.NovaTla = hv["nova_tla"].(bool)
		put(h.RegistrySeat[:], &h.RegistrySeatLength, hv["registry_seat"].(string))
		put(h.RegistryRoles[:], &h.RegistryRolesLength, hv["registry_roles"].(string))
		put(h.AnsibleUser[:], &h.AnsibleUserLength, hv["ansible_user"].(string))
		put(h.NovaSeat[:], &h.NovaSeatLength, hv["nova_seat"].(string))
		put(h.NovaRedisAddr[:], &h.NovaRedisAddrLength, hv["nova_redis_addr"].(string))
		put(h.NovaBusAddr[:], &h.NovaBusAddrLength, hv["nova_bus_addr"].(string))
		h.NovaRedisPort = int32(hv["nova_redis_port"].(int))
		put(h.NovaPgDsn[:], &h.NovaPgDsnLength, hv["nova_pg_dsn"].(string))
		h.Local = hv["ansible_connection"] == "local"
		hostT := (&table[HostView]{name: "HostView", fields: HostViewTableFields}).bare(HostViewReset, HostViewMeasureReason, HostViewSave, HostViewLoad)
		in, err = hostT.Write(&h, schemaread.FormBase64)
		require.NoError(t, err)
		gotH, _, err := hostT.Read(in)
		require.NoError(t, err)
		assert.Equal(t, h, gotH)
		assert.True(t, gotH.Local)
		assert.EqualValues(t, 4, gotH.SlotsShare)

		// the beat: the merged beat, the member beat and the bench hash
		beat := snap.Beats["hetzner2"]
		var mb MachineBeat
		put(mb.Os[:], &mb.OsLength, beat.OS)
		put(mb.Arch[:], &mb.ArchLength, beat.Arch)
		put(mb.Cores[:], &mb.CoresLength, beat.Cores)
		put(mb.MemoryGb[:], &mb.MemoryGbLength, beat.MemoryGB)
		put(mb.At[:], &mb.AtLength, beat.At)
		beatT := (&table[MachineBeat]{name: "MachineBeat", fields: MachineBeatTableFields}).bare(MachineBeatReset, MachineBeatMeasureReason, MachineBeatSave, MachineBeatLoad)
		in, err = beatT.Write(&mb, schemaread.FormBinary)
		require.NoError(t, err)
		gotB, _, err := beatT.Read(in)
		require.NoError(t, err)
		assert.Equal(t, beat.OS, string(gotB.Os[:gotB.OsLength]))
		assert.Equal(t, beat.At, string(gotB.At[:gotB.AtLength]))
	})
}

func fieldInfo(t *testing.T, fields []TableFieldInfo, name string) TableFieldInfo {
	t.Helper()
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no field %s", name)
	return TableFieldInfo{}
}

// bare is a reader with no JSON form, for the tests that read a live value.
func (tb *table[T]) bare(reset func(*T), measure func(*T) (int64, error), save func(*T, []byte) int64, load func(*T, []byte, *TableReport) bool) *schemaread.Reader[T] {
	tb.reset, tb.measureReason, tb.save, tb.load = reset, measure, save, load
	tb.fromJSON = func(*T, []byte, *TableReport) bool { return false }
	return tb.reader()
}
