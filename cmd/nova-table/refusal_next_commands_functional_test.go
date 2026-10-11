//go:build functional

package main

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"

	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeDump is every key of a store, dumped, to compare before and after.
func storeDump(c *redis.Client) map[string]string {
	ctx := context.Background()
	out := map[string]string{}
	for _, k := range c.Keys(ctx, "*").Val() {
		out[k] = c.Dump(ctx, k).Val()
	}
	return out
}

// nextWrites runs the command a refusal ends in and requires that it runs and
// writes nothing.
func nextWrites(t *testing.T, c *redis.Client, addr, name, refusal string) {
	t.Helper()
	_, cmd, ok := strings.Cut(refusal, "; run: ")
	if !ok {
		assert.True(t, ok, "%s: no next command in %q", name, refusal)
		return
	}
	args := shellSplit(strings.TrimSpace(cmd))[1:]
	if args[0] != "help" {
		args = withStore(t, addr, args)
	}
	before := storeDump(c)
	code, _, stderr := runTable(args...)
	assert.EqualValues(t, 0, code, "%s: %q exits %d: %s", name, cmd, code, stderr)
	{
		after := storeDump(c)
		assert.Equal(t, before, after, "%s: the next command %q wrote", name, cmd)
	}
}

// Every refusal of the round ends in a command that runs and writes nothing: a
// receipt over its bound, a column list or a bind over its bound, an ordinary
// write with an epoch ahead, a pre-send refusal, a row add past the bound.
func TestRefusalsEndInACommandThatRunsAndWritesNothing(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, _ := ntable.ParseColumns("ready,working,done")
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()))
	for _, r := range []string{"build", "test"} {
		{
			_, err := ntable.RowAdd(ctx, c, "demo", r, ntable.RowSpec{})
			require.NoError(t, err, "%v", err)
		}
	}
	rev := func() string { return c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val() }
	man := func(op, members string) string {
		return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev() + `","operation_id":"` + op + `","members":[` + members + `]}`
	}

	// a receipt over its bound, through the command
	var creates, ents, names []string
	for i := 0; i < 12; i++ {
		creates = append(creates, fmt.Sprintf(`{"id":"m%d","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`, i))
	}
	{
		code, _, stderr := runTable("batch", "--redis", addr, man("seed", strings.Join(creates, ",")))
		require.EqualValues(t, 0, code, "%v", stderr)
	}
	for j := 0; j < 1000; j++ {
		names = append(names, fmt.Sprintf(`"f%d"`, j))
	}
	for i := 0; i < 12; i++ {
		f := map[string]any{}
		for j := 0; j < 1000; j++ {
			f[fmt.Sprintf("f%d", j)] = strings.Repeat("v", 64)
		}
		c.HSet(ctx, ntable.MemberKey(fmt.Sprintf("m%d", i)), f)
		ents = append(ents, fmt.Sprintf(`{"id":"m%d","expect":{},"unset":[%s]}`, i, strings.Join(names, ",")))
	}
	before := storeDump(c)
	code, stdout, stderr := runTable("batch", "--redis", addr, man("big", strings.Join(ents, ",")))
	require.EqualValues(t, 1, code, "receipt over its bound: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.Empty(t, stdout, "receipt over its bound: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.Contains(t, stderr, "receipt bytes", "receipt over its bound: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.Equal(t, storeDump(c), before, "receipt over its bound: exit %d stdout %q stderr %q", code, stdout, stderr)
	nextWrites(t, c, addr, "receipt bytes", stderr)

	// create and set --columns past the column bound
	var many []string
	for i := 0; i <= ntable.LimitColumns; i++ {
		many = append(many, fmt.Sprintf("c%d", i))
	}
	code, _, stderr = runTable("create", "wide", "--columns", strings.Join(many, ","), "--redis", addr)
	assert.EqualValues(t, 1, code, "create 1001 columns: exit %d", code)
	nextWrites(t, c, addr, "create columns", stderr)
	code, _, stderr = runTable("set", "demo", "--columns", strings.Join(many, ","), "--redis", addr)
	assert.EqualValues(t, 1, code, "set --columns 1001: exit %d", code)
	nextWrites(t, c, addr, "set columns", stderr)

	// an ordinary write with an epoch ahead
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "x", 1, ntable.WriteOptions{Epoch: 5}); err == nil {
		assert.Error(t, err, "an ordinary write with an epoch ahead was accepted")
	} else {
		nextWrites(t, c, addr, "epoch ahead", err.Error())
	}

	// a bind past the row bound, through the library: changed=no and a next command
	rows := make([]ntable.Row, ntable.LimitRows+1)
	for i := range rows {
		rows[i] = ntable.Row{Key: fmt.Sprintf("r%d", i), Cells: make([]ntable.Cell, 3)}
	}
	err := ntable.Bind(ctx, c, ntable.Table{Name: "demo", Columns: cols, Rows: rows}, time.Now())
	require.Error(t, err, "bind past the row bound: %v; want a refusal saying changed=no", err)
	require.Contains(t, err.Error(), "changed=no", "bind past the row bound: %v; want a refusal saying changed=no", err)
	nextWrites(t, c, addr, "bind rows", err.Error())

	// a pre-send refusal through the command
	var fs []string
	for i := 0; i <= ntable.LimitSetFields; i++ {
		fs = append(fs, fmt.Sprintf(`"k%d":"v"`, i))
	}
	code, _, stderr = runTable("batch", "--redis", addr, man("pre", `{"id":"m0","expect":{},"set":{`+strings.Join(fs, ",")+`}}`))
	assert.EqualValues(t, 1, code, "pre-send: exit %d %q", code, stderr)
	assert.Contains(t, stderr, ntable.CheckedBeforeSending, "pre-send: exit %d %q", code, stderr)
	nextWrites(t, c, addr, "pre-send limit", stderr)

	// row add past the bound
	pipe := c.Pipeline()
	for i := 0; i < ntable.LimitRows-2; i++ {
		pipe.ZAdd(ctx, ntable.DefKey("demo")+":rows", redis.Z{Score: float64(i + 10), Member: fmt.Sprintf("h%d", i)})
	}
	{
		_, err := pipe.Exec(ctx)
		require.NoError(t, err, "%v", err)
	}
	code, _, stderr = runTable("row", "add", "demo", "one-more", "--redis", addr)
	assert.EqualValues(t, 1, code, "row add past the bound: exit %d", code)
	nextWrites(t, c, addr, "row add past the bound", stderr)
}
