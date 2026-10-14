package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// The forms nova-sprint's own tlacheck had, kept when its copy became this one
// (nova-sprint runs this command now): the case of inputs as one argument, and a
// checkout with no tla/RUNS.tsv reading as one that has measured nothing.

func TestInputsTakesTheCaseAsItsOneArgument(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	flagged := call(t, nil, "inputs", "--root", root, "--case", "MCA.cfg")
	if flagged.code != 0 {
		t.Fatalf("%+v", flagged)
	}
	for _, name := range []string{"MCA.cfg", "MCA"} {
		if r := call(t, nil, "inputs", "--root", root, name); r.code != 0 || r.stdout != flagged.stdout || r.stderr != "" {
			t.Errorf("inputs %s: %+v", name, r)
		}
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a case not in the plan": {[]string{"inputs", "--root", root, "MCGhost.cfg"}, `no case "MCGhost.cfg"`},
		"two arguments":          {[]string{"inputs", "--root", root, "MCA.cfg", "MCAStale.cfg"}, "unexpected argument"},
		"an argument and --case": {[]string{"inputs", "--root", root, "--case", "MCA.cfg", "MCAStale.cfg"}, "unexpected argument"},
	} {
		if r := call(t, nil, tc.args...); r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, tc.want) {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestGroupsStaleWithNoRecordsFileIsEveryRequiredGroup(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	if _, err := os.Stat(filepath.Join(root, "tla", tlc.RunsFile)); !os.IsNotExist(err) {
		t.Fatalf("the fixture has a records file: %v", err)
	}
	recs, err := readRuns(root)
	if err != nil || recs != nil {
		t.Fatalf("readRuns of a checkout with no records = %v, %v; want none and no error", recs, err)
	}
	r := call(t, nil, "groups", "--root", root, "--stale")
	var groups []string
	if r.code != 0 || r.stderr != "" || json.Unmarshal([]byte(r.stdout), &groups) != nil || !reflect.DeepEqual(groups, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("groups --stale with no records (every group with a case, bench-gated ones too): %+v", r)
	}
	// A records file that is there but cannot be read is still refused.
	if err := os.Mkdir(filepath.Join(root, "tla", tlc.RunsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := call(t, nil, "groups", "--root", root, "--stale"); r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "cannot read the records") {
		t.Fatalf("an unreadable records file: %+v", r)
	}
}
