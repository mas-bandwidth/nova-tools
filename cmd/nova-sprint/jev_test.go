package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// TestJevMechRecordsOneLine: jev mech reads the record, the mirror diff and
// the body, appends one JEV line to the record's reads, does not append the
// same line twice, and exits 1 with the remedy when a pass fails.
func TestJevMechRecordsOneLine(t *testing.T) {
	mirror, addr, head := readFixture(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	base := readGit(t, mirror, "rev-parse", "dev")
	// The stream lander's record: reads lives on pr:nova-tools:3.
	c.HSet(ctx, "pr:nova-tools:3", "repo", "mas-bandwidth/nova-tools", "n", "3", "reads", "SCORE who=rowan head="+head+" score=9/10")
	dir := t.TempDir()
	write := func(name, s string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("good.md", "BASE: dev\nbase-sha: "+base+"\nPATHS: a.txt\nDEPENDS-ON: none\nDONE-WHEN: go test passes\nSTREAM: s\nCloses #9\n\nwhat and why\n")
	bad := write("bad.md", "BASE: dev\nbase-sha: "+base+"\nPATHS: b.txt\nDEPENDS-ON: -\nDONE-WHEN: go test passes\nCloses #9\n")
	run := func(body string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runJev(ctx, []string{"mech", "--repo", "mas-bandwidth/nova-tools", "--n", "3", "--body-file", body, "--mirror", mirror, "--redis", addr}, &out, &errOut)
		return code, out.String(), errOut.String()
	}
	reads := func() []string { return strings.Split(c.HGet(ctx, "pr:nova-tools:3", "reads").Val(), "\n") }

	code, out, errOut := run(good)
	if code != 0 || !strings.HasPrefix(out, "JEV RECORDED pr:nova-tools:3 head="+head[:8]+" gate=ok lint=ok scope=ok base=ok") {
		t.Fatalf("good: exit %d out %q err %q", code, out, errOut)
	}
	r := reads()
	if len(r) != 2 || r[1] != "JEV who=jev pass=mech head="+head+" gate=ok lint=ok scope=ok base=ok why=-" {
		t.Fatalf("reads %q", r)
	}
	if code, out, _ = run(good); code != 0 || !strings.HasPrefix(out, "JEV SAME ") || len(reads()) != 2 {
		t.Fatalf("again: exit %d out %q reads %d", code, out, len(reads()))
	}
	code, out, _ = run(bad)
	if code != 1 || !strings.Contains(out, "gate=fail lint=fail scope=fail base=ok") || !strings.Contains(out, "a.txt outside PATHS") ||
		!strings.Contains(out, "missing STREAM:") || !strings.Contains(out, "remedy=fix the lint, scope and push") {
		t.Fatalf("bad: exit %d out %q", code, out)
	}
	if r = reads(); len(r) != 3 || !strings.HasPrefix(r[2], "JEV who=jev pass=mech head="+head+" gate=fail ") {
		t.Fatalf("reads after bad %q", r)
	}
}

// TestJevMechRefusals: usage is exit 2 before Redis; no record is exit 1
// naming pr record.
func TestJevMechRefusals(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	t.Setenv("NOVA_REDIS_ADDR", "")
	mr := miniredis.RunT(t)
	body := filepath.Join(t.TempDir(), "b.md")
	if err := os.WriteFile(body, []byte("BASE: dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{},
		{"lint"},
		{"mech", "--repo", "nova-tools", "--n", "3", "--redis", mr.Addr()},
		{"mech", "--repo", "a/b/c", "--n", "3", "--body-file", body, "--redis", mr.Addr()},
		{"mech", "--repo", "nova-tools", "--n", "0", "--body-file", body, "--redis", mr.Addr()},
		{"mech", "--repo", "nova-tools", "--n", "3", "--body-file", body},
		{"mech", "--repo", "nova-tools", "--n", "3", "--body-file", body + ".none", "--redis", mr.Addr()},
	} {
		var out, errOut bytes.Buffer
		if code := runJev(context.Background(), args, &out, &errOut); code != 2 || out.Len() != 0 {
			t.Errorf("%v: exit %d out %q, want 2", args, code, out.String())
		}
	}
	var out, errOut bytes.Buffer
	code := runJev(context.Background(), []string{"mech", "--repo", "nova-tools", "--n", "3", "--body-file", body, "--redis", mr.Addr()}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "JEV REFUSED pr:nova-tools:3 why=no record with a head; remedy: nova-sprint pr record") {
		t.Fatalf("no record: exit %d err %q", code, errOut.String())
	}
}
