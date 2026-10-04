//go:build functional

package main

import (
	"context"
	"github.com/redis/go-redis/v9"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.True(t, ok, "no next command in %q", refusal)
	cmd = strings.TrimSpace(cmd)
	require.False(t, strings.ContainsAny(cmd, "<>"), "the next command holds a placeholder: %q", cmd)
	words := shellSplit(cmd)
	require.GreaterOrEqual(t, len(words), 2, "next command %q is not a nova-table command", cmd)
	require.Equal(t, "nova-table", words[0], "next command %q is not a nova-table command", cmd)
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
	require.NotEqualValues(t, 0, best, "no verb in %q", words)
	out := append([]string{}, words[:best]...)
	out = append(out, "--redis", addr)
	return append(out, words[best:]...)
}

func runsWhenPasted(t *testing.T, addr, name, refusal string) {
	t.Helper()
	words := nextCommand(t, refusal)
	code, stdout, stderr := runTable(withStore(t, addr, words)...)
	assert.EqualValues(t, 0, code, "%s: %q exit %d\n stdout %q\n stderr %q", name, strings.Join(words, " "), code, stdout, stderr)
	assert.Empty(t, stderr, "%s: %q exit %d\n stdout %q\n stderr %q", name, strings.Join(words, " "), code, stdout, stderr)
	assert.NotEmpty(t, stdout, "%s: %q exit %d\n stdout %q\n stderr %q", name, strings.Join(words, " "), code, stdout, stderr)
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
	require.NoError(t, err, "%v", err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()))
	for _, r := range []string{"build", "test"} {
		{
			_, err := ntable.RowAdd(ctx, c, "demo", r, ntable.RowSpec{})
			require.NoError(t, err, "%v", err)
		}
	}
	code, _, stderr := runTable("batch", "--redis", addr, `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"seed","members":[{"id":"a","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1},"set":{"role":"x"}}]}`)
	require.EqualValues(t, 0, code, "seed: %d %s", code, stderr)
	{
		_, err := ntable.RowAdd(ctx, c, "demo", "bnd", ntable.RowSpec{Binds: map[string]string{"ready": "ext:ready"}, Owner: "o"})
		require.NoError(t, err, "%v", err)
	}
	require.NoError(t, c.Set(ctx, ntable.MemberKey("junk"), "s", 0).Err())
	require.NoError(t, c.ZAdd(ctx, ntable.CellKey("demo", "test", "done"), redis.Z{Score: 1, Member: "ghost"}).Err())
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
		{"missing table", manifest("ghost", "0", "0", "n12", `{"id":"a","expect":{}}`)},
		{"bound cell", manifest("demo", "0", rev, "n13", `{"id":"n","expect":{"absent":true},"create":{"row":"bnd","col":"ready","score":1}}`)},
		{"wrong type member", manifest("demo", "0", rev, "n14", `{"id":"junk","expect":{}}`)},
		{"hidden placement", manifest("demo", "0", rev, "n15", `{"id":"ghost","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)},
		{"operation id holds another request", manifest("demo", "0", rev, "seed", `{"id":"a","expect":{}}`)},
		{"member id that starts with a hyphen and holds a quote", manifest("demo", "0", rev, "n16", `{"id":"-it's","expect":{"revision":"9"}}`)},
	}
	for _, tc := range cases {
		code, stdout, stderr := runTable("batch", "--redis", addr, tc.manifest)
		if code != 1 || stdout != "" {
			assert.EqualValues(t, 1, code, "%s: exit %d, stdout %q, stderr %q; want a refusal", tc.name, code, stdout, stderr)
			assert.Empty(t, stdout, "%s: refusal wrote stdout: %s", tc.name, stdout)
			continue
		}
		assert.Contains(t, stderr, "changed=no", "%s: no changed=no: %s", tc.name, stderr)
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
			assert.Error(t, err, "%s: accepted", tc.name)
			continue
		}
		assert.Contains(t, err.Error(), "changed=no", "%s: no changed=no: %v", tc.name, err)
		runsWhenPasted(t, addr, tc.name, err.Error())
	}
}

// A manifest over a bound is a refusal: exit 1, named, bound and count found,
// changed=no, and a next command that runs; the input is not echoed.
func TestBatchCLIOverBoundIsARefusal(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	fields := make([]string, ntable.LimitSetFields+1)
	for i := range fields {
		fields[i] = `"zzq` + strings.Repeat("f", i%7) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":"v"`
	}
	manifest := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"0","operation_id":"over","members":[{"id":"m","expect":{},"set":{` + strings.Join(fields, ",") + `}}]}`
	code, stdout, stderr := runTable("batch", "--redis", addr, manifest)
	require.EqualValues(t, 1, code, "exit %d stdout %q stderr %q; want a refusal, exit 1", code, stdout, stderr)
	require.Empty(t, stdout, "exit %d stdout %q stderr %q; want a refusal, exit 1", code, stdout, stderr)
	for _, w := range []string{"limit exceeded: set fields per member: bound 128, observed 129", `member "m"`, "changed=no"} {
		assert.Contains(t, stderr, w, "refusal lacks %q: %s", w, stderr)
	}
	assert.NotContains(t, stderr, "zzq", "refusal echoes the input: %s", stderr)
	runsWhenPasted(t, addr, "over bound", stderr)
}

// A refusal for a member of an older epoch ends in a command that shows the
// member: read at the active epoch it would repeat the refusal, so the command
// reads the member at the epoch it belongs to.
func TestBatchMemberEpochNextCommandReadsTheMemberAtItsOwnEpoch(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working")
	require.NoError(t, err, "%v", err)
	tb := ntable.Table{Name: "demo", Columns: cols, EpochKey: "epochs", EpochField: "n"}
	require.NoError(t, ntable.Create(ctx, c, tb, time.Now()))
	{
		_, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
		require.NoError(t, err, "%v", err)
	}
	{
		_, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "old", 1, ntable.WriteOptions{Epoch: 0})
		require.NoError(t, err, "%v", err)
	}
	require.NoError(t, c.HSet(ctx, "epochs", "n", 1).Err())
	{
		_, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}, ntable.WriteOptions{Epoch: 1})
		require.NoError(t, err, "%v", err)
	}
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	manifest := `{"schema":1,"table":"demo","epoch":"1","expected_table_revision":"` + rev + `","operation_id":"me1","members":[{"id":"old","expect":{}}]}`
	code, stdout, stderr := runTable("batch", "--redis", addr, manifest)
	require.EqualValues(t, 1, code, "exit %d stdout %q stderr %q; want a MEMBEREPOCH refusal", code, stdout, stderr)
	require.Empty(t, stdout, "exit %d stdout %q stderr %q; want a MEMBEREPOCH refusal", code, stdout, stderr)
	require.Contains(t, stderr, "MEMBEREPOCH", "exit %d stdout %q stderr %q; want a MEMBEREPOCH refusal", code, stdout, stderr)
	require.Contains(t, stderr, "changed=no", "exit %d stdout %q stderr %q; want a MEMBEREPOCH refusal", code, stdout, stderr)
	words := strings.Join(nextCommand(t, stderr), " ")
	assert.Contains(t, words, "member read", "the next command %q does not read the member at its own epoch", words)
	assert.Contains(t, words, "--at-epoch 0", "the next command %q does not read the member at its own epoch", words)
	runsWhenPasted(t, addr, "member epoch", stderr)
}

// A refusal for a row the table lacks ends in a command that shows the table,
// not one that writes to it.
func TestBatchNoRowNextCommandDoesNotWrite(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready")
	require.NoError(t, err, "%v", err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()))
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	manifest := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"nr1","members":[{"id":"n","expect":{"absent":true},"create":{"row":"nope","col":"ready","score":1}}]}`
	code, _, stderr := runTable("batch", "--redis", addr, manifest)
	require.EqualValues(t, 1, code, "exit %d stderr %q; want a NOROW refusal", code, stderr)
	require.Contains(t, stderr, "NOROW", "exit %d stderr %q; want a NOROW refusal", code, stderr)
	{
		next := strings.Join(nextCommand(t, stderr), " ")
		assert.NotContains(t, next, "row add", "the next command %q writes to the table or does not show it", next)
		assert.True(t, strings.HasPrefix(next, "show "), "the next command %q writes to the table or does not show it", next)
	}
	runsWhenPasted(t, addr, "no row", stderr)
	{
		n := c.ZCard(ctx, ntable.DefKey("demo")+":rows").Val()
		assert.EqualValues(t, 0, n, "running the next command left %d rows", n)
	}
}
