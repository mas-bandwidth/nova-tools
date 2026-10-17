package update

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

const adoptionHeader = "tool\tfriend\tstate\tversion\tdetail"

var adoptionStates = []string{"evaluated", "useful-now", "tried", "adopted", "declined", "deferred", "unknown", "equivalent"}

func adoptionValid(s string) bool {
	return slices.Contains(adoptionStates, s)
}

type adoption struct {
	Tool, Friend, State, Version, Detail string
}

// adoptionLedgerValue is one ledger as pkg/schemaread hands it back: the five
// fields of each row, whether it arrived as the table wire, the base64 of that
// wire, the schema JSON text or the legacy TSV people write.
type adoptionLedgerValue struct {
	rows []adoption
}

// adoptionLedger is the process's one strict reader of the adoption ledger.
var adoptionLedger = schemaread.NewReader(adoptionLedgerCodec())

// adoptionLedgerCodec is the adoption ledger's one table behind pkg/schemaread
// (S2: a reader accepts the binary image, its padded standard base64 and the
// schema JSON text, refuses damage by name and counts reads per form; the
// legacy TSV stays readable because people write these files by hand).
func adoptionLedgerCodec() *schemaread.Codec[adoptionLedgerValue] {
	return &schemaread.Codec[adoptionLedgerValue]{
		Name:  "AdoptionLedger",
		Help:  "nova-update adoption -h",
		Forms: []byte{1}, // the table wire's one form byte
		Reset: func(v *adoptionLedgerValue) { *v = adoptionLedgerValue{} },
		Measure: func(image []byte) error {
			_, err := releasewire.AdoptionLedgerLoadMeasureReason(image)
			return err
		},
		LoadWire: func(v *adoptionLedgerValue, image []byte) schemaread.Report {
			n, err := releasewire.AdoptionLedgerLoadMeasureReason(image)
			if err != nil || n < 0 {
				return schemaread.Report{Malformed: true}
			}
			region := adoptionRegion(n)
			var rep releasewire.TableReport
			ledger := releasewire.AdoptionLedgerLoad(region, image, &rep)
			if ledger == nil {
				return schemaread.Report{Malformed: true, Reason: rep.Reason}
			}
			v.rows = adoptionRowsOf(ledger)
			return adoptionReport(&rep)
		},
		LoadJSON: func(v *adoptionLedgerValue, text []byte) schemaread.Report {
			var b releasewire.AdoptionLedgerBuilder
			if !b.Init() {
				return schemaread.Report{Malformed: true}
			}
			var rep releasewire.TableReport
			if !releasewire.AdoptionLedgerFromJson(&b, text, &rep) || !b.Lock() {
				return schemaread.Report{Malformed: true, Reason: rep.Reason}
			}
			v.rows = adoptionRowsOf(b.AsConst())
			return adoptionReport(&rep)
		},
		Legacy:     adoptionLedgerLegacy,
		LoadLegacy: adoptionLoadLegacy,
		Constraints: func(v *adoptionLedgerValue, c *schemaread.Checker) {
			// loadAdoption requires five non-empty fields and a state of the
			// closed list; a variable table reads an absent string as zero, so
			// the reader refuses it here rather than returning empty.
			for _, r := range v.rows {
				c.Required("tool", r.Tool != "")
				c.Required("friend", r.Friend != "")
				c.Required("state", r.State != "")
				c.Required("version", r.Version != "")
				c.Required("detail", r.Detail != "")
			}
		},
	}
}

// adoptionLedgerLegacy recognises the hand-written TSV by its header or by a
// tab in otherwise text: damaged legacy still counts under the legacy form,
// not as base64 text.
func adoptionLedgerLegacy(in []byte) bool {
	if len(in) == 0 {
		return false
	}
	t := bytes.TrimLeft(in, " \t\r\n")
	if len(t) == 0 || t[0] == '{' {
		return false
	}
	if t[0] < 0x20 || t[0] >= 0x80 {
		return false // the binary wire leads with a control byte, not text
	}
	return bytes.HasPrefix(t, []byte("tool")) || bytes.ContainsRune(t, '\t')
}

// adoptionLoadLegacy parses the five-column TSV as loadAdoption always has,
// with its header and # comment lines (S2: the legacy form stays readable).
func adoptionLoadLegacy(v *adoptionLedgerValue, in []byte) schemaread.Report {
	sc := bufio.NewScanner(bytes.NewReader(in))
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() || sc.Text() != adoptionHeader {
		return schemaread.Report{Reason: fmt.Sprintf("line 1: invalid header (put the header back exactly: %s)", tabbed(adoptionHeader))}
	}
	line := 1
	for sc.Scan() {
		line++
		s := sc.Text()
		if strings.HasPrefix(s, "#") {
			continue
		}
		f := strings.Split(s, "\t")
		if len(f) != 5 {
			return schemaread.Report{Reason: fmt.Sprintf("line %d: %d fields, want 5 (use the five-column TSV header)", line, len(f))}
		}
		if f[0] == "" || f[1] == "" || f[2] == "" || f[3] == "" || f[4] == "" {
			return schemaread.Report{Reason: fmt.Sprintf("line %d: empty field (supply all five fields; version and detail may be -)", line)}
		}
		if !adoptionValid(f[2]) {
			return schemaread.Report{Reason: fmt.Sprintf("line %d: unknown state %s (use evaluated,useful-now,tried,adopted,declined,deferred,unknown,equivalent)", line, f[2])}
		}
		v.rows = append(v.rows, adoption{Tool: f[0], Friend: f[1], State: f[2], Version: f[3], Detail: f[4]})
	}
	if err := sc.Err(); err != nil {
		return schemaread.Report{Reason: fmt.Sprintf("line %d: unreadable adoption file (use lines below 1 MiB)", line)}
	}
	return schemaread.Report{}
}

// adoptionRowsOf copies the loaded table's rows into the ledger's five text
// fields; the region the table points into is not held past the read.
func adoptionRowsOf(ledger *releasewire.AdoptionLedger) []adoption {
	out := make([]adoption, 0, ledger.Rows.Count)
	for i := int32(0); i < ledger.Rows.Count; i++ {
		row := ledger.Rows.At(i)
		out = append(out, adoption{
			Tool:    adoptionText(&row.Tool),
			Friend:  adoptionText(&row.FriendName),
			State:   adoptionStateText(row.State),
			Version: adoptionText(&row.Version),
			Detail:  adoptionText(&row.Detail),
		})
	}
	return out
}

// adoptionText reads one self-relative string slot.
func adoptionText(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	s := releasewire.TableStringAt(slot)
	return string(s.Data[:s.Length])
}

// adoptionStateText names a state the way the legacy file spells it; a value
// outside the closed list names nothing and is refused as absent, never read
// as a zero choice.
func adoptionStateText(state releasewire.AdoptionState) string {
	if state < 1 || state > releasewire.AdoptionStateCount {
		return ""
	}
	return adoptionStates[state-1]
}

// adoptionReport turns the generated report into the strict read's: a key
// given twice in the JSON text is ambiguous and is malformed, never last-wins.
func adoptionReport(r *releasewire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		Malformed:    r.Malformed || r.Duplicate > 0,
		Reason:       r.Reason,
	}
}

// adoptionRegion is an aligned buffer of n bytes for a region load.
func adoptionRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

// loadAdoption reads the adoption ledger through pkg/schemaread: its binary
// image, the padded standard base64 of that image, the schema JSON text and
// the legacy TSV all read as one value, and damage is a named refusal (S2,
// S4 readers first, UNION.md C13).
func loadAdoption(r io.Reader) ([]adoption, error) {
	return loadAdoptionThrough(adoptionLedger, r)
}

// loadAdoptionThrough is loadAdoption with the reader handed in, so a test
// counts its own reads per form without the process's other reads.
func loadAdoptionThrough(reader *schemaread.Reader[adoptionLedgerValue], r io.Reader) ([]adoption, error) {
	in, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("unreadable adoption file: %w", err)
	}
	v, _, err := reader.Read(in)
	if err != nil {
		return nil, err
	}
	return v.rows, nil
}

// adoptionVerb reads the adoption ledger and lists each choice: its one value is
// the count on the first line and an item per choice, the choice's detail its prose.
func adoptionVerb(name string, args []string, out, errs io.Writer) int {
	o := struct {
		file, as string
		max      int
		json     bool
	}{max: 20}
	f := flag.NewFlagSet("adoption", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.file, "file", "", "the adoption ledger (required): "+adoptionShape)
	f.StringVar(&o.as, "as", "", "list only this friend's choices")
	f.IntVar(&o.max, "max", 20, "choices listed before one MORE line stands for the rest; 0 lists all")
	f.BoolVar(&o.json, "json", false, "print the result as one JSON object instead of lines")
	help := name + " adoption -h"
	if err := verbflag.Parse(f, interspersed(f, args)); err != nil {
		return emit(refused("adoption", help, flagProblem(f, err).Error()), verbflag.BoolAsked(args, "json"), 0, out, errs)
	}
	// Every problem of the invocation in one refusal (STANDARD §2).
	var problems []string
	if o.file == "" {
		problems = append(problems, "missing --file; refusing to guess")
	}
	if o.max < 0 {
		problems = append(problems, fmt.Sprintf("--max wants 0 or more (0 shows all), got %d", o.max))
	}
	if len(f.Args()) != 0 {
		problems = append(problems, fmt.Sprintf("adoption takes no positional arguments, got %q", f.Arg(0)))
	}
	if len(problems) > 0 {
		return emit(refused("adoption", help, strings.Join(problems, "; ")), o.json, 0, out, errs)
	}
	file, err := os.Open(o.file)
	if err != nil {
		return emit(refused("adoption", help, fmt.Sprintf("cannot open %s (supply a readable --file: %s)", o.file, adoptionShape)), o.json, 0, out, errs)
	}
	rows, err := loadAdoption(file)
	_ = file.Close() // ignored: file was opened only to be read
	if err != nil {
		return emit(refused("adoption", help, fmt.Sprintf("%s: %s", o.file, err)), o.json, 0, out, errs)
	}
	selected := []adoption{}
	for _, r := range rows {
		if o.as == "" || r.Friend == o.as {
			selected = append(selected, r)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].Tool == selected[j].Tool {
			return selected[i].Friend < selected[j].Friend
		}
		return selected[i].Tool < selected[j].Tool
	})
	res := &tool.Out{Verb: "adoption", Status: tool.OK}
	friends := map[string]bool{}
	for _, r := range selected {
		friends[r.Friend] = true
		res.Item("choice", "tool", r.Tool, "friend", r.Friend, "state", r.State, "version", r.Version, "detail", r.Detail)
	}
	res.Fact("entries", len(selected)).Fact("friends", len(friends)).Fact("file", o.file).Fact("max", o.max)
	return emit(res, o.json, o.max, out, errs)
}

// adoptionShape is what the file adoption --file names holds.
const adoptionShape = "one line per tool choice, five tab-separated fields tool friend state version detail, written by hand"
