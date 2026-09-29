package ntable

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// luaLimits reads T.limits and T.limit_names from table.lua: name -> value.
func luaLimits(t *testing.T) map[string]int {
	t.Helper()
	src, err := os.ReadFile("../nsprint/fn/lua/table.lua")
	if err != nil {
		t.Fatal(err)
	}
	block := func(head string) string {
		i := strings.Index(string(src), head)
		if i < 0 {
			t.Fatalf("table.lua has no %s", head)
		}
		rest := string(src)[i:]
		return rest[:strings.Index(rest, "\n  }")]
	}
	values := map[string]int{}
	for _, m := range regexp.MustCompile(`(\w+)\s*=\s*(\d+)`).FindAllStringSubmatch(block("T.limits = {"), -1) {
		values[m[1]], _ = strconv.Atoi(m[2])
	}
	out := map[string]int{}
	for _, m := range regexp.MustCompile(`(\w+)\s*=\s*'([^']+)'`).FindAllStringSubmatch(block("T.limit_names = {"), -1) {
		v, ok := values[m[1]]
		if !ok {
			t.Fatalf("table.lua names a limit %s that has no value", m[1])
		}
		out[m[2]] = v
	}
	if len(out) != len(values) {
		t.Fatalf("table.lua: %d limit values, %d names", len(values), len(out))
	}
	return out
}

func specLimits(t *testing.T) map[string]int {
	t.Helper()
	src, err := os.ReadFile("../../docs/SPEC-NOVA-TABLE.md")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, m := range regexp.MustCompile(`(?m)^\| ([a-z_0-9 -]+) \| (\d+) \|$`).FindAllStringSubmatch(string(src), -1) {
		out[m[1]], _ = strconv.Atoi(m[2])
	}
	return out
}

// The bounds of a batch are one set: the Lua server, the Go validator and the
// specification carry the same names and numbers.
func TestBatchBoundsAgreeAcrossServerValidatorAndSpec(t *testing.T) {
	t.Parallel()
	goSide := map[string]int{
		limitNameManifest:     LimitManifestBytes,
		limitNameChanged:      LimitChangedEntries,
		limitNameGuardEntries: LimitGuardEntries,
		limitNameMemberID:     LimitMemberIDBytes,
		limitNameFieldValue:   LimitFieldValueBytes,
		limitNameSet:          LimitSetFields,
		limitNameUnset:        LimitUnsetFields,
		limitNameGuards:       LimitFieldGuards,
		limitNameOneOf:        LimitOneOfOptions,
		limitNameReadSet:      LimitReadSetMembers,
		limitNameColumns:      LimitColumns,
		limitNameRows:         LimitRows,
		limitNameReceipt:      LimitReceiptBytes,
		limitNameBatchValues:  LimitBatchValueBytes,
	}
	for label, side := range map[string]map[string]int{"table.lua": luaLimits(t), "SPEC-NOVA-TABLE.md": specLimits(t)} {
		if len(side) != len(goSide) {
			t.Errorf("%s states %d bounds, the validator %d: %v", label, len(side), len(goSide), side)
		}
		for name, want := range goSide {
			if got, ok := side[name]; !ok || got != want {
				t.Errorf("%s: %s = %d (present %v), the validator holds %d", label, name, got, ok, want)
			}
		}
	}
}

// The receipt's value bound is one number in the server and in the library.
func TestReceiptValueBoundAgreesBetweenServerAndLibrary(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../nsprint/fn/lua/table.lua")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`T\.receipt_value_bytes = (\d+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("table.lua has no T.receipt_value_bytes")
	}
	if got, _ := strconv.Atoi(string(m[1])); got != ReceiptValueBytes {
		t.Errorf("table.lua %d, limits.go %d", got, ReceiptValueBytes)
	}
	if ReceiptValueBytes != 64 {
		t.Errorf("a receipt holds a value of at most 64 bytes in full, limits.go says %d", ReceiptValueBytes)
	}
}

// The batch section of the specification says what is: no process, no gate, no
// design brief, and no second copy of the document under another name.
func TestSpecBatchSectionDescribesWhatIs(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../docs/SPEC-NOVA-TABLE.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	i := strings.Index(text, "## Batched member read and conditional write")
	if i < 0 {
		t.Fatal("no batch section")
	}
	for _, phrase := range []string{" gate", "mini-quack", "card manager", "card layer", "Use the existing", "Tests must", "must pin", "Extend the", "The design must", "resumes"} {
		if strings.Contains(text[i:], phrase) {
			t.Errorf("the batch section holds %q: it describes process, not what is", phrase)
		}
	}
	if _, err := os.Lstat("../../docs/SPEC-TABLE.md"); err == nil {
		t.Errorf("docs/SPEC-TABLE.md exists: a second name for SPEC-NOVA-TABLE.md that nothing needs")
	}
}

// receiptSizeFunction is T.receipt_size as table.lua holds it, cut out of the file.
func receiptSizeFunction(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("../nsprint/fn/lua/table.lua")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)  function T\.receipt_size\(.*?\n  end\n`).Find(src)
	if m == nil {
		t.Fatal("table.lua has no T.receipt_size")
	}
	return string(m)
}

// The size a batch's receipt will have is computed before the first write: the
// delta as it will be encoded, every score read back after the writes counted at
// its longest form, and the delta left as it was found. The function runs here as
// the script holds it, on a stand-in encoder that prints the fields the receipt
// grows with.
func TestReceiptSizeCountsEachLateScoreAtItsLongestAndRestoresIt(t *testing.T) {
	t.Parallel()
	L := lua.NewState()
	defer L.Close()
	script := `
T = {score_text_bytes = ` + strconv.Itoa(scoreTextBytes(t)) + `}
` + receiptSizeFunction(t) + `
local a = {delta = {after_score = '1'}}
local b = {delta = {after_score = nil}}
local c = {delta = {after_score = '0.30000000000000004'}}
local late = {a, b, c}
local seen
local function encode()
  local n = 0
  for _, l in ipairs(late) do n = n + #(l.delta.after_score or '') end
  seen = n
  return string.rep('.', 100 + n)
end
size = T.receipt_size(encode, late)
restored = a.delta.after_score == '1' and b.delta.after_score == nil and c.delta.after_score == '0.30000000000000004'
none = T.receipt_size(function() return string.rep('.', 7) end, {})
`
	if err := L.DoString(script); err != nil {
		t.Fatal(err)
	}
	// three late scores, none of them shorter than the longest a score prints
	want := 100 + 3*scoreTextBytes(t)
	if got := int(L.GetGlobal("size").(lua.LNumber)); got != want {
		t.Errorf("size %d, want %d", got, want)
	}
	if L.GetGlobal("restored") != lua.LTrue {
		t.Errorf("a score was not put back")
	}
	if got := int(L.GetGlobal("none").(lua.LNumber)); got != 7 {
		t.Errorf("a delta with no late score is its own length: %d", got)
	}
}

// scoreTextBytes is T.score_text_bytes; the store prints a score with %.17g, and
// the longest such text is that of the most negative finite double.
func scoreTextBytes(t *testing.T) int {
	t.Helper()
	src, err := os.ReadFile("../nsprint/fn/lua/table.lua")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`T\.score_text_bytes = (\d+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("table.lua has no T.score_text_bytes")
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

func TestScoreTextBoundHoldsEveryScoreTheStorePrints(t *testing.T) {
	t.Parallel()
	longest := 0
	for _, f := range []float64{-math.MaxFloat64, math.MaxFloat64, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, 0.30000000000000004, -1.5e-300} {
		if n := len(fmt.Sprintf("%.17g", f)); n > longest {
			longest = n
		}
	}
	if got := scoreTextBytes(t); got < longest {
		t.Errorf("T.score_text_bytes %d is shorter than a score the store prints (%d)", got, longest)
	}
}

// The receipt's bound is the manifest's: one MiB, in the server, the library and
// the specification (TestBatchBoundsAgreeAcrossServerValidatorAndSpec compares
// the three); a batch that fits its manifest bound can still exceed it.
func TestReceiptBoundIsTheManifestBound(t *testing.T) {
	t.Parallel()
	if LimitReceiptBytes != 1<<20 {
		t.Errorf("receipt bound %d, manifest bound %d", LimitReceiptBytes, LimitManifestBytes)
	}
}

// A count that stopped where it passed its bound says its size is at least that.
func TestLimitErrorSaysAtLeastForAStoppedCount(t *testing.T) {
	t.Parallel()
	exact := (&LimitError{Name: "receipt bytes", Bound: 10, Observed: 12}).Error()
	least := (&LimitError{Name: "receipt bytes", Bound: 10, Observed: 12, AtLeast: true}).Error()
	if !strings.Contains(exact, "observed 12") || strings.Contains(exact, "at least") {
		t.Errorf("exact: %s", exact)
	}
	if !strings.Contains(least, "observed at least 12") {
		t.Errorf("stopped count: %s", least)
	}
}
