// Package rowsread is the typed half of pkg/config's record readers
// (pkg/config records.go): it loads each record from the generated tables of
// schemas/config (pkg/configwire), from the table wire or from a fixed batch,
// and hands pkg/config the record as the store reads it. pkg/config imports no
// generated table (pkg/configwire's golden test imports pkg/config), so a
// program that reads the typed forms imports this package, whose init installs
// the loads (config.InstallRecordWire), as a database/sql driver registers.
//
// Every table here is a fixed table (the owner's rule S1); the base64 of an
// image is decoded by pkg/schemaread before a load is called (S2). A load
// refuses an image it cannot read as the store's record (a field pair out of
// name order, a pair named argv, a kind no word names) as malformed; the
// generated load refuses the rest by name, and clamps count as refusals.
package rowsread

import (
	"encoding/binary"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/configwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

func init() {
	config.InstallRecordWire(config.RecordWire{Side: side, File: file, Loop: loop, MemberBeat: memberBeat, BenchBeat: benchBeat})
}

// ReasonNotOneRecord is the refusal of a fixed batch that holds other than the
// one record a single value is.
const ReasonNotOneRecord = "not_one_record"

// table is one generated table's loads: the table wire and the fixed batch.
type table[W any] struct {
	reset     func(*W)
	load      func(*W, []byte, *configwire.TableReport) bool
	fixedLoad func([]W, []byte, []configwire.TableFixedEntry, *configwire.TableReport) int64
	known     []configwire.TableFixedKnownLayout
	planMax   int32
}

var (
	rowFields     = table[configwire.RowFields]{configwire.RowFieldsReset, configwire.RowFieldsLoad, configwire.RowFieldsFixedLoad, configwire.RowFieldsFixedKnown, configwire.RowFieldsFixedPlanMax}
	configRow     = table[configwire.ConfigRow]{configwire.ConfigRowReset, configwire.ConfigRowLoad, configwire.ConfigRowFixedLoad, configwire.ConfigRowFixedKnown, configwire.ConfigRowFixedPlanMax}
	configChange  = table[configwire.ConfigChange]{configwire.ConfigChangeReset, configwire.ConfigChangeLoad, configwire.ConfigChangeFixedLoad, configwire.ConfigChangeFixedKnown, configwire.ConfigChangeFixedPlanMax}
	fileHeader    = table[configwire.FileHeader]{configwire.FileHeaderReset, configwire.FileHeaderLoad, configwire.FileHeaderFixedLoad, configwire.FileHeaderFixedKnown, configwire.FileHeaderFixedPlanMax}
	inventoryLoop = table[configwire.InventoryLoop]{configwire.InventoryLoopReset, configwire.InventoryLoopLoad, configwire.InventoryLoopFixedLoad, configwire.InventoryLoopFixedKnown, configwire.InventoryLoopFixedPlanMax}
	memberTable   = table[configwire.MemberBeat]{configwire.MemberBeatReset, configwire.MemberBeatLoad, configwire.MemberBeatFixedLoad, configwire.MemberBeatFixedKnown, configwire.MemberBeatFixedPlanMax}
	benchTable    = table[configwire.BenchBeat]{configwire.BenchBeatReset, configwire.BenchBeatLoad, configwire.BenchBeatFixedLoad, configwire.BenchBeatFixedKnown, configwire.BenchBeatFixedPlanMax}
)

// one is a single value from its image: the table wire, or a fixed batch of
// exactly one record.
func (t table[W]) one(wire []byte) (*W, schemaread.Report) {
	if wire[0] == configwire.TableFixedForm {
		vals, rep := t.batch(wire)
		if refused(rep) {
			return nil, rep
		}
		if len(vals) != 1 {
			return nil, schemaread.Report{Reason: ReasonNotOneRecord}
		}
		return &vals[0], rep
	}
	v := new(W)
	t.reset(v)
	var r configwire.TableReport
	ok := t.load(v, wire, &r)
	out := schemaread.Report{Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped), Malformed: r.Malformed || !ok}
	if r.Verdict == configwire.TableOpenRefused || r.Verdict == configwire.TableOpenDamaged {
		if err := r.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	return v, out
}

// batch is every record of one fixed batch, the whole of data. A refusal
// keeps the fixed form's own reason (layout_unknown, truncated and the rest).
func (t table[W]) batch(data []byte) ([]W, schemaread.Report) {
	start, rec, _ := t.extent(data)
	n := 0
	if rec > 0 && start <= len(data) {
		n = (len(data) - start) / int(rec)
	}
	vals := make([]W, n)
	plan := make([]configwire.TableFixedEntry, t.planMax)
	var r configwire.TableReport
	got := t.fixedLoad(vals, data, plan, &r)
	if got < 0 {
		reason := r.Reason
		if reason == "" {
			reason = schemaread.ReasonMalformed
		}
		return nil, schemaread.Report{Reason: reason}
	}
	return vals[:got], schemaread.Report{Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped), Malformed: r.Malformed}
}

// extent is where a fixed batch's records start, the size of one and the
// layout hash each record leads with; rec is 0 for a batch too short to say
// or a layout this build does not know, which the fixed load then refuses.
func (t table[W]) extent(data []byte) (start int, rec int64, hash uint64) {
	if len(data) < configwire.TableFixedHeaderBytes+4 {
		return 0, 0, 0
	}
	hash = binary.LittleEndian.Uint64(data[configwire.TableFixedHashAt:])
	start = configwire.TableFixedHeaderBytes + 4 + int(binary.LittleEndian.Uint32(data[configwire.TableFixedHeaderBytes:]))
	for _, k := range t.known {
		if k.Hash == hash && k.Record > 8 {
			rec = k.Record
		}
	}
	return start, rec, hash
}

// take is the fixed batch data begins with and what follows it: the batch
// runs while each next record leads with the batch's layout hash (the file
// twin's batches follow one another, FileHeader in schemas/config).
func (t table[W]) take(data []byte) ([]W, []byte, schemaread.Report) {
	start, rec, hash := t.extent(data)
	end := len(data)
	if rec > 0 && start <= len(data) {
		end = start
		for end+int(rec) <= len(data) && binary.LittleEndian.Uint64(data[end:]) == hash {
			end += int(rec)
		}
	}
	vals, rep := t.batch(data[:end])
	return vals, data[end:], rep
}

func refused(r schemaread.Report) bool {
	return r.Reason != "" || r.Malformed || r.KindMismatch > 0 || r.Clamped > 0
}

func add(a, b schemaread.Report) schemaread.Report {
	return schemaread.Report{Unknown: a.Unknown + b.Unknown, KindMismatch: a.KindMismatch + b.KindMismatch, Clamped: a.Clamped + b.Clamped, Malformed: a.Malformed || b.Malformed, Reason: a.Reason + b.Reason}
}

var malformed = schemaread.Report{Malformed: true}

// text is a counted string's text; a length outside the buffer is no text.
func text(buf []byte, n int32, ok *bool) string {
	if n < 0 || int(n) > len(buf) {
		*ok = false
		return ""
	}
	return string(buf[:n])
}

// fieldsOf is a RowFields record as the store's fields: the pairs, kept in
// strictly increasing name order (which makes the image of a row unique), and
// the argv slot, which no pair is named for.
func fieldsOf(r *configwire.RowFields) (map[string]string, bool) {
	ok := int(r.PairsCount) <= len(r.Pairs)
	out := map[string]string{}
	prev := ""
	for i := 0; ok && i < int(r.PairsCount); i++ {
		p := &r.Pairs[i]
		name := text(p.Name[:], p.NameLength, &ok)
		if name == "argv" || (i > 0 && name <= prev) {
			ok = false
		}
		out[name] = text(p.Value[:], p.ValueLength, &ok)
		prev = name
	}
	if r.ArgvLength != 0 {
		out["argv"] = text(r.Argv[:], r.ArgvLength, &ok)
	}
	return out, ok
}

// word is an enum member's canonical word, the member's name in lower case
// (the ConfigKind and ChangeOp doc); "" for None or a value no member names.
func word(name string) string {
	if name == "None" || name == "???" {
		return ""
	}
	return strings.ToLower(name)
}

// side is a history row's before or after (pg.go History).
func side(wire []byte) (map[string]string, schemaread.Report) {
	v, rep := rowFields.one(wire)
	if v == nil || refused(rep) {
		return nil, rep
	}
	m, ok := fieldsOf(v)
	if !ok {
		return nil, malformed
	}
	return m, rep
}

// file is the file twin's image: a FileHeader batch of one, then the rows as a
// ConfigRow batch, the history as a ConfigChange batch, and the sides of the
// history as a RowFields batch, each change's before (when it has one) then
// its after, in history order.
func file(wire []byte) (config.FileState, schemaread.Report) {
	var st config.FileState
	hdr, rest, rep := fileHeader.take(wire)
	if refused(rep) {
		return st, rep
	}
	rows, rest, r2 := configRow.take(rest)
	if rep = add(rep, r2); refused(rep) {
		return st, rep
	}
	changes, rest, r3 := configChange.take(rest)
	if rep = add(rep, r3); refused(rep) {
		return st, rep
	}
	sides, rest, r4 := rowFields.take(rest)
	if rep = add(rep, r4); refused(rep) {
		return st, rep
	}
	if len(hdr) != 1 || len(rest) != 0 {
		return st, malformed
	}
	ok := true
	st.Schema = int(hdr[0].Schema)
	st.Rows = map[string]map[string]config.Row{}
	for i := range rows {
		r := &rows[i]
		kind := word(configwire.EnumNameConfigKind(r.Kind))
		fields, fok := fieldsOf(&r.Fields)
		row := config.Row{Name: text(r.Name[:], r.NameLength, &ok), Fields: fields, CreatedAt: text(r.CreatedAt[:], r.CreatedAtLength, &ok), UpdatedAt: text(r.UpdatedAt[:], r.UpdatedAtLength, &ok)}
		if st.Rows[kind] == nil {
			st.Rows[kind] = map[string]config.Row{}
		}
		if _, dup := st.Rows[kind][row.Name]; dup || kind == "" || !fok {
			ok = false
		}
		st.Rows[kind][row.Name] = row
	}
	next := 0
	sideAt := func(has bool) map[string]string {
		if !has {
			return nil
		}
		if next >= len(sides) {
			ok = false
			return nil
		}
		m, fok := fieldsOf(&sides[next])
		next++
		ok = ok && fok
		return m
	}
	for i := range changes {
		c := &changes[i]
		ch := config.Change{ID: c.Id, Kind: word(configwire.EnumNameConfigKind(c.Kind)), Name: text(c.Name[:], c.NameLength, &ok), Op: word(configwire.EnumNameChangeOp(c.Op)),
			Actor: text(c.Actor[:], c.ActorLength, &ok), Reason: text(c.Reason[:], c.ReasonLength, &ok), At: text(c.At[:], c.AtLength, &ok)}
		ch.Before = sideAt(c.HasBefore)
		ch.After = sideAt(c.HasAfter)
		st.History = append(st.History, ch)
	}
	if !ok || next != len(sides) {
		return config.FileState{}, malformed
	}
	return st, rep
}

// loop is a loop's Redis view from its InventoryLoop image: the fields the
// loop kind's apply writes, in their canonical text (keys a comma list,
// every decimal, the flags true or false), for parseLoop to read.
func loop(wire []byte) (config.View, schemaread.Report) {
	v, rep := inventoryLoop.one(wire)
	if v == nil || refused(rep) {
		return nil, rep
	}
	ok := int(v.KeysCount) <= len(v.Keys)
	keys := []string{}
	for i := 0; ok && i < int(v.KeysCount); i++ {
		keys = append(keys, text(v.Keys[i].Name[:], v.Keys[i].NameLength, &ok))
	}
	out := config.View{
		"machine": text(v.Machine[:], v.MachineLength, &ok), "name": text(v.Name[:], v.NameLength, &ok),
		"argv": text(v.Argv[:], v.ArgvLength, &ok), "seat": text(v.Seat[:], v.SeatLength, &ok),
		"keys": strings.Join(keys, ","), "every": strconv.Itoa(int(v.Every)),
		"keepalive": strconv.FormatBool(v.Keepalive), "enabled": strconv.FormatBool(v.Enabled),
		"log": text(v.Log[:], v.LogLength, &ok),
	}
	if !ok {
		return nil, malformed
	}
	return out, rep
}

// memberBeat is the member beat record from its MemberBeat image.
func memberBeat(wire []byte) (config.MemberBeat, schemaread.Report) {
	v, rep := memberTable.one(wire)
	if v == nil || refused(rep) {
		return config.MemberBeat{}, rep
	}
	return config.MemberBeat{AtMS: v.AtMs, Cores: int(v.Cores)}, rep
}

// benchBeat is the bench hash's fields from its BenchBeat image: a field the
// beat does not carry (empty text, at 0) is left out, as the hash leaves it.
func benchBeat(wire []byte) (config.View, schemaread.Report) {
	v, rep := benchTable.one(wire)
	if v == nil || refused(rep) {
		return nil, rep
	}
	ok := true
	out := config.View{}
	for k, s := range map[string]string{
		"host": text(v.Host[:], v.HostLength, &ok), "load1": text(v.Load1[:], v.Load1Length, &ok),
		"ncpu": text(v.Ncpu[:], v.NcpuLength, &ok), "cpu": text(v.Cpu[:], v.CpuLength, &ok),
		"os": text(v.Os[:], v.OsLength, &ok), "arch": text(v.Arch[:], v.ArchLength, &ok),
		"memory_gb": text(v.MemoryGb[:], v.MemoryGbLength, &ok),
	} {
		if s != "" {
			out[k] = s
		}
	}
	if v.AtMs > 0 {
		out["at"] = strconv.FormatInt(v.AtMs, 10)
	}
	if !ok {
		return nil, malformed
	}
	return out, rep
}
