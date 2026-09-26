package land_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// TestRefusalTableWalk is the refusal-table walk of Issue #3139 rev 7 §8.1 (build B9): every
// refusal is a named line with a remedy, from one table. The walk checks the table itself (names
// unique, each example matched by its own row and no other, every remedy a command), then every
// REFUSED reason the land library (internal/nsprint/fn/lua/land.lua) can return and every reason
// the Go side of land/ names: each must resolve to exactly one row. A new refusal with no row
// fails here (fails on: an unnamed refusal, a refusal with no remedy).
func TestRefusalTableWalk(t *testing.T) {
	t.Parallel()

	rows := land.Refusals()
	if len(rows) < 20 {
		t.Fatalf("refusal table has %d rows; want every land refusal", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Name == "" || seen[r.Name] {
			t.Fatalf("row %+v: empty or duplicate name", r)
		}
		seen[r.Name] = true
		if !strings.HasPrefix(r.Remedy, "nova-sprint ") && !strings.HasPrefix(r.Remedy, "bench-conform ") {
			t.Errorf("row %s: remedy %q is not a command", r.Name, r.Remedy)
		}
		if r.Example == "" {
			t.Errorf("row %s: no example", r.Name)
			continue
		}
		got, ok := land.LookupRefusal(r.Example)
		if !ok || got.Name != r.Name {
			t.Errorf("row %s: example %q resolves to %q (ok=%v)", r.Name, r.Example, got.Name, ok)
		}
		line := land.RefusedLine(r.Example)
		if !strings.HasPrefix(line, "REFUSED "+r.Example+" remedy=") || strings.Contains(line, "{1}") || strings.Contains(line, "{2}") {
			t.Errorf("row %s: line %q", r.Name, line)
		}
	}

	// Every reason the land library returns resolves to one row.
	lua, err := os.ReadFile(filepath.Join("..", "fn", "lua", "land.lua"))
	if err != nil {
		t.Fatalf("read land.lua: %v", err)
	}
	reasons := luaRefusals(string(lua))
	if len(reasons) < 25 {
		t.Fatalf("found %d REFUSED reasons in land.lua; the extractor is broken", len(reasons))
	}
	for _, reason := range reasons {
		if _, ok := land.LookupRefusal(reason); !ok {
			t.Errorf("land.lua refusal %q has no row in refusals.go", reason)
		}
	}

	// The Go side's named refusals (publisher fences, benching, reinstate) resolve too.
	for _, reason := range []string{
		"stale", "nondeterministic b42", "inbound-stale since 1727200000",
		land.BenchedReason("studio"), land.ReinstateReason("studio"), land.NotBenchedReason("studio"),
	} {
		if _, ok := land.LookupRefusal(reason); !ok {
			t.Errorf("go refusal %q has no row", reason)
		}
	}

	// An unknown reason still prints a line with a remedy, and is reported unnamed.
	if _, ok := land.LookupRefusal("something new"); ok {
		t.Fatalf("an unknown reason resolved to a row")
	}
	if line := land.RefusedLine("something new"); line != "REFUSED something new remedy=nova-sprint land status" {
		t.Fatalf("unknown line %q", line)
	}
	if line := land.RefusedLine("hold on gh/mas-bandwidth/nova-tools/7"); line != "REFUSED hold on gh/mas-bandwidth/nova-tools/7 remedy=nova-sprint why gh/mas-bandwidth/nova-tools/7" {
		t.Fatalf("hold line %q", line)
	}
}

// luaRefusals returns one sample reason per REFUSED return in the land library: literal parts
// kept, every concatenated variable replaced by x1.
func luaRefusals(src string) []string {
	var exprs []string
	for _, m := range regexp.MustCompile(`\{ 'REFUSED', (.+?) \}`).FindAllStringSubmatch(src, -1) {
		exprs = append(exprs, m[1])
	}
	for _, m := range regexp.MustCompile(`(?m)'REFUSED ' \.\. (.+)$`).FindAllStringSubmatch(src, -1) {
		exprs = append(exprs, m[1])
	}
	// land_lease_refusal's own reasons (its callers return the variable).
	if i := strings.Index(src, "local function land_lease_refusal"); i >= 0 {
		body := src[i:]
		if j := strings.Index(body, "\nend\n"); j >= 0 {
			body = body[:j]
		}
		for _, m := range regexp.MustCompile(`return '([^']+)'`).FindAllStringSubmatch(body, -1) {
			exprs = append(exprs, "'"+m[1]+"'")
		}
	}
	var out []string
	for _, e := range exprs {
		e = strings.TrimSpace(e)
		if e == "refusal" {
			continue
		}
		// A multi-value return ({ 'REFUSED', 'inflight', tostring(inf) }, ns_writer) is one
		// reason: its values joined by a space (no REFUSED literal carries a comma).
		var b strings.Builder
		for i, elem := range strings.Split(e, ",") {
			if i > 0 {
				b.WriteString(" ")
			}
			for _, part := range strings.Split(elem, "..") {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "'") && strings.HasSuffix(part, "'") && len(part) >= 2 {
					b.WriteString(part[1 : len(part)-1])
				} else {
					b.WriteString("x1")
				}
			}
		}
		out = append(out, b.String())
	}
	return out
}
