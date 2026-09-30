package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// init --members <name>:<width> and fleet up <m> --width <n> set a member's
// width; where shows it in the fleet table's width column beside working, and
// --json carries it; a width that is not one is refused before anything is
// written.
func TestTheWidthIsSetByInitAndFleetUpAndShown(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:64,m2:3,m3")
	ta.ok("fleet up m3 --width 8")
	out := ta.ok("where")
	if !strings.Contains(out, "fleet | ready | working | width |") {
		t.Fatalf("the fleet table has no width beside working:\n%s", out)
	}
	var v struct {
		Tables map[string]map[string]map[string]string `json:"tables"`
	}
	if err := json.Unmarshal([]byte(ta.ok("where --json")), &v); err != nil {
		t.Fatal(err)
	}
	fleet := v.Tables["fleet"]
	if fleet["m1"]["width"] != "64" || fleet["m2"]["width"] != "3" || fleet["m3"]["width"] != "8" {
		t.Fatalf("the widths: %v", fleet)
	}
	for _, line := range []string{"fleet up m4 --width 0", "fleet up m4 --width 1025", "fleet up m4 --width x"} {
		if code, _, errs := ta.do(line); code == 0 || !strings.Contains(errs, "width") {
			t.Errorf("%s: exit %d, %s", line, code, errs)
		}
	}
	nb := newTestApp(t)
	if code, _, errs := nb.do("init --members m1:0"); code == 0 || !strings.Contains(errs, "width") {
		t.Errorf("init --members m1:0: exit %d, %s", code, errs)
	}
}
