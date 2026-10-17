// Package noticeread is the saved notice's reader of the typed envelope and
// the legacy JSON the Core writes today (docs/SPEC-HOOK.md, the saved notice;
// schemas/friendfile/notice.schema). A saved notice reaches the reader as the
// binary wire, as padded standard base64 of that wire, as the schema JSON text,
// or as the compact JSON the saved file holds today; the reader detects the
// form, reads it strictly through pkg/schemaread, counts the read under its
// form, and refuses a malformed value by name (`NOTICE_ENVELOPE REFUSED:
// <reason>; run: nova-friend help`) where the old parse read it as no notice
// and moved on. The legacy form is exactly what the Core writes, so the notice
// reads alike from every form and a damaged value is never read as absent.
//
// It lives beside the notice package and not in it because the notice package
// imports no generated table: pkg/friendfilewire's own golden test imports
// notice, and a notice that imported friendfilewire would close an import
// cycle. The notice package takes the reader through noticeread.New.
package noticeread

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/mas-bandwidth/nova-tools/pkg/friendfilewire"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// Help is the command a refusal points at.
const Help = "nova-friend help"

// Reader reads saved notices and counts them by form. It is safe for
// concurrent use. The strict reader's value is the envelope's canonical schema
// JSON text, which the value owns, so a read never outlives a builder's arena.
type Reader struct {
	r *schemaread.Reader[string]
}

// New is a reader with empty counters.
func New() *Reader { return &Reader{r: schemaread.NewReader(codec())} }

// Counts are the per-form read counters, the cutover's evidence: how many
// notices arrived as binary, base64, JSON text and legacy JSON, and how many
// reads were refused, by reason.
func (r *Reader) Counts() *schemaread.Counts { return r.r.Counts() }

// Read is the saved notice raw holds in any form, as the envelope, or a
// *schemaread.Refusal.
func (r *Reader) Read(raw []byte) (notice.Envelope, error) {
	v, _, err := r.r.Read(raw)
	if err != nil {
		return notice.Envelope{}, err
	}
	return fromCanon(v)
}

// Write is the envelope as raw in form: the binary wire, padded standard
// base64 of the wire, or the schema JSON text. legacy is read only.
func (r *Reader) Write(e notice.Envelope, form schemaread.Form) ([]byte, error) {
	canon, err := canonOf(e)
	if err != nil {
		return nil, err
	}
	switch form {
	case schemaread.FormBinary:
		return saveWire(canon), nil
	case schemaread.FormBase64:
		w := saveWire(canon)
		if w == nil {
			return nil, errors.New("notice envelope REFUSED: invalid_value; run: nova-friend help")
		}
		return []byte(base64.StdEncoding.EncodeToString(w)), nil
	case schemaread.FormJSON:
		return []byte(canon), nil
	}
	return nil, errors.New("notice envelope: cannot write form")
}

func codec() *schemaread.Codec[string] {
	return &schemaread.Codec[string]{
		Name:  "NOTICE_ENVELOPE",
		Help:  Help,
		Reset: func(v *string) { *v = "" },
		Measure: func(wire []byte) error {
			_, err := friendfilewire.NoticeEnvelopeLoadMeasureReason(wire)
			return err
		},
		LoadWire: func(v *string, wire []byte) schemaread.Report {
			canon, rep := build(func(b *friendfilewire.NoticeEnvelopeBuilder, r *friendfilewire.TableReport) bool {
				return friendfilewire.NoticeEnvelopeLoadBuilder(b, wire, r)
			})
			*v = canon
			return rep
		},
		LoadJSON: func(v *string, text []byte) schemaread.Report {
			canon, rep := build(func(b *friendfilewire.NoticeEnvelopeBuilder, r *friendfilewire.TableReport) bool {
				return friendfilewire.NoticeEnvelopeFromJson(b, text, r)
			})
			*v = canon
			return rep
		},
		Legacy:     isLegacy,
		LoadLegacy: loadLegacy,
		SaveWire: func(v *string) []byte {
			if v == nil {
				return nil
			}
			return saveWire(*v)
		},
		SaveJSON: func(v *string) []byte {
			if v == nil {
				return nil
			}
			return []byte(*v)
		},
	}
}

// isLegacy is true for the JSON the Core's saved notice holds today: its
// message lists are plain strings (senders), where the schema JSON text holds
// a list of one-field tables. The one readable difference marks the legacy
// form; a notice with no senders may read as either and equals either.
func isLegacy(in []byte) bool {
	var m map[string]any
	if json.Unmarshal(in, &m) != nil {
		return false
	}
	if s, ok := m["senders"].([]any); ok && len(s) > 0 {
		_, isStr := s[0].(string)
		return isStr
	}
	return false
}

// loadLegacy reads the Core's saved notice strictly: the JSON the file holds
// today, carried across the list bridge into the table (S1: every field the
// struct serializes is a field of the table). A value a table bound refuses is
// clamped, the same refusal the typed forms give.
func loadLegacy(v *string, in []byte) schemaread.Report {
	var e notice.Envelope
	if err := json.Unmarshal(in, &e); err != nil {
		return schemaread.Report{Malformed: true}
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return schemaread.Report{Malformed: true}
	}
	bridged := reshape(raw, toTableShape)
	canon, rep := build(func(b *friendfilewire.NoticeEnvelopeBuilder, r *friendfilewire.TableReport) bool {
		return friendfilewire.NoticeEnvelopeFromJson(b, bridged, r)
	})
	*v = canon
	return rep
}

// build loads one envelope, locks it and keeps the canonical schema JSON the
// value owns, so the builder's arena can be freed before the caller sees it.
func build(load func(*friendfilewire.NoticeEnvelopeBuilder, *friendfilewire.TableReport) bool) (string, schemaread.Report) {
	var b friendfilewire.NoticeEnvelopeBuilder
	if !b.Init() {
		return "", schemaread.Report{Malformed: true}
	}
	var rep friendfilewire.TableReport
	ok := load(&b, &rep)
	if !ok || !b.Lock() {
		b.Shutdown()
		return "", reportOf(&rep, false)
	}
	root := b.AsConst()
	if root == nil {
		b.Shutdown()
		return "", schemaread.Report{Malformed: true}
	}
	n := friendfilewire.NoticeEnvelopeToJsonMeasure(root)
	canon := ""
	if n >= 0 {
		buf := make([]byte, n)
		if friendfilewire.NoticeEnvelopeToJson(root, buf) == n {
			canon = string(buf)
		}
	}
	b.Shutdown()
	if canon == "" {
		return "", schemaread.Report{Malformed: true}
	}
	return canon, reportOf(&rep, true)
}

// saveWire is the canonical schema JSON's binary wire.
func saveWire(canon string) []byte {
	var b friendfilewire.NoticeEnvelopeBuilder
	if !b.Init() {
		return nil
	}
	var rep friendfilewire.TableReport
	if !friendfilewire.NoticeEnvelopeFromJson(&b, []byte(canon), &rep) || !b.Lock() {
		b.Shutdown()
		return nil
	}
	root := b.AsConst()
	if root == nil {
		b.Shutdown()
		return nil
	}
	n := friendfilewire.NoticeEnvelopeMeasure(root)
	var out []byte
	if n >= 0 {
		out = make([]byte, n)
		if friendfilewire.NoticeEnvelopeSave(root, out) != n {
			out = nil
		}
	}
	b.Shutdown()
	return out
}

func reportOf(rep *friendfilewire.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == friendfilewire.TableOpenRefused || rep.Verdict == friendfilewire.TableOpenDamaged {
		if err := rep.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	return out
}

// canonOf is the envelope as its canonical schema JSON, across the list
// bridge.
func canonOf(e notice.Envelope) (string, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	bridged := reshape(raw, toTableShape)
	canon, rep := build(func(b *friendfilewire.NoticeEnvelopeBuilder, r *friendfilewire.TableReport) bool {
		return friendfilewire.NoticeEnvelopeFromJson(b, bridged, r)
	})
	if rep.Malformed || rep.Reason != "" {
		return "", errors.New("notice envelope REFUSED: " + refusalReason(rep) + "; run: nova-friend help")
	}
	return canon, nil
}

// fromCanon is the canonical schema JSON as the envelope the Core writes,
// across the list bridge.
func fromCanon(canon string) (notice.Envelope, error) {
	var e notice.Envelope
	if err := json.Unmarshal(reshape([]byte(canon), fromTableShape), &e); err != nil {
		return notice.Envelope{}, err
	}
	return e, nil
}

func refusalReason(rep schemaread.Report) string {
	if rep.Reason != "" {
		return rep.Reason
	}
	switch {
	case rep.Malformed:
		return schemaread.ReasonMalformed
	case rep.KindMismatch > 0:
		return schemaread.ReasonKindMismatch
	case rep.Clamped > 0:
		return schemaread.ReasonClamped
	}
	return schemaread.ReasonMalformed
}

// reshape runs f over the decoded value and re-encodes it; it returns text
// unchanged when text is not JSON, so a bridge never masks a refusal.
func reshape(text []byte, f func(any) any) []byte {
	var v any
	if json.Unmarshal(text, &v) != nil {
		return text
	}
	out, err := json.Marshal(f(v))
	if err != nil {
		return text
	}
	return out
}

// listKeys are the JSON names of the lists the struct writes as plain strings
// and the table reads as a list of one-field tables.
var listKeys = map[string]bool{"To": true, "CC": true, "senders": true}

// toTableShape is the bridge a record crosses when it moves to the table: a
// list of text (to, cc, senders) becomes a list of one-field tables, the
// shape the compiler has no direct form for.
func toTableShape(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if e == nil && listKeys[k] {
				x[k] = []any{}
				continue
			}
			switch k {
			case "To", "CC", "senders":
				if list, ok := e.([]any); ok {
					out := make([]any, len(list))
					for i, s := range list {
						out[i] = map[string]any{"s": s}
					}
					x[k] = out
					continue
				}
			}
			x[k] = toTableShape(e)
		}
	case []any:
		for i := range x {
			x[i] = toTableShape(x[i])
		}
	}
	return v
}

// fromTableShape is the bridge back: a list of one-field tables becomes the
// list of text the struct writes.
func fromTableShape(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			list, _ := e.([]any)
			switch k {
			case "To", "CC", "senders":
				out := make([]any, len(list))
				for i, entry := range list {
					out[i] = entry.(map[string]any)["s"]
				}
				x[k] = out
				continue
			}
			x[k] = fromTableShape(e)
		}
	case []any:
		for i := range x {
			x[i] = fromTableShape(x[i])
		}
	}
	return v
}
