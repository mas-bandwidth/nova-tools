package hook

// The typed stamp (schemas/hook/stamp.schema, generated into pkg/hookwire).
// A lease or a proof is a stamp: the record's epoch it was written under,
// the store's time in Unix milliseconds (int64, never RFC 3339 text), and its
// holder. The read is pkg/schemaread's strict one: the binary wire, padded
// standard base64 of that wire, the schema JSON text, or the stamp the
// record's scripts write today, each counted under the form it arrived in. A
// value that is none of them is refused by name and the refusal rides the
// push's status (PushStatus.Refused); the push still reads down, but the gate
// says why instead of failing closed in silence.
//
// The writers are unchanged: stampLua and answerLua still write the legacy
// text, so readers go first and the per-form counter is the evidence for the
// later writer switch (the owner's rule S4).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/hookwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// StampHelp is the command a stamp refusal points at.
const StampHelp = "nova-hook status -h"

// stampName is the prefix of every stamp refusal.
const stampName = "STAMP"

// ReasonLegacyShape is the refusal of a legacy text that is not the shape the
// record's scripts write (or its RFC 3339 predecessor).
const ReasonLegacyShape = "legacy_shape"

// stampReader is the one reader every Judge uses; its counters are the
// process's per-form stamp reads (the tests read them through stampReads).
var stampReader = schemaread.NewReader(stampCodec())

// legacyRFC3339 counts legacy stamps whose time was RFC 3339 text.
var legacyRFC3339 atomic.Uint64

// readStamp reads raw with r, the stamp it holds, or the refusal by name.
func readStamp(r *schemaread.Reader[hookwire.Stamp], raw string) (stamp, error) {
	v, _, err := r.Read([]byte(raw))
	if err != nil {
		return stamp{}, err
	}
	return stamp{Epoch: v.Epoch, At: v.AtMs, By: string(v.By[:v.ByLength])}, nil
}

func stampCodec() *schemaread.Codec[hookwire.Stamp] {
	byMax := 128
	for _, f := range hookwire.StampTableFields {
		if f.Name == "by" {
			byMax = int(f.ArrayBound)
		}
	}
	return &schemaread.Codec[hookwire.Stamp]{
		Name:  stampName,
		Help:  StampHelp,
		Reset: hookwire.StampReset,
		Measure: func(wire []byte) error {
			_, err := hookwire.StampLoadMeasureReason(wire)
			return err
		},
		LoadWire: func(v *hookwire.Stamp, wire []byte) schemaread.Report {
			return stampVia(v, func(b *hookwire.StampBuilder, rep *hookwire.TableReport) bool {
				return hookwire.StampLoadBuilder(b, wire, rep)
			})
		},
		LoadJSON: func(v *hookwire.Stamp, text []byte) schemaread.Report {
			return stampVia(v, func(b *hookwire.StampBuilder, rep *hookwire.TableReport) bool {
				return hookwire.StampFromJson(b, text, rep)
			})
		},
		Legacy: func(in []byte) bool {
			t := bytes.TrimSpace(in)
			return bytes.HasPrefix(t, []byte(`{"epoch":`)) && !bytes.Contains(t, []byte(`"at_ms"`))
		},
		LoadLegacy: loadLegacyStamp,
		Constraints: func(v *hookwire.Stamp, c *schemaread.Checker) {
			c.Required("epoch", v.EpochPresent && v.Epoch > 0)
			c.Required("at_ms", v.AtMsPresent && v.AtMs > 0)
			c.Required("by", v.ByLength > 0)
			c.Text("by", stampText(v), byMax)
		},
		SaveWire: func(v *hookwire.Stamp) []byte {
			return stampWhole(func() int64 { return hookwire.StampMeasure(v) }, func(b []byte) int64 { return hookwire.StampSave(v, b) })
		},
		SaveJSON: func(v *hookwire.Stamp) []byte {
			return stampWhole(func() int64 { return hookwire.StampToJsonMeasure(v) }, func(b []byte) int64 { return hookwire.StampToJson(v, b) })
		},
	}
}

// stampText is the holder as the table holds it; a length past the buffer is
// read whole so the bound check sees it.
func stampText(v *hookwire.Stamp) string {
	n := int(v.ByLength)
	switch {
	case n < 0:
		return ""
	case n > len(v.By):
		return string(v.By[:]) + string(bytes.Repeat([]byte("?"), n-len(v.By)))
	}
	return string(v.By[:n])
}

func stampVia(v *hookwire.Stamp, load func(*hookwire.StampBuilder, *hookwire.TableReport) bool) schemaread.Report {
	var b hookwire.StampBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	defer b.Shutdown()
	var rep hookwire.TableReport
	ok := load(&b, &rep)
	if root := b.GetRoot(); root != nil {
		*v = *root
	}
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == hookwire.TableOpenRefused || rep.Verdict == hookwire.TableOpenDamaged {
		out.Reason = rep.Err().Error()
	}
	return out
}

// stampWhole runs a generated Measure and Save into one exact buffer; nil
// when either refuses.
func stampWhole(measure func() int64, save func([]byte) int64) []byte {
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

// loadLegacyStamp reads the stamp the record's scripts write: compact JSON
// with the keys epoch, at and by, at the store's time in milliseconds, or in
// the RFC 3339 text an earlier writer used (read and counted, never written).
// Any other shape is legacy_shape.
func loadLegacyStamp(v *hookwire.Stamp, text []byte) schemaread.Report {
	bad := schemaread.Report{Reason: ReasonLegacyShape}
	var s struct {
		Epoch *int64          `json:"epoch"`
		At    json.RawMessage `json:"at"`
		By    *string         `json:"by"`
	}
	d := json.NewDecoder(bytes.NewReader(text))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || d.Decode(&struct{}{}) != io.EOF || s.Epoch == nil || s.By == nil || len(s.At) == 0 {
		return bad
	}
	var at int64
	rfc := false
	if s.At[0] == '"' {
		var ts string
		if json.Unmarshal(s.At, &ts) != nil {
			return bad
		}
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return bad
		}
		at, rfc = t.UnixMilli(), true
	} else if json.Unmarshal(s.At, &at) != nil || fmt.Sprint(at) != string(s.At) {
		return bad
	}
	hookwire.StampReset(v)
	v.Epoch, v.EpochPresent = *s.Epoch, true
	v.AtMs, v.AtMsPresent = at, true
	if len(*s.By) > len(v.By)-1 {
		return schemaread.Report{Clamped: 1}
	}
	v.ByLength = int32(copy(v.By[:len(v.By)-1], *s.By))
	if rfc {
		legacyRFC3339.Add(1)
	}
	return schemaread.Report{}
}
