package pulse

// The forge read was ONE PAGE, and the fleet is three pages long.
//
// A non-author ran `fleet certify --all` from the Studio on 2026-09-18 and got
// `registry-truth` and `runner-online` FAIL on five of eight machines, each saying "the forge
// names no online <machine>-nova-*". Every one of those machines was serving runners at that
// moment. The forge answered `total_count: 95` and a `runners` array of THIRTY -- GitHub's
// default page -- and the thirty were hulk's 22, batman's 6 and air's 2, which is exactly the
// three machines that passed.
//
// This is not a certification bug. `pulse.GHRunners` is what the reaper reads too, so every
// runner past the thirtieth has been invisible to every tool here.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH puts a `gh` on PATH that answers the runner list, and records the argv it was given
// so a test can hold the read against the way the API is actually paged.
func fakeGHRunnerList(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "gh.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\ncat <<'NOVA_GH_BODY'\n" + body + "\nNOVA_GH_BODY\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// TestTheRunnerListIsReadWholeAndNotOnePage: every page, and the argv says so.
func TestTheRunnerListIsReadWholeAndNotOnePage(t *testing.T) {
	// What `gh api --paginate ... --jq '.runners[]'` prints: one object per line, pages
	// concatenated, which is why this cannot be parsed as one JSON array.
	var lines []string
	for _, name := range []string{"hulk-nova-1", "vision-nova-1", "space-nova-1", "mini-nova-1", "superman-nova-1"} {
		lines = append(lines, `{"id":1,"name":"`+name+`","status":"online","busy":false}`)
	}
	log := fakeGHRunnerList(t, strings.Join(lines, "\n"))
	runners, err := GHRunners{}.Runners("mas-bandwidth/nova-tools")
	if err != nil {
		t.Fatal(err)
	}
	if len(runners) != len(lines) {
		t.Fatalf("read %d runners, want %d: a page is not the fleet", len(runners), len(lines))
	}
	seen := map[string]bool{}
	for _, r := range runners {
		seen[r.Name] = true
	}
	for _, want := range []string{"mini-nova-1", "superman-nova-1"} {
		if !seen[want] {
			t.Errorf("%s is not in the list; it sits past the first page", want)
		}
	}
	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--paginate", "per_page=100"} {
		if !strings.Contains(string(argv), want) {
			t.Errorf("the forge read does not pass %s:\n%s", want, argv)
		}
	}
}

// TestASinglePageAnswerStillParses: a gh that hands back one JSON array (the shape before
// --paginate, and what a stubbed gh in another test gives) must keep working.
func TestASinglePageAnswerStillParses(t *testing.T) {
	fakeGHRunnerList(t, `[{"id":1,"name":"hulk-nova-1","status":"online","busy":false}]`)
	runners, err := GHRunners{}.Runners("mas-bandwidth/nova-tools")
	if err != nil {
		t.Fatal(err)
	}
	if len(runners) != 1 || runners[0].Name != "hulk-nova-1" {
		t.Fatalf("runners = %+v", runners)
	}
}

// TestAnEmptyForgeAnswerIsNoRunnersAndNotAnError.
func TestAnEmptyForgeAnswerIsNoRunnersAndNotAnError(t *testing.T) {
	fakeGHRunnerList(t, "")
	runners, err := GHRunners{}.Runners("mas-bandwidth/nova-tools")
	if err != nil {
		t.Fatal(err)
	}
	if len(runners) != 0 {
		t.Fatalf("runners = %+v", runners)
	}
}
