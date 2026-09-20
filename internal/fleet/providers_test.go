package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProviderRegistry_ValidTSVParsing verifies parsing of TSV tables containing:
// - Header rows
// - Comment rows starting with '#'
// - Empty and whitespace lines
// - Optional/empty notes and dash '-' notes
func TestProviderRegistry_ValidTSVParsing(t *testing.T) {
	tsv := `# Provider routing table for nova fleet
# route	provider	model	tier	concurrency	cost_per_mtoken	error_threshold	notes

route	provider	model	tier	concurrency	cost_per_mtoken	error_threshold	notes

deepseek-direct	deepseek	deepseek-v4-flash	flash	10	0.14	0.20	direct api
opencode-deepseek	opencode	deepseek-v4-flash	flash	5	0.00	0.15	free tier on opencode
openrouter-deepseek	openrouter	deepseek/deepseek-chat	any	8	0.50	0.25	-
mercury-pro	mercury	mercury-2.5	pro	4	1.25	0.10	high priority pro route
`
	reg, err := ParseProviderRegistry(strings.NewReader(tsv))
	if err != nil {
		t.Fatalf("ParseProviderRegistry failed: %v", err)
	}

	routes := reg.Routes()
	if len(routes) != 4 {
		t.Fatalf("got %d routes, want 4", len(routes))
	}

	// Verify route 1: deepseek-direct
	r1, ok := reg.Lookup("deepseek-direct")
	if !ok {
		t.Fatal("lookup deepseek-direct failed")
	}
	if r1.Provider != "deepseek" || r1.Model != "deepseek-v4-flash" || r1.Tier != "flash" {
		t.Errorf("r1 unexpected metadata: %+v", r1)
	}
	if r1.Concurrency != 10 || r1.CostPerMToken != 0.14 || r1.ErrorThreshold != 0.20 {
		t.Errorf("r1 unexpected numeric fields: Concurrency=%d, Cost=%f, ErrorThreshold=%f",
			r1.Concurrency, r1.CostPerMToken, r1.ErrorThreshold)
	}
	if r1.Notes != "direct api" {
		t.Errorf("r1 notes = %q, want 'direct api'", r1.Notes)
	}

	// Verify route 2: opencode-deepseek (cost 0.00)
	r2, ok := reg.Lookup("opencode-deepseek")
	if !ok {
		t.Fatal("lookup opencode-deepseek failed")
	}
	if r2.CostPerMToken != 0.00 {
		t.Errorf("r2 cost = %f, want 0.00", r2.CostPerMToken)
	}

	// Verify route 3: dash note becomes empty string
	r3, ok := reg.Lookup("openrouter-deepseek")
	if !ok {
		t.Fatal("lookup openrouter-deepseek failed")
	}
	if r3.Notes != "" {
		t.Errorf("r3 notes = %q, want empty string for '-'", r3.Notes)
	}
	if r3.Tier != "any" {
		t.Errorf("r3 tier = %q, want 'any'", r3.Tier)
	}

	// Verify route 4: mercury-pro
	r4, ok := reg.Lookup("mercury-pro")
	if !ok {
		t.Fatal("lookup mercury-pro failed")
	}
	if r4.Tier != "pro" || r4.Concurrency != 4 || r4.CostPerMToken != 1.25 || r4.ErrorThreshold != 0.10 {
		t.Errorf("r4 unexpected fields: %+v", r4)
	}
}

// TestProviderRegistry_CostOrderingAndTieBreaking tests that AvailableRoutes:
// - Orders free/cheapest routes first (ascending CostPerMToken)
// - Breaks ties deterministically by route name alphabetically
func TestProviderRegistry_CostOrderingAndTieBreaking(t *testing.T) {
	tsv := `route	provider	model	tier	concurrency	cost_per_mtoken	error_threshold	notes
route-expensive	p1	m1	any	10	2.50	0.20	-
route-free-z	p2	m2	any	10	0.00	0.20	-
route-mid-b	p3	m3	any	10	0.50	0.20	-
route-free-a	p4	m4	any	10	0.00	0.20	-
route-mid-a	p5	m5	any	10	0.50	0.20	-
`
	reg, err := ParseProviderRegistry(strings.NewReader(tsv))
	if err != nil {
		t.Fatalf("ParseProviderRegistry failed: %v", err)
	}

	routes := reg.AvailableRoutes("any", nil, nil)
	if len(routes) != 5 {
		t.Fatalf("got %d routes, want 5", len(routes))
	}

	expectedOrder := []string{
		"route-free-a",    // cost 0.00, name 'a'
		"route-free-z",    // cost 0.00, name 'z'
		"route-mid-a",     // cost 0.50, name 'a'
		"route-mid-b",     // cost 0.50, name 'b'
		"route-expensive", // cost 2.50
	}

	for i, want := range expectedOrder {
		if routes[i].Route != want {
			t.Errorf("route[%d] = %q, want %q", i, routes[i].Route, want)
		}
	}
}

// TestProviderRegistry_ConcurrencyCeiling tests that routes at or exceeding
// their Concurrency limit are excluded from AvailableRoutes.
func TestProviderRegistry_ConcurrencyCeiling(t *testing.T) {
	tsv := `route	provider	model	tier	concurrency	cost_per_mtoken	error_threshold	notes
route-cap-2	p1	m1	any	2	0.10	0.50	-
route-cap-5	p2	m2	any	5	0.20	0.50	-
route-cap-0	p3	m3	any	0	0.05	0.50	disabled route
`
	reg, err := ParseProviderRegistry(strings.NewReader(tsv))
	if err != nil {
		t.Fatalf("ParseProviderRegistry failed: %v", err)
	}

	// Case 1: route-cap-2 has 1 in flight (under ceiling), route-cap-5 has 5 (at ceiling)
	inFlight := map[string]int{
		"route-cap-2": 1,
		"route-cap-5": 5,
	}
	avail := reg.AvailableRoutes("any", inFlight, nil)
	if len(avail) != 1 || avail[0].Route != "route-cap-2" {
		t.Fatalf("inFlight={2:1, 5:5}: want [route-cap-2], got %v", avail)
	}

	// Case 2: route-cap-2 has 2 in flight (at ceiling)
	inFlight["route-cap-2"] = 2
	avail = reg.AvailableRoutes("any", inFlight, nil)
	if len(avail) != 0 {
		t.Fatalf("inFlight={2:2, 5:5}: want empty, got %v", avail)
	}

	// Case 3: route-cap-2 has 3 in flight (above ceiling)
	inFlight["route-cap-2"] = 3
	avail = reg.AvailableRoutes("any", inFlight, nil)
	if len(avail) != 0 {
		t.Fatalf("inFlight={2:3, 5:5}: want empty, got %v", avail)
	}

	// Case 4: route-cap-5 has 4 in flight (under ceiling)
	inFlight["route-cap-5"] = 4
	avail = reg.AvailableRoutes("any", inFlight, nil)
	if len(avail) != 1 || avail[0].Route != "route-cap-5" {
		t.Fatalf("inFlight={2:3, 5:4}: want [route-cap-5], got %v", avail)
	}

	// Case 5: route-cap-0 has concurrency 0 (never available)
	avail = reg.AvailableRoutes("any", nil, nil)
	for _, r := range avail {
		if r.Route == "route-cap-0" {
			t.Fatal("route-cap-0 with Concurrency=0 must not be available")
		}
	}
}

// TestProviderRegistry_ErrorThresholdCircuitBreaker tests that routes whose errorRate
// meets or exceeds ErrorThreshold are tripped and excluded.
func TestProviderRegistry_ErrorThresholdCircuitBreaker(t *testing.T) {
	tsv := `route	provider	model	tier	concurrency	cost_per_mtoken	error_threshold	notes
route-strict	p1	m1	any	10	0.10	0.15	15% threshold
route-lenient	p2	m2	any	10	0.20	0.50	50% threshold
`
	reg, err := ParseProviderRegistry(strings.NewReader(tsv))
	if err != nil {
		t.Fatalf("ParseProviderRegistry failed: %v", err)
	}

	// No errors: both available
	avail := reg.AvailableRoutes("any", nil, nil)
	if len(avail) != 2 {
		t.Fatalf("no errors: want 2 routes, got %d", len(avail))
	}

	// 10% error rate on route-strict (below 15% threshold): both available
	errorRate := map[string]float64{
		"route-strict": 0.10,
	}
	avail = reg.AvailableRoutes("any", nil, errorRate)
	if len(avail) != 2 {
		t.Fatalf("rate=0.10 < 0.15: want 2 routes, got %d", len(avail))
	}

	// Exactly 15% error rate on route-strict: trips threshold (0.15 >= 0.15)
	errorRate["route-strict"] = 0.15
	avail = reg.AvailableRoutes("any", nil, errorRate)
	if len(avail) != 1 || avail[0].Route != "route-lenient" {
		t.Fatalf("rate=0.15 >= 0.15: want [route-lenient], got %v", avail)
	}

	// 20% error rate on route-strict: trips threshold
	errorRate["route-strict"] = 0.20
	avail = reg.AvailableRoutes("any", nil, errorRate)
	if len(avail) != 1 || avail[0].Route != "route-lenient" {
		t.Fatalf("rate=0.20 >= 0.15: want [route-lenient], got %v", avail)
	}

	// Both trip
	errorRate["route-lenient"] = 0.55
	avail = reg.AvailableRoutes("any", nil, errorRate)
	if len(avail) != 0 {
		t.Fatalf("both tripped: want 0 routes, got %d", len(avail))
	}
}

// TestProviderRegistry_TierFiltering tests filtering between flash, pro, and any tiers.
func TestProviderRegistry_TierFiltering(t *testing.T) {
	tsv := `route	provider	model	tier	concurrency	cost_per_mtoken	error_threshold	notes
flash-route	p1	m1	flash	10	0.10	0.20	-
pro-route	p2	m2	pro	10	1.00	0.20	-
universal-route	p3	m3	any	10	0.50	0.20	-
`
	reg, err := ParseProviderRegistry(strings.NewReader(tsv))
	if err != nil {
		t.Fatalf("ParseProviderRegistry failed: %v", err)
	}

	// 1. Requested tier: "flash" -> should include "flash-route" and "universal-route"
	availFlash := reg.AvailableRoutes("flash", nil, nil)
	if len(availFlash) != 2 {
		t.Fatalf("tier=flash: want 2 routes, got %d", len(availFlash))
	}
	if availFlash[0].Route != "flash-route" || availFlash[1].Route != "universal-route" {
		t.Fatalf("tier=flash unexpected order/routes: %v, %v", availFlash[0].Route, availFlash[1].Route)
	}

	// 2. Requested tier: "pro" -> should include "universal-route" and "pro-route" (sorted by cost)
	availPro := reg.AvailableRoutes("pro", nil, nil)
	if len(availPro) != 2 {
		t.Fatalf("tier=pro: want 2 routes, got %d", len(availPro))
	}
	// universal-route (cost 0.50) < pro-route (cost 1.00)
	if availPro[0].Route != "universal-route" || availPro[1].Route != "pro-route" {
		t.Fatalf("tier=pro unexpected order/routes: %v, %v", availPro[0].Route, availPro[1].Route)
	}

	// 3. Requested tier: "any" or "" -> should include all three routes
	availAny := reg.AvailableRoutes("any", nil, nil)
	if len(availAny) != 3 {
		t.Fatalf("tier=any: want 3 routes, got %d", len(availAny))
	}
	availEmpty := reg.AvailableRoutes("", nil, nil)
	if len(availEmpty) != 3 {
		t.Fatalf("tier='': want 3 routes, got %d", len(availEmpty))
	}
}

// TestProviderRegistry_ReadFromFile tests ReadProviderRegistry with file on disk.
func TestProviderRegistry_ReadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.tsv")

	content := `route	provider	model	tier	concurrency	cost_per_mtoken	error_threshold	notes
route-file	prov	mod	flash	5	0.12	0.20	file test
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := ReadProviderRegistry(path)
	if err != nil {
		t.Fatalf("ReadProviderRegistry failed: %v", err)
	}
	if reg.Path() != path {
		t.Errorf("Path() = %q, want %q", reg.Path(), path)
	}
	r, ok := reg.Lookup("route-file")
	if !ok || r.Route != "route-file" {
		t.Fatalf("route-file not found or wrong: %+v", r)
	}

	// Missing file returns an error
	_, err = ReadProviderRegistry(filepath.Join(dir, "nonexistent.tsv"))
	if err == nil {
		t.Fatal("ReadProviderRegistry on nonexistent file should fail")
	}
}

// TestProviderRegistry_MalformedTSV verifies error handling for invalid TSV content.
func TestProviderRegistry_MalformedTSV(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"too few fields", "route1\tprov\tmod\tflash\t5\t0.10\n"},
		{"too many fields", "route1\tprov\tmod\tflash\t5\t0.10\t0.20\tnotes\textra\n"},
		{"missing route", "\tprov\tmod\tflash\t5\t0.10\t0.20\t-\n"},
		{"missing provider", "r1\t\tmod\tflash\t5\t0.10\t0.20\t-\n"},
		{"missing model", "r1\tprov\t\tflash\t5\t0.10\t0.20\t-\n"},
		{"unknown tier", "r1\tprov\tmod\tultra\t5\t0.10\t0.20\t-\n"},
		{"negative concurrency", "r1\tprov\tmod\tflash\t-1\t0.10\t0.20\t-\n"},
		{"non-numeric concurrency", "r1\tprov\tmod\tflash\tfive\t0.10\t0.20\t-\n"},
		{"negative cost", "r1\tprov\tmod\tflash\t5\t-0.10\t0.20\t-\n"},
		{"non-numeric cost", "r1\tprov\tmod\tflash\t5\tfree\t0.20\t-\n"},
		{"negative error threshold", "r1\tprov\tmod\tflash\t5\t0.10\t-0.20\t-\n"},
		{"non-numeric error threshold", "r1\tprov\tmod\tflash\t5\t0.10\thigh\t-\n"},
		{"duplicate route", "r1\tp1\tm1\tflash\t5\t0.10\t0.20\t-\nr1\tp2\tm2\tpro\t5\t0.50\t0.20\t-\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProviderRegistry(strings.NewReader(tc.body))
			if err == nil {
				t.Fatalf("%s: expected error, got nil", tc.name)
			}
		})
	}
}
