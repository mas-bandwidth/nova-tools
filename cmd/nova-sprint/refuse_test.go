package main

import (
	"strings"
	"testing"
)

// storeRefusal gives each store class one remedy and one exit code; a login
// the store refused keeps the help door (its cause carries the #3520 hint).
func TestStoreRefusalClasses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cause string
		code  int
		want  []string
	}{
		{"task done t1: ERR Function not found", 2, []string{"nova_sprint function library is not loaded", "run: nova-sprint fn load --redis "}},
		{"ns_ws_counts: NOPERM User audit has no permissions to run the 'fcall_ro' command", 6, []string{"denies ACL user audit", "run: nova-sprint acl check --redis "}},
		{"connect redis: dial tcp 10.0.0.5:6379: connect: connection refused", 6, []string{"10.0.0.5:6379 did not answer", "run: nova-sprint doctor --redis 10.0.0.5:6379"}},
		{"redis at 127.0.0.1:1: NOAUTH Authentication required.", 2, []string{"run: nova-sprint help"}},
		{"--sprint is required", 2, []string{"run: nova-sprint help"}},
	}
	for _, tc := range cases {
		line, code := storeRefusal(tc.cause)
		if code != tc.code || !strings.HasPrefix(line, tc.cause+"; ") {
			t.Errorf("%q: exit %d line %q, want %d and the cause first", tc.cause, code, line, tc.code)
		}
		for _, w := range tc.want {
			if !strings.Contains(line, w) {
				t.Errorf("%q: line %q lacks %q", tc.cause, line, w)
			}
		}
	}
}
