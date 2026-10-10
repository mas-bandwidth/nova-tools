package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// What a job spent (native.go, launchSpend and spendWord): each launch's final read as a
// cost record, folded into the NATIVE line's spend= word; and the member reading that word
// into the usage record it reports with the finish (member.go, Result).

func TestTheSpendWordFoldsEveryLaunchAndKeepsTheCostOnlyWhole(t *testing.T) {
	t.Parallel()
	launch := func(in, cost string) map[string]string {
		return map[string]string{"tokens_in": in, "tokens_out": "10", "cache_write": swarm.Dash, "cache_read": "100", "reasoning": "5",
			"requests": "2", "max_prompt": "300", "cost": cost, "provider": "opencode", "model": "deepseek-v4-pro"}
	}
	cases := []struct {
		name     string
		launches []map[string]string
		want     string
	}{
		{name: "no launch answered", want: ""},
		{name: "one launch", launches: []map[string]string{launch("40", "0.01")},
			want: "input:40,cache_read:100,output:10,reasoning:5,requests:2,max_prompt:300,cost:0.01,model:opencode/deepseek-v4-pro"},
		{name: "two launches add, each once", launches: []map[string]string{launch("40", "0.01"), launch("70", "0.02")},
			want: "input:110,cache_read:200,output:20,reasoning:10,requests:4,max_prompt:300,cost:0.03,model:opencode/deepseek-v4-pro"},
		{name: "a launch with tokens and no cost: no cost for the job", launches: []map[string]string{launch("40", "0.01"), launch("70", "")},
			want: "input:110,cache_read:200,output:20,reasoning:10,requests:4,max_prompt:300,model:opencode/deepseek-v4-pro"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var us []cardcost.Usage
			for _, l := range tc.launches {
				us = append(us, launchSpend(l))
			}
			assert.Equal(t, tc.want, spendWord(us))
		})
	}
}

func TestTheMemberReportsTheSpendInTheUsageRecord(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	close(done)
	line := "NATIVE OK label=c1 job=/j tmp=/t rc=0 wall=49.49s sandbox=none card_sha256=x binary_sha256=y config=- harness=ok budget=20303/400000"
	cases := []struct {
		name, log, usage string
	}{
		{name: "the older line: wall and budget only", log: line + "\n", usage: "wall=49.49s budget=20303/400000 cost=none"},
		// the harness reported nothing and no receipt is there: the usage still says so,
		// so a routed read's verdict carries --usage and is kept unpriced=no-tokens
		{name: "no line and no receipt", log: "the child printed nothing native reads\n", usage: "usage_source=none cost=none"},
		{name: "a line with the job's spend", log: line + " spend=input:19541,cache_read:36336,cache_write:0,output:692,reasoning:70,requests:6,max_prompt:9861,cost:0.04219614,model:opencode/deepseek-v4-pro\n",
			usage: "wall=49.49s budget=20303/400000 input=19541 cache_read=36336 cache_write=0 output=692 reasoning=70 requests=6 max_prompt=9861 model=opencode/deepseek-v4-pro actual_usd=0.04219614 actual_by=harness cost=actual"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			logPath := filepath.Join(dir, "c1.native.log")
			write(t, logPath, tc.log)
			c := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: done}
			assert.Equal(t, tc.usage, c.Result().Usage)
		})
	}
}
