package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// cost attach prices a record that ended with no tokens (docs/SPEC-SPRINT.md, cost attach).
// A priced record is refused without --replace. A file with one bad row writes nothing.

func TestCostAttachPricesAnEmptyRecordAndRefusesASecondWithoutReplace(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.m.SetRoutes(costRoutes())
	friendLand(t, ta, root, "Verdict: LAND\nHead: "+landHead+"\n\nPushed.\n", "")
	before := friendConsumer(t, ta)
	assert.Equal(t, cardcost.WhyNoTokens, before.Unpriced)

	out := ta.ok("cost attach s1-1.w1 --model " + friendUsageModel + " --input 1000 --cache-read 0 --cache-write 0 --output 100 --reasoning 0 --usd 0.02")
	assert.Contains(t, out, "route=flash-a")
	assert.Contains(t, out, "COST ATTACH OK records=1")
	assert.Equal(t, int64(1100), whereFriends(ta)["amy"].Tokens, "the work card's usage moves the friend's tokens before the next tick")
	ta.ok("tick")

	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Cost.Consumers, 1)
	got := c.Cost.Consumers[0]
	assert.Equal(t, "flash-a", got.Route, "attach writes on_route")
	assert.Equal(t, friendUsageModel, got.Model)
	assert.Equal(t, int64(1000), got.Usage.Tokens.Input)
	assert.Equal(t, int64(100), got.Usage.Tokens.Output)
	want := cardcost.Predict(got.Usage.Tokens, costRoutes()[0].Prices)
	assert.Equal(t, want.USD, got.Usage.Predicted)
	assert.Equal(t, "0.02", got.Usage.Actual)
	priced := c.Primary.F(sprint.FieldCostRecord + got.Key)

	code, _, errs := ta.do("cost attach s1-1.1 --model " + friendUsageModel + " --input 1 --cache-read 0 --cache-write 0 --output 1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "--replace")
	assert.Contains(t, errs, "s1-1")
	ta.json("card s1-1", &c)
	assert.Equal(t, priced, c.Primary.F(sprint.FieldCostRecord+got.Key), "a refusal writes nothing")

	out = ta.ok("cost attach s1-1.w1.1 --replace --model " + friendUsageModel + " --input 1000 --cache-read 0 --cache-write 0 --output 200 --source corrected")
	assert.Contains(t, out, "replaced=")
	assert.Contains(t, out, `output\x3d100`, "the old figures are one field, so = is escaped")
	ta.ok("tick")
	assert.Contains(t, ta.ok("log --card s1-1"), "output=100")
	ta.json("card s1-1", &c)
	require.Len(t, c.Cost.Consumers, 1)
	assert.Equal(t, int64(200), c.Cost.Consumers[0].Usage.Tokens.Output)
}

func TestCostAttachFileIsAllOrNoneAndDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.m.SetRoutes(costRoutes())
	friendLand(t, ta, root, "Verdict: LAND\nHead: "+landHead+"\n\nPushed.\n", "")
	before := ta.ok("card s1-1")

	dry := ta.ok("cost attach s1-1.w1 --model " + friendUsageModel + " --input 1000 --cache-read 0 --cache-write 0 --output 100 --dry-run")
	assert.Contains(t, dry, "nothing was written")
	assert.Equal(t, before, ta.ok("card s1-1"))

	file := filepath.Join(t.TempDir(), "rows.tsv")
	body := "card\tattempt\tmodel\tinput\tcache_read\tcache_write\toutput\treasoning\tusd\tsource\n" +
		"s1-1.w1\t1\t" + friendUsageModel + "\t1000\t0\t0\t100\t0\t0.02\tfrom her result\n" +
		"s1-9.w1\t2\t" + friendUsageModel + "\t1\t0\t0\t1\t0\tnot-a-number\tbad\n"
	require.NoError(t, os.WriteFile(file, []byte(body), 0o644))
	code, _, errs := ta.do("cost attach --file " + file)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "line 3")
	assert.Contains(t, errs, "s1-9.w1")
	assert.Contains(t, errs, "attempt 2")
	assert.Equal(t, before, ta.ok("card s1-1"), "one bad row writes nothing")

	body = "s1-1.w1\t1\t" + friendUsageModel + "\t1000\t0\t0\t100\t\t\t\n"
	require.NoError(t, os.WriteFile(file, []byte(body), 0o644))
	out := ta.ok("cost attach --file " + file)
	assert.Contains(t, out, "COST ATTACH OK records=1")
	ta.ok("tick")
	got := friendConsumer(t, ta)
	assert.Equal(t, "flash-a", got.Route)
	assert.Equal(t, int64(1000), got.Tokens.Input)
	assert.Equal(t, "", got.Actual)
}
