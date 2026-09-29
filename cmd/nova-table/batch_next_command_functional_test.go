//go:build functional

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// shellSplit splits a command line the way a shell does for the single-quote
// form the refusals use: words split on blanks, 'a b' keeps its blank, and
// a backslash escapes the next byte outside quotes.
func shellSplit(line string) []string {
	var words []string
	var cur strings.Builder
	inWord, quoted := false, false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case quoted:
			if ch == '\'' {
				quoted = false
			} else {
				cur.WriteByte(ch)
			}
		case ch == '\'':
			quoted, inWord = true, true
		case ch == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
			inWord = true
		case ch == ' ' || ch == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// nextCommand returns the words of the command after the last "; run: ".
func nextCommand(t *testing.T, refusal string) []string {
	t.Helper()
	_, cmd, ok := strings.Cut(refusal, "; run: ")
	if !ok {
		t.Fatalf("no next command in %q", refusal)
	}
	cmd = strings.TrimSpace(cmd)
	if strings.ContainsAny(cmd, "<>") {
		t.Fatalf("the next command holds a placeholder: %q", cmd)
	}
	words := shellSplit(cmd)
	if len(words) < 2 || words[0] != "nova-table" {
		t.Fatalf("next command %q is not a nova-table command", cmd)
	}
	return words[1:]
}

// withStore places --redis <addr> straight after the verb words, where the real
// dispatcher's flag parser reads it, whatever follows.
func withStore(t *testing.T, addr string, words []string) []string {
	t.Helper()
	best := 0
	for _, c := range commands {
		w := strings.Fields(c.name)
		if len(w) > len(words) || len(w) < best {
			continue
		}
		if strings.Join(words[:len(w)], " ") == c.name {
			best = len(w)
		}
	}
	if best == 0 {
		t.Fatalf("no verb in %q", words)
	}
	out := append([]string{}, words[:best]...)
	out = append(out, "--redis", addr)
	return append(out, words[best:]...)
}

func runsWhenPasted(t *testing.T, addr, name, refusal string) {
	t.Helper()
	words := nextCommand(t, refusal)
	code, stdout, stderr := runTable(withStore(t, addr, words)...)
	if code != 0 || stderr != "" || stdout == "" {
		t.Errorf("%s: %q exit %d\n stdout %q\n stderr %q", name, strings.Join(words, " "), code, stdout, stderr)
	}
}

// Each refusal of `nova-table batch`, and of a read set, ends in a command
// that the real verb parser accepts and the store runs.
func TestBatchRefusalNextCommandsRun(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working,done")
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"build", "test"} {
		if _, err := ntable.RowAdd(ctx, c, "demo", r, ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
	}
	code, _, stderr := runTable("batch", "--redis", addr, `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"seed","members":[{"id":"a","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1},"set":{"role":"x"}}]}`)
	if code != 0 {
		t.Fatalf("seed: %d %s", code, stderr)
	}
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	manifest := func(table, epoch, revision, op, members string) string {
		return `{"schema":1,"table":"` + table + `","epoch":"` + epoch + `","expected_table_revision":"` + revision + `","operation_id":"` + op + `","members":[` + members + `]}`
	}
	cases := []struct{ name, manifest string }{
		{"not a member", manifest("demo", "0", rev, "n1", `{"id":"zz","expect":{},"move":{"row":"build","col":"done"}}`)},
		{"not a member, hyphen id", manifest("demo", "0", rev, "n1b", `{"id":"-zz","expect":{},"move":{"row":"build","col":"done"}}`)},
		{"quote in id", manifest("demo", "0", rev, "n1c", `{"id":"it's","expect":{}}`)},
		{"member exists", manifest("demo", "0", rev, "n2", `{"id":"a","expect":{"absent":true},"create":{"row":"build","col":"done","score":1}}`)},
		{"epoch ahead", manifest("demo", "3", rev, "n3", `{"id":"a","expect":{}}`)},
		{"place guard", manifest("demo", "0", rev, "n4", `{"id":"a","expect":{"place":{"row":"test","col":"done"}}}`)},
		{"member revision", manifest("demo", "0", rev, "n5", `{"id":"a","expect":{"revision":"9"}}`)},
		{"table revision", manifest("demo", "0", "1", "n6", `{"id":"a","expect":{}}`)},
		{"field guard", manifest("demo", "0", rev, "n8", `{"id":"a","expect":{"fields":{"role":{"equals":"y"}}}}`)},
		{"unknown row", manifest("demo", "0", rev, "n9", `{"id":"n","expect":{"absent":true},"create":{"row":"nope","col":"ready","score":1}}`)},
		{"unknown column", manifest("demo", "0", rev, "n10", `{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"nope","score":1}}`)},
		{"reserved field", manifest("demo", "0", rev, "n11", `{"id":"a","expect":{},"set":{"epoch":"5"}}`)},
		{"missing table", manifest("ghost", "0", "0", "n12", `{"id":"a","expect":{}}`)},
	}
	for _, tc := range cases {
		code, stdout, stderr := runTable("batch", "--redis", addr, tc.manifest)
		if code != 1 || stdout != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want a refusal", tc.name, code, stdout, stderr)
			continue
		}
		if !strings.Contains(stderr, "changed=no") {
			t.Errorf("%s: no changed=no: %s", tc.name, stderr)
		}
		runsWhenPasted(t, addr, tc.name, stderr)
	}

	reads := []struct {
		name  string
		scope ntable.ReadSetScope
		epoch []uint64
	}{
		{"read set epoch ahead", ntable.ReadSetScope{Members: []string{"a"}}, []uint64{3}},
		{"read set unknown row", ntable.ReadSetScope{Selection: []ntable.CellSelection{{Row: "no-such-row", Col: "ready"}}}, nil},
		{"read set unknown column", ntable.ReadSetScope{Selection: []ntable.CellSelection{{Row: "build", Col: "nope"}}}, nil},
	}
	for _, tc := range reads {
		_, err := ntable.ReadSet(ctx, c, "demo", tc.scope, tc.epoch...)
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "changed=no") {
			t.Errorf("%s: no changed=no: %v", tc.name, err)
		}
		runsWhenPasted(t, addr, tc.name, err.Error())
	}
}
