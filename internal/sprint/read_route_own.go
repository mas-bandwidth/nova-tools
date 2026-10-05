package sprint

import (
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// PropReaderUsage is the readers table's property of each reader's usage line:
// one record a line, the reader, a tab, then the line. A line that names
// model= is a friend or bud reader, who brings that model and needs no fleet
// route (readFieldsOf). The ask reads the property. SetReaderUsage records it.
const PropReaderUsage = "reader_usage"

// SetReaderUsageLine is the property with reader's record set to line, or
// taken out when line is empty. Records stay in reader order, one a line.
func SetReaderUsageLine(was, reader, line string) string {
	recs := map[string]string{}
	for _, rec := range strings.Split(was, "\n") {
		name, rest, ok := strings.Cut(rec, "\t")
		if !ok || name == "" || rest == "" {
			continue
		}
		recs[name] = rest
	}
	if line == "" {
		delete(recs, reader)
	} else {
		recs[reader] = line
	}
	names := make([]string, 0, len(recs))
	for name := range recs {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(name)
		b.WriteByte('\t')
		b.WriteString(recs[name])
	}
	return b.String()
}

// readerUsageLine is reader's record in PropReaderUsage, or "".
func (s *Snapshot) readerUsageLine(reader string) string {
	if s == nil || s.Readers == nil {
		return ""
	}
	raw, ok := s.Readers.Prop(PropReaderUsage)
	if !ok || raw == "" {
		return ""
	}
	for _, rec := range strings.Split(raw, "\n") {
		name, rest, found := strings.Cut(rec, "\t")
		if found && name == reader {
			return rest
		}
	}
	return ""
}

// broughtModel is the model and harness a friend or bud reader brings on its
// usage line. ok is false when the line names no model: a fleet reader, which
// still draws a route.
func (s *Snapshot) broughtModel(reader string) (model, harness string, ok bool) {
	u := cardcost.ParseUsage(s.readerUsageLine(reader))
	if u.Model == "" {
		return "", "", false
	}
	for _, w := range u.Extra {
		k, v, cut := strings.Cut(w, "=")
		if cut && k == "harness" {
			harness = v
			break
		}
	}
	return u.Model, harness, true
}

// readerUpServes says a reader up brings its own model and so can read tier.
// Until a reader row names the tiers it serves, any such reader serves every
// tier. A fleet reader, whose usage line names no model, serves a tier only
// by an enabled route of it.
func (s *Snapshot) readerUpServes(tier string) bool {
	if s == nil || s.Readers == nil || tier == "" {
		return false
	}
	for _, rd := range s.Readers.Rows() {
		if !s.ReaderIsUp(rd) {
			continue
		}
		if _, _, ok := s.broughtModel(rd); ok {
			return true
		}
	}
	return false
}

// readFieldsOf is the route fields of one read asked of reader. A friend or
// bud reader (broughtModel) needs no route: the read records that model and
// harness and the card's read tier, and the route index stays. A fleet reader
// draws a route of the tier (readRouteOf).
func (s *Snapshot) readFieldsOf(ri routeIndexes, pr *Card, reader string, avoid []string) map[string]string {
	if model, harness, ok := s.broughtModel(reader); ok {
		out := map[string]string{FieldTier: s.readTierOf(pr), FieldModel: model}
		if harness != "" {
			out[FieldHarness] = harness
		}
		return out
	}
	return s.readRouteOf(ri, pr, avoid)
}
