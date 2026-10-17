// Package update implements the shared, explicit version inventory behind
// nova-update and nova-version. It never discovers homes or installs on a verdict.
package update

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"unsafe"

	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

const Header = "name\tkind\tinstalled\tlatest\tapply\towner"

// tabbed is a tab-separated header as a reader copies it from a line: each tab
// spelled <TAB>, the way the banner spells it, never escaped as \x09.
func tabbed(header string) string { return strings.ReplaceAll(header, "\t", "<TAB>") }

var Kinds = []string{"harness", "engine", "model", "tool", "pin"}

type Entry struct {
	Name, Kind, Latest, Owner string
	Installed, Apply          []string
}

func kindValid(s string) bool {
	return slices.Contains(Kinds, s)
}
func argv(s string) ([]string, error) {
	if s == "" || strings.TrimSpace(s) != s || strings.Contains(s, "  ") || strings.ContainsAny(s, "\r\n\t\"'") {
		return nil, fmt.Errorf("argv requires single spaces and no quoting (put arguments needing spaces in a script and name the script)")
	}
	return strings.Split(s, " "), nil
}

// ManifestError is every problem one read of a manifest found, in line order, each one
// worded as a single-problem refusal was (`line 3: ...`): a run that finds several says so
// once, and the writer fixes the file in one pass and not one error per run.
type ManifestError struct {
	Problems []string
	// More is how many problems past manifestProblemCap were found and not listed.
	More int
}

func (e *ManifestError) Error() string {
	out := strings.Join(e.Problems, "; ")
	if e.More > 0 {
		out += fmt.Sprintf("; and %d more (fix these and run again)", e.More)
	}
	return out
}

// manifestProblemCap bounds the problems one refusal lists; the rest are counted.
const manifestProblemCap = 50

// Load reads the update manifest (SPEC-UPDATE rule 2) in every form: the
// legacy six-column TSV people write by hand, the update record's table wire,
// its padded standard base64, and the schema JSON text form (S1, S2). The
// legacy text is read as before (the header and # comment lines stay);
// every other form is read through pkg/schemaread with the generated
// UpdateManifest table, refused by name when malformed or clamped, never read
// as absent, empty or zero. Reads are counted per record and per form; the
// per-form counters are the reader's (S3, the evidence a cutover landed).
func Load(r io.Reader) ([]Entry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, &ManifestError{Problems: []string{"unreadable manifest (use lines below 1 MiB)"}}
	}
	if manifestRead.Detect(data) == schemaread.FormLegacy {
		manifestLegacyReads.Add(1)
		return loadLegacy(data)
	}
	doc, _, err := manifestRead.Read(data)
	if err != nil {
		return nil, err
	}
	return entriesFromTable(doc.v)
}

// manifestRead is the strict reader of the update manifest's table image, its
// padded standard base64, its schema JSON text and its legacy TSV, shared by
// every Load and by the dual-read test's per-form counters.
var manifestRead = schemaread.NewReader(manifestCodec())

// manifestLegacyReads counts reads of the legacy TSV, which Load routes to the
// legacy reader (its refusals are the manifest's own *ManifestError, not the
// schema reader's), so it is counted here rather than in manifestRead's.
var manifestLegacyReads atomic.Uint64

// loadLegacy reads the legacy six-column TSV people write by hand, with its
// header and # comment lines, exactly as Load always read it.
func loadLegacy(data []byte) ([]Entry, error) {
	r := strings.NewReader(string(data))
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	var problems []string
	add := func(line int, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("line %d: ", line)+fmt.Sprintf(format, args...))
	}
	if !sc.Scan() || sc.Text() != Header {
		add(1, "invalid header (put the header back exactly: %s)", tabbed(Header))
	}
	var out []Entry
	seen := map[string]bool{}
	line := 1
	for sc.Scan() {
		line++
		s := sc.Text()
		if strings.HasPrefix(s, "#") {
			continue
		}
		f := strings.Split(s, "\t")
		if len(f) != 6 {
			add(line, "%d fields, want 6 (use the six-column TSV header)", len(f))
			continue
		}
		before := len(problems)
		for i, v := range f {
			if v == "" {
				add(line, "empty %s field (supply all six fields; apply may be none)", strings.Split(Header, "\t")[i])
			}
		}
		if len(problems) > before {
			continue
		}
		if !kindValid(f[1]) {
			add(line, "unknown kind %s (use harness,engine,model,tool,pin)", f[1])
		}
		if seen[f[0]] {
			add(line, "duplicate name %s (give each entry a distinct name)", f[0])
		}
		seen[f[0]] = true
		e := Entry{Name: f[0], Kind: f[1], Latest: f[3], Owner: f[5]}
		var err error
		e.Installed, err = argv(f[2])
		if err != nil {
			add(line, "installed: %v", err)
		}
		// "-" is the field a snapshot leaves for a person to fill in, and it is the same
		// answer as "none": there is no command to apply an update with. It is not a
		// command named "-".
		if f[4] != "none" && f[4] != "-" {
			e.Apply, err = argv(f[4])
			if err != nil {
				add(line, "apply: %v", err)
			}
		}
		// A latest of "-" is NOT YET KNOWN, which is what a snapshot writes and what a
		// person promotes to a source by hand. It is read, reported as unknown, and never
		// guessed at; every other value is still a <scheme>:<locator> or a refusal.
		if e.Latest == "-" {
			out = append(out, e)
			continue
		}
		scheme, loc, ok := strings.Cut(e.Latest, ":")
		if !ok || loc == "" {
			add(line, "invalid latest (use github,npm,brew,ollama or local with a locator)")
			continue
		}
		switch scheme {
		case "github", "npm", "brew", "ollama":
		case "local":
			if _, err = argv(loc); err != nil {
				add(line, "latest: %v", err)
			}
		default:
			add(line, "unknown source %s (use github,npm,brew,ollama,local)", scheme)
			continue
		}
		if e.Kind == "pin" && scheme != "local" {
			add(line, "pin requires local source (name the depended-on version argv)")
		}
		if scheme == "ollama" && e.Kind != "model" {
			add(line, "ollama digest requires model kind (set kind=model)")
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		add(line, "unreadable manifest (use lines below 1 MiB)")
	}
	if len(problems) > 0 {
		me := &ManifestError{Problems: problems}
		if len(problems) > manifestProblemCap {
			me.Problems, me.More = problems[:manifestProblemCap], len(problems)-manifestProblemCap
		}
		return nil, me
	}
	return out, nil
}

// manDoc is one UpdateManifest value and the region that holds it: text and
// lists are self-relative references into that region, so the value is never
// copied out of it.
type manDoc struct {
	v      *wire.UpdateManifest
	region []byte
}

// manRegion is an aligned buffer of n bytes for a region load.
func manRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

// manReport reduces a generated TableReport to the strict read's report.
func manReport(r *wire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		Malformed:    r.Malformed || r.Duplicate > 0,
		Reason:       r.Reason,
	}
}

// manStr is a *string field's text; a zero slot is absent, not a value.
func manStr(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

// argvWords holds a list of words to argv (:29): non-empty, single-spaced.
func argvWords(c *schemaread.Checker, field string, l *wire.TableList[wire.Word], required bool) {
	if required {
		c.Required(field, l.Count > 0)
	}
	for i := int32(0); i < l.Count; i++ {
		w := manStr(&l.At(i).Text)
		c.Required(field+" word", w != "" && !strings.ContainsAny(w, " \r\n\t\"'"))
	}
}

// manifestEntryOf are the per-entry constraints Load enforces (S1, the six
// non-empty fields of manifest.go:86-98).
func manifestEntryOf(c *schemaread.Checker, v *wire.UpdateManifestEntry) {
	c.Required("name", manStr(&v.Name) != "")
	c.Required("kind", v.Kind != wire.ManifestKindNone)
	c.Enum("kind", int(v.Kind)-1, int(wire.ManifestKindCount))
	argvWords(c, "installed", &v.Installed, true)
	c.Required("latest", manStr(&v.Latest) != "")
	argvWords(c, "apply", &v.Apply, false)
	c.Required("owner", manStr(&v.Owner) != "")
}

// manifestConstraints are the table's constraints (manifest.go:95, duplicate
// name refused).
func manifestConstraints(v *wire.UpdateManifest, c *schemaread.Checker) {
	seen := map[string]bool{}
	ok := true
	for i := int32(0); i < v.Entries.Count; i++ {
		e := v.Entries.At(i)
		manifestEntryOf(c, e)
		n := manStr(&e.Name)
		ok = ok && !seen[n]
		seen[n] = true
	}
	c.Required("entries.name distinct", ok)
}

// manifestLegacy recognises the six-column TSV people write by hand. The
// manifest is the only one of the forms whose text is tab-separated: base64
// and JSON carry no tabs, and the binary wire leads with a control byte, so a
// printable text with a tab is the legacy form. A wrong header is still legacy
// text and still refused as the legacy form.
func manifestLegacy(in []byte) bool {
	s := bytes.TrimLeft(in, " \t\r\n")
	if len(s) == 0 || s[0] == '{' || s[0] < 0x20 || s[0] >= 0x80 {
		return false
	}
	return bytes.ContainsRune(s, '\t')
}

// manifestCodec is the update manifest's Codec for pkg/schemaread: the
// generated UpdateManifest table wire, its padded base64 and its JSON text,
// with the constraints Load enforces.
func manifestCodec() *schemaread.Codec[manDoc] {
	return &schemaread.Codec[manDoc]{
		Name:  "UpdateManifest",
		Help:  "put the manifest back in one of its readable forms and run again",
		Reset: func(d *manDoc) { *d = manDoc{} },
		Measure: func(w []byte) error {
			_, err := wire.UpdateManifestLoadMeasureReason(w)
			return err
		},
		LoadWire: func(d *manDoc, w []byte) schemaread.Report {
			n, err := wire.UpdateManifestLoadMeasureReason(w)
			if err != nil || n < 0 {
				rep := schemaread.Report{Malformed: true}
				if err != nil {
					rep.Reason = err.Error()
				}
				return rep
			}
			var r wire.TableReport
			d.region = manRegion(n)
			if d.v = wire.UpdateManifestLoad(d.region, w, &r); d.v == nil {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			return manReport(&r)
		},
		LoadJSON: func(d *manDoc, text []byte) schemaread.Report {
			var r wire.TableReport
			v, reg, ok := manifestFromJSON(text, &r)
			if !ok {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			d.v, d.region = v, reg
			return manReport(&r)
		},
		Legacy: manifestLegacy,
		Constraints: func(d *manDoc, c *schemaread.Checker) {
			c.Required("record", d.v != nil)
			if d.v != nil {
				manifestConstraints(d.v, c)
			}
		},
		SaveWire: func(d *manDoc) []byte {
			n := wire.UpdateManifestMeasure(d.v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if wire.UpdateManifestSave(d.v, buf) != n {
				return nil
			}
			return buf
		},
		SaveJSON: func(d *manDoc) []byte {
			n := wire.UpdateManifestToJsonMeasure(d.v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if wire.UpdateManifestToJson(d.v, buf) != n {
				return nil
			}
			return buf
		},
	}
}

// manifestFromJSON is an UpdateManifest built from JSON text by the generated
// reader and locked into its region.
func manifestFromJSON(text []byte, r *wire.TableReport) (*wire.UpdateManifest, []byte, bool) {
	b := &wire.UpdateManifestBuilder{}
	if !b.Init() || !wire.UpdateManifestFromJson(b, text, r) || !b.Lock() {
		return nil, nil, false
	}
	return b.AsConst(), b.Region(), true
}

// entriesFromTable is a loaded UpdateManifest value as the entries Load yields,
// with the latest-source validation the legacy form runs (manifest.go:156-176).
func entriesFromTable(v *wire.UpdateManifest) ([]Entry, error) {
	var out []Entry
	seen := map[string]bool{}
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	for i := int32(0); i < v.Entries.Count; i++ {
		e := v.Entries.At(i)
		en := Entry{Name: manStr(&e.Name), Kind: wire.EnumNameManifestKind(e.Kind), Latest: manStr(&e.Latest), Owner: manStr(&e.Owner)}
		if seen[en.Name] {
			add("duplicate name %s (give each entry a distinct name)", en.Name)
		}
		seen[en.Name] = true
		for j := int32(0); j < e.Installed.Count; j++ {
			en.Installed = append(en.Installed, manStr(&e.Installed.At(j).Text))
		}
		for j := int32(0); j < e.Apply.Count; j++ {
			en.Apply = append(en.Apply, manStr(&e.Apply.At(j).Text))
		}
		if en.Latest == "-" {
			out = append(out, en)
			continue
		}
		scheme, loc, ok := strings.Cut(en.Latest, ":")
		if !ok || loc == "" {
			add("invalid latest (use github,npm,brew,ollama or local with a locator)")
			continue
		}
		switch scheme {
		case "github", "npm", "brew", "ollama":
		case "local":
		default:
			add("unknown source %s (use github,npm,brew,ollama,local)", scheme)
			continue
		}
		if en.Kind == "pin" && scheme != "local" {
			add("pin requires local source (name the depended-on version argv)")
		}
		if scheme == "ollama" && en.Kind != "model" {
			add("ollama digest requires model kind (set kind=model)")
		}
		out = append(out, en)
	}
	if len(problems) > 0 {
		return nil, &ManifestError{Problems: problems}
	}
	return out, nil
}
