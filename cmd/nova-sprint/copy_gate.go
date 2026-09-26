package main

import (
	"context"
	"fmt"
	"io"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// gateCopyEnd is the spec gate (#4313) at a door that ends a code copy ok
// with a PR outside the copy wrapper: friend done --ok --pr and card end
// --ok --pr (nova-tools#4401 read, DOORS). A read copy carries no commit and
// passes. Any other copy's end runs the gate in repoDir, the checkout at
// head, the base the copy's (a fix's the PR head) and the test the copy's
// (a fix's the finding test it names, never none); the rows print as GATE
// lines. It returns "" when the end may go on, else the refusal: the gate's
// typed reason and why, or why the gate could not run.
func gateCopyEnd(ctx context.Context, c redis.Cmdable, id string, rec map[string]string, repoDir, findingTest, head string, out io.Writer) string {
	cc := card.CopyCardFrom(id, rec)
	if cc.Leg == "read" {
		return ""
	}
	if repoDir == "" {
		return card.GateNoTest + " --ok --pr wants --repo <your checkout at --head>: the spec gate runs there before the end"
	}
	if cc.Test == "" && cc.Primary != "" {
		// a copy cut before TM.CARRY carried test: the primary's
		cc.Test = c.HGet(ctx, taskcard.Key(cc.Primary), "test").Val()
	}
	g, err := card.GateFriendCopy(ctx, card.FriendGate{Copy: cc, Finding: findingTest, Repo: repoDir, Head: head})
	if err != nil {
		return "gate could not run: " + err.Error()
	}
	for _, row := range g.Rows {
		_, _ = fmt.Fprintf(out, "GATE %s\n", row)
	}
	if !g.Passed() {
		return g.Reason + " " + g.Why
	}
	return ""
}
