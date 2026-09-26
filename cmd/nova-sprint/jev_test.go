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
)

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
