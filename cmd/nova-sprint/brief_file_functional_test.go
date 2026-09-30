//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// add --brief-file on a real store: a brief of many paragraphs, read from a
// file, is stored byte for byte with its one trailing newline cut, and the
// packets of queue --json and take --json carry it whole.
func TestAddBriefFileOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	run := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := a.run(args, &out, &errb); code != 0 {
			t.Fatalf("%v: %d %s%s", args, code, out.String(), errb.String())
		}
		return out.String()
	}
	// the card lint's passing brief, then the paragraphs under test
	brief := passingBrief("Fix the empty case.") + "\nThen:\n\t- keep the tab\n  - keep the indent, \"quotes\", 'ticks', ünï\n"
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, []byte(brief+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("init", "--readers", "reader-a,reader-b", "--members", "m1")
	// a brief that fails the card lint is refused on the store, exit 2, and writes nothing
	bare := filepath.Join(t.TempDir(), "bare.md")
	if err := os.WriteFile(bare, []byte("Fix the empty case.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := a.run([]string{"add", "--stream", "s0", "--count", "1", "--brief-file", bare}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "LINT DRIFT brief rule-gocache") || out.Len() != 0 {
		t.Fatalf("a brief without the child rules: exit %d, out %q, err %q; want exit 2 with the lint's lines", code, out.String(), errb.String())
	}
	run("add", "--stream", "s1", "--count", "1", "--brief-file", path)
	run("fleet", "beat", "m1")
	run("start")
	run("tick")
	var q struct{ Cards []queueCard }
	if err := json.Unmarshal([]byte(run("queue", "--as", "m1", "--json")), &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Cards) != 1 || q.Cards[0].Packet == nil || q.Cards[0].Packet.Brief != brief {
		t.Fatalf("queue --json: %+v", q.Cards)
	}
	var took struct {
		Packets []struct{ Brief string }
	}
	if err := json.Unmarshal([]byte(run("take", "--as", "m1", "--limit", "1", "--json")), &took); err != nil {
		t.Fatal(err)
	}
	if len(took.Packets) != 1 || took.Packets[0].Brief != brief {
		t.Fatalf("take --json: %+v", took.Packets)
	}
}
