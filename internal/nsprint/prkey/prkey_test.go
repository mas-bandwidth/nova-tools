package prkey_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
)

func TestBothSpellingsNameOneKey(t *testing.T) {
	t.Parallel()

	for _, repo := range []string{"rowan-tools", "mas-bandwidth/rowan-tools", " rowan-tools "} {
		if got := prkey.Key(repo, 375); got != "pr:rowan-tools:375" {
			t.Fatalf("Key(%q, 375) = %q, want pr:rowan-tools:375", repo, got)
		}
		if got := prkey.KeyText(repo, "375"); got != "pr:rowan-tools:375" {
			t.Fatalf("KeyText(%q) = %q", repo, got)
		}
	}
}

func TestSplit(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, owner, name string }{
		{"rowan-tools", prkey.DefaultOwner, "rowan-tools"},
		{"mas-bandwidth/nova-tools", "mas-bandwidth", "nova-tools"},
		{"ctl-org/ctl-repo", "ctl-org", "ctl-repo"},
	}
	for _, c := range cases {
		o, n, err := prkey.Split(c.in)
		if err != nil || o != c.owner || n != c.name {
			t.Fatalf("Split(%q) = %q, %q, %v; want %q, %q", c.in, o, n, err, c.owner, c.name)
		}
		if full, _ := prkey.Full(c.in); full != c.owner+"/"+c.name {
			t.Fatalf("Full(%q) = %q", c.in, full)
		}
	}
	for _, bad := range []string{"", "/x", "o/", "a/b/c", "o/r:1", "o r"} {
		if _, _, err := prkey.Split(bad); err == nil {
			t.Fatalf("Split(%q) accepted", bad)
		}
	}
}
