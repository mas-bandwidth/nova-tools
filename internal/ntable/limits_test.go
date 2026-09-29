package ntable

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
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
}
