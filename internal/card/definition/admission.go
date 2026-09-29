package definition

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"unicode/utf8"
)

// AdmissionSchema names the shape of the canonical admission record.
const AdmissionSchema = "card-admission/1"

// Admission is the record one admitted card carries: identities, digests and the
// header fields that are data. It holds no brief, no DONE-WHEN and no PROBES:
// their bytes are covered by DefinitionDigest, and the brief alone by BriefDigest.
type Admission struct {
	ID               string
	DefinitionDigest string // hex SHA-256 of the card file
	BriefDigest      string // hex SHA-256 of the brief
	ObjectID         string // the Git object id of the committed blob
	Commit           string // the full commit the blob was read at
	Repository       string // the repository identity
	Path             string // the repository-relative path of the card file
	Kind             string
	Completion       Completion
	PolicyVersion    int
	DependsOn        []string
	Entry            string // "" when the card has none
	Tier             string
	CardSchema       string // the card's SCHEMA value
	Title            string
	Paths            []string
	Test             string // the TEST value as the card writes it
	Doors            string
}

// Admissions joins an array of definitions to the pins they were read from,
// position for position, and produces one record per card. It refuses a length
// mismatch, a definition whose file is not its pin's path or whose digest is not
// its pin's SHA-256 (the definition was not read from those bytes), and any
// definition that Validate's field checks refuse. It returns no records beside a
// refusal.
func Admissions(defs []Definition, pins []Pinned) ([]Admission, []Refusal) {
	if len(defs) == 0 || len(defs) != len(pins) {
		return nil, []Refusal{{Operation: OpAdmit, Cause: CausePinMismatch,
			Found: fmt.Sprintf("%d definitions and %d pins", len(defs), len(pins)),
			Next:  "pass one pin per definition, in the same order: Parse(Sources(pins))"}}
	}
	var refs []Refusal
	out := make([]Admission, 0, len(defs))
	for i, d := range defs {
		p := pins[i]
		file := fileLabel(i, d)
		if d.File != p.Path || d.Digest != p.SHA256 || d.Digest == "" {
			refs = append(refs, Refusal{Operation: OpAdmit, File: file, Cause: CausePinMismatch,
				Found: fmt.Sprintf("definition %s (digest %.12s) does not match pin %s (sha256 %.12s)", d.File, d.Digest, p.Path, p.SHA256),
				Next:  "parse the definitions from the pinned bytes: Parse(Sources(pins))"})
			continue
		}
		if rs := checkDefinition(OpAdmit, d, nil, d.Lines, file); len(rs) > 0 {
			refs = append(refs, rs...)
			continue
		}
		cs, _ := Classify([]string{d.Kind})
		deps := append([]string(nil), d.DependsOn...)
		sort.Strings(deps)
		paths := append([]string(nil), d.Paths...)
		sort.Strings(paths)
		out = append(out, Admission{
			ID: d.ID, DefinitionDigest: d.Digest, BriefDigest: d.BriefDigest,
			ObjectID: p.ObjectID, Commit: p.Commit, Repository: p.Repository, Path: p.Path,
			Kind: d.Kind, Completion: cs[0], PolicyVersion: PolicyVersion(),
			DependsOn: deps, Entry: d.Entry, Tier: d.Tier, CardSchema: d.Schema,
			Title: d.Title, Paths: paths, Test: d.Test.String(), Doors: d.Doors,
		})
	}
	if len(refs) > 0 {
		return nil, refs
	}
	return out, nil
}

// EncodeAdmissions writes each record in its canonical form: one JSON object, keys
// in byte order, only strings and arrays of strings (no numbers, so no floats),
// no insignificant whitespace, and no HTML escaping. The same record always
// encodes to the same bytes. A field that is not valid UTF-8 is refused.
func EncodeAdmissions(as []Admission) ([][]byte, []Refusal) {
	out := make([][]byte, len(as))
	var refs []Refusal
	for i, a := range as {
		b, why := canonical(a)
		if why != "" {
			refs = append(refs, Refusal{Operation: OpAdmit, File: a.Path, Cause: CauseInvalidValue,
				Found: why, Next: "build the record with Admissions"})
			continue
		}
		out[i] = b
	}
	if len(refs) > 0 {
		return nil, refs
	}
	return out, nil
}

// DigestAdmissions is the hex SHA-256 of each record's canonical encoding.
func DigestAdmissions(as []Admission) ([]string, []Refusal) {
	enc, refs := EncodeAdmissions(as)
	if refs != nil {
		return nil, refs
	}
	out := make([]string, len(enc))
	for i, b := range enc {
		h := sha256.Sum256(b)
		out[i] = hex.EncodeToString(h[:])
	}
	return out, nil
}

type kv struct {
	key    string
	str    string
	list   []string
	isList bool
	omit   bool
}

func canonical(a Admission) ([]byte, string) {
	fields := []kv{
		{key: "schema", str: AdmissionSchema},
		{key: "card_schema", str: a.CardSchema},
		{key: "id", str: a.ID},
		{key: "definition_digest", str: a.DefinitionDigest},
		{key: "brief_digest", str: a.BriefDigest},
		{key: "object_id", str: a.ObjectID},
		{key: "commit", str: a.Commit},
		{key: "repository", str: a.Repository},
		{key: "path", str: a.Path},
		{key: "kind", str: a.Kind},
		{key: "completion", str: string(a.Completion)},
		{key: "completion_policy", str: fmt.Sprint(a.PolicyVersion)},
		{key: "depends_on", list: a.DependsOn, isList: true},
		{key: "entry", str: a.Entry, omit: a.Entry == ""},
		{key: "tier", str: a.Tier},
		{key: "title", str: a.Title},
		{key: "paths", list: a.Paths, isList: true},
		{key: "test", str: a.Test},
		{key: "doors", str: a.Doors},
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].key < fields[j].key })
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, f := range fields {
		if f.omit {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		writeJSONString(&b, f.key)
		b.WriteByte(':')
		if f.isList {
			b.WriteByte('[')
			for i, s := range f.list {
				if !utf8.ValidString(s) {
					return nil, fmt.Sprintf("%s holds invalid UTF-8", f.key)
				}
				if i > 0 {
					b.WriteByte(',')
				}
				writeJSONString(&b, s)
			}
			b.WriteByte(']')
			continue
		}
		if !utf8.ValidString(f.str) {
			return nil, fmt.Sprintf("%s holds invalid UTF-8", f.key)
		}
		writeJSONString(&b, f.str)
	}
	b.WriteByte('}')
	return b.Bytes(), ""
}

// writeJSONString writes s as a JSON string: quote and backslash escaped, every
// control character and the Unicode line and paragraph separators as \u escapes,
// everything else as its own UTF-8 (so <, > and & are never escaped).
func writeJSONString(b *bytes.Buffer, s string) {
	const hexd = "0123456789abcdef"
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029:
			b.WriteString(`\u`)
			b.WriteByte(hexd[r>>12&0xf])
			b.WriteByte(hexd[r>>8&0xf])
			b.WriteByte(hexd[r>>4&0xf])
			b.WriteByte(hexd[r&0xf])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
