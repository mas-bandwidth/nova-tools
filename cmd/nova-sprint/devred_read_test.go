package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestDevRedAndReadVerbsRefuseUsage: both verbs are registered and refuse
// a missing subverb or flag with exit 2 and the remedy on the line.
func TestDevRedAndReadVerbsRefuseUsage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"dev-red"}, "want status, check, watch or unwatch"},
		{[]string{"dev-red", "status"}, "needs --repo <r> and --base <b>"},
		{[]string{"dev-red", "bogus"}, "unknown subverb bogus"},
		{[]string{"read"}, "usage: nova-sprint read <brief|carry|digest|post>"},
		{[]string{"read", "carry", "--repo", "nova-tools", "--n", "7", "--redis", "127.0.0.1:1", "--line", "x"}, "takes no --line, --out or --no-github, and no --head; usage: nova-sprint read carry --repo <r> --n <n>"},
		{[]string{"read", "digest", "--repo", "mas-bandwidth/nova-tools/x", "--n", "7", "--redis", "127.0.0.1:1"}, "needs --repo <owner/name|name>"},
	}
	for _, tc := range cases {
		var out, errOut bytes.Buffer
		if code := run(tc.args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut.String(), tc.want)
		}
	}
}
