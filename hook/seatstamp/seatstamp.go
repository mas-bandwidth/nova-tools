// Package seatstamp is the hook record's reader of the typed stamp and the
// legacy JSON stamp alike (docs/SPEC-HOOK.md; schemas/hook/stamp.schema). A
// lease or a proof value reaches the reader as the binary wire, as padded
// standard base64 of that wire, as the schema JSON text, or as the compact JSON
// the record's scripts write today; the reader detects the form, reads it
// strictly through pkg/schemaread, counts the read under its form, and refuses
// a malformed value by name (`STAMP REFUSED: <reason>; run: nova-hook status
// -h`) where the old parse read it as no stamp and moved on. The legacy form
// is exactly what the scripts write, so an RFC 3339 time, a key the scripts
// never write or spacing they never write is legacy_shape, not a stamp.
//
// It lives beside the hook package and not in it because the hook package
// imports no generated table: pkg/hookwire's own tests import hook, and a hook
// that imported hookwire would close an import cycle. A store takes the reader
// through hook.Store.Stamps, and Judge through hook.JudgeWith.
package seatstamp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/hookwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// Help is the command a refusal points at.
const Help = "nova-hook status -h"

// ReasonLegacyShape is the refusal of a JSON value that carries the legacy
// stamp's keys and is not exactly what the record's scripts write.
const ReasonLegacyShape = "legacy_shape"

// Reader reads stamps and counts them by form. It is safe for concurrent use.
type Reader struct {
	r *schemaread.Reader[hookwire.Stamp]
}

// New is a reader with empty counters.
func New() *Reader { return &Reader{r: schemaread.NewReader(codec())} }

// Counts are the per-form read counters, the cutover's evidence: how many
// stamps arrived as binary, base64, JSON text and legacy JSON, and how many
// reads were refused, by reason.
func (r *Reader) Counts() *schemaread.Counts { return r.r.Counts() }

// ReadStamp is the stamp raw holds in any form, or a *schemaread.Refusal.
func (r *Reader) ReadStamp(raw string) (hook.Stamp, error) {
	v, _, err := r.r.Read([]byte(raw))
	if err != nil {
		return hook.Stamp{}, err
	}
	return hook.Stamp{Epoch: v.Epoch, AtMS: v.AtMs, By: textOf(v.By[:], v.ByLength)}, nil
}

var _ hook.StampReader = (*Reader)(nil)

func codec() *schemaread.Codec[hookwire.Stamp] {
	byMax := 0
	for _, f := range hookwire.StampTableFields {
		if f.Name == "by" {
			byMax = int(f.ArrayBound)
		}
	}
	return &schemaread.Codec[hookwire.Stamp]{
		Name:  "STAMP",
		Help:  Help,
		Reset: hookwire.StampReset,
		Measure: func(wire []byte) error {
			_, err := hookwire.StampLoadMeasureReason(wire)
			return err
		},
		LoadWire: func(v *hookwire.Stamp, wire []byte) schemaread.Report {
			return via(v, func(b *hookwire.StampBuilder, rep *hookwire.TableReport) bool {
				return hookwire.StampLoadBuilder(b, wire, rep)
			})
		},
		LoadJSON: func(v *hookwire.Stamp, text []byte) schemaread.Report {
			return via(v, func(b *hookwire.StampBuilder, rep *hookwire.TableReport) bool {
				return hookwire.StampFromJson(b, text, rep)
			})
		},
		Legacy:     isLegacy,
		LoadLegacy: func(v *hookwire.Stamp, in []byte) schemaread.Report { return loadLegacy(v, in, byMax) },
		Constraints: func(v *hookwire.Stamp, c *schemaread.Checker) {
			c.Required("epoch", v.EpochPresent && v.Epoch > 0)
			c.Required("at_ms", v.AtMsPresent && v.AtMs > 0)
			c.Required("by", v.ByLength > 0)
			c.Text("by", textOf(v.By[:], v.ByLength), byMax)
		},
	}
}

// isLegacy is true for the compact JSON the record's scripts write: it opens
// with the epoch key and carries the "at" key, which the typed text spells
// at_ms.
func isLegacy(in []byte) bool {
	return bytes.HasPrefix(in, []byte(`{"epoch":`)) && bytes.Contains(in, []byte(`"at":`))
}

// legacyText is the stamp as the record's scripts write it (hook/redis.go,
// stampLua): compact JSON, the time in milliseconds, the holder a JSON string.
func legacyText(epoch, atMS int64, by string) string {
	raw, err := json.Marshal(by)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(`{"epoch":%d,"at":%d,"by":%s}`, epoch, atMS, raw)
}

// loadLegacy reads the scripts' stamp strictly: exactly what they write for
// the values it carries, or legacy_shape. A holder over the table's bound is
// clamped, the same refusal the typed forms give.
func loadLegacy(v *hookwire.Stamp, text []byte, byMax int) schemaread.Report {
	var s struct {
		Epoch int64  `json:"epoch"`
		At    int64  `json:"at"`
		By    string `json:"by"`
	}
	d := json.NewDecoder(bytes.NewReader(text))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || legacyText(s.Epoch, s.At, s.By) != string(text) {
		return schemaread.Report{Reason: ReasonLegacyShape}
	}
	if len(s.By) > byMax {
		return schemaread.Report{Clamped: 1}
	}
	hookwire.StampReset(v)
	v.Epoch, v.EpochPresent = s.Epoch, true
	v.AtMs, v.AtMsPresent = s.At, true
	v.ByLength = int32(copy(v.By[:len(v.By)-1], s.By))
	return schemaread.Report{}
}

func via(v *hookwire.Stamp, load func(*hookwire.StampBuilder, *hookwire.TableReport) bool) schemaread.Report {
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

// textOf is a counted string field's text; a length past the buffer is read
// whole so the bound check sees it.
func textOf(buf []byte, n int32) string {
	if n < 0 {
		return ""
	}
	if int(n) > len(buf) {
		return string(buf) + strings.Repeat("?", int(n)-len(buf))
	}
	return string(buf[:n])
}
