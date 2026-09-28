package main

// The 2026-09-25 defect: pr record --repo mas-bandwidth/rowan-tools wrote
// pr:mas-bandwidth/rowan-tools:375 and read post --repo rowan-tools read
// pr:rowan-tools:375, so READ POST REFUSED a record PR RECORD had just
// created. Both verbs now key the record through internal/nsprint/prkey:
// either spelling on either verb hits pr:<name>:<n>, and no owner-form key
// is ever written. Since #3595 read post writes through the line store, so
// its line is on the record's reads before pr lines adds the SCORE.

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func TestReadRefusesAConflictingOwner(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	code, _, errOut := runSprint("read", "post", "--repo", "mas-bandwidth/rowan-tools", "--owner", "someone-else",
		"--n", "3", "--line", "SCORE who=r head=abcdef0 score=9/10", "--no-github", "--redis", "127.0.0.1:1")
	if code != 2 || !strings.Contains(errOut, "names owner mas-bandwidth but --owner is someone-else") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	for _, bad := range []string{"a/b/c", "o/", "x:1"} {
		if code, _, errOut := runSprint("pr", "record", "--repo", bad, "--n", "1", "--redis", "127.0.0.1:1"); code != 2 {
			t.Fatalf("pr record --repo %q: exit %d %q", bad, code, errOut)
		}
	}
}
