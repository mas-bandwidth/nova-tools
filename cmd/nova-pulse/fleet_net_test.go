package main

// The CLI half of `fleet net`: the dispatch, the flags every verb refuses without, and the
// refusal a specified-but-unwritten verb prints. Nothing here reaches a machine or a tailnet.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFleetNetRefusesEveryUnwrittenVerbByNameWithItsRule: the stub is a pointer into the
// spec. A verb that is specified and not written says so, names its rule, and exits 2.
func TestFleetNetRefusesEveryUnwrittenVerbByNameWithItsRule(t *testing.T) {
	for _, verb := range []string{"acl", "ssh", "join", "expiry", "names", "share", "serve"} {
		var out, errb bytes.Buffer
		code := run([]string{"fleet", "net", verb}, &out, &errb, time.Now().UTC())
		if code != 2 {
			t.Errorf("fleet net %s exits %d, want 2", verb, code)
		}
		for _, want := range []string{"NET REFUSED verb=" + verb, "reason=not-implemented", "docs/SPEC-FLEET-NET.md"} {
			if !strings.Contains(errb.String(), want) {
				t.Errorf("fleet net %s refuses as %q; want %q on the line", verb, errb.String(), want)
			}
		}
		if out.Len() != 0 {
			t.Errorf("fleet net %s wrote to stdout: %q (a refusal goes to stderr)", verb, out.String())
		}
	}
}

// TestFleetNetRefusesNoSubVerbAndAnUnknownOne, each naming the set it could have been.
func TestFleetNetRefusesNoSubVerbAndAnUnknownOne(t *testing.T) {
	for _, args := range [][]string{{"fleet", "net"}, {"fleet", "net", "nonesuch"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb, time.Now().UTC()); code != 2 {
			t.Errorf("%v exits %d, want 2", args, code)
		}
		if !strings.Contains(errb.String(), "status") || !strings.Contains(errb.String(), "init") {
			t.Errorf("%v refuses without naming the sub-verbs: %q", args, errb.String())
		}
	}
}

// TestFleetNetStatusAndInitRefuseTheFlagsTheyNeed: every path and every name comes from a
// flag and none has a default, which is the fleet rule.
func TestFleetNetStatusAndInitRefuseTheFlagsTheyNeed(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"fleet", "net", "status"}, "--machines"},
		{[]string{"fleet", "net", "init"}, "--machines"},
		{[]string{"fleet", "net", "init", "--machines", "m.tsv"}, "--node"},
		{[]string{"fleet", "net", "init", "--machines", "m.tsv", "--node", "n"}, "--tailnet"},
		{[]string{"fleet", "net", "init", "--machines", "m.tsv", "--node", "n", "--tailnet", "t"}, "--owner"},
		{[]string{"fleet", "net", "init", "--machines", "m.tsv", "--node", "n", "--tailnet", "t", "--owner", "o"}, "--out"},
	} {
		var out, errb bytes.Buffer
		if code := run(c.args, &out, &errb, time.Now().UTC()); code != 2 {
			t.Errorf("%v exits %d, want 2", c.args, code)
		}
		if !strings.Contains(errb.String(), c.want) {
			t.Errorf("%v refuses as %q; want it to name %s", c.args, errb.String(), c.want)
		}
	}
}

// TestFleetNetInitThroughTheCLIWritesTheThreeFiles is the dogfood end of it: the verb a
// person types, over the example registry, writing into a temp directory.
func TestFleetNetInitThroughTheCLIWritesTheThreeFiles(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "net", "init",
		"--machines", filepath.Join("..", "..", "internal", "fleet", "testdata", "machines.tsv"),
		"--node", "rowan", "--tailnet", "example.com", "--owner", "someone@example.com",
		"--out", dir}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit %d; stderr=%q", code, errb.String())
	}
	for _, p := range []string{"fleet/tailnet-policy.hujson", ".github/workflows/tailnet-acl.yml", "docs/TAILNET.md"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s was not written: %v", p, err)
		}
	}
	if !strings.Contains(out.String(), "NET INIT OK node=rowan files=3") {
		t.Errorf("no verdict line: %q", out.String())
	}
}
