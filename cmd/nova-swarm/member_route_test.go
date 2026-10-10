package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// argsRunner is a nativeRunner whose own executable is a fake harness: a script
// that writes the arguments it was launched with, one a line, to <slots>/<card>.args.
func argsRunner(t *testing.T, model, tokens string, deadline time.Duration) *nativeRunner {
	t.Helper()
	windowsIsNotABench(t)
	dir := t.TempDir()
	slots := filepath.Join(dir, "slots")
	require.NoError(t, os.MkdirAll(slots, 0o755))
	self := filepath.Join(dir, "self.sh")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\"; done > '" + slots + "'/\"$(echo \"$@\" | sed 's/.*--label \\([^ ]*\\).*/\\1/')\".args\n"
	require.NoError(t, testbin.WriteExecutable(self, []byte(script), 0o755))
	return &nativeRunner{self: self, harness: "/bin/true", model: model, root: dir, slots: slots,
		resultsRoot: filepath.Join(dir, "results"), deadline: deadline, tokens: tokens, stderr: &bytes.Buffer{}}
}

// launched starts the packet on the runner and returns native's arguments by flag,
// and the frame the member wrote.
func launched(t *testing.T, r *nativeRunner, p member.Packet) (map[string]string, cardcontract.Frame) {
	t.Helper()
	ch, err := r.Start(p)
	require.NoError(t, err)
	<-ch.(*nativeChild).done
	b, err := os.ReadFile(filepath.Join(r.slots, p.Card+".args"))
	require.NoError(t, err)
	words := strings.Split(strings.TrimSpace(string(b)), "\n")
	args := map[string]string{}
	for i := 0; i+1 < len(words); i++ {
		if strings.HasPrefix(words[i], "--") {
			args[words[i]] = words[i+1]
		}
	}
	f, err := cardcontract.ReadFrame(filepath.Join(r.slots, launchName(p)+cardcontract.FrameName))
	require.NoError(t, err)
	return args, f
}

// The card decides its model: a flash card launches with the flash route's
// model, budget and deadline as the deal wrote them into its packet, a pro card
// with the pro route's, and the frame (JOB.md's profile) names the model chosen;
// the member's own --model, --tokens and --deadline are not used for either.
func TestAMemberLaunchesEachCardOnItsPacketsRoute(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "override/model", "999", 9*time.Second)
	for _, c := range []struct {
		card, tier, route, model, tokens string
		deadline                         int
	}{
		{"f1", "flash", "flash-a", "openai/gpt-mini", "100000", 600},
		{"p1", "pro", "pro-b", "anthropic/claude-pro", "unmetered", 1800},
	} {
		p := member.Packet{Card: c.card, Kind: "work", Attempt: 1, Gen: 1, Branch: "work/" + c.card,
			Brief: c.card + ": do it (s1) tier: " + c.tier + "\n\nThe task.", Route: c.route, Model: c.model, Tokens: c.tokens, Deadline: c.deadline}
		args, f := launched(t, r, p)
		assert.Equal(t, c.model, args["--model"], c.card)
		assert.Equal(t, c.tokens, args["--tokens"], c.card)
		assert.Equal(t, (time.Duration(c.deadline) * time.Second).String(), args["--deadline"], c.card)
		_, hasUSD := args["--usd"]
		assert.False(t, hasUSD, "a packet with no dollar budget launches with no --usd: %s", c.card)
		assert.Equal(t, c.model, f.Model, "%s: the frame names the model the card runs on", c.card)
		assert.Equal(t, c.tier, f.Tier, c.card)
		assert.NotEqual(t, "claude", cardcontract.FamilyOf("openai/gpt-mini"))
	}
}

// A route's dollar budget reaches native as --usd beside --tokens (nova-tools #5094).
func TestAMemberLaunchesACardWithItsRoutesDollarBudget(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "override/model", "999", 9*time.Second)
	routeSeconds := 600
	args, _ := launched(t, r, member.Packet{Card: "u1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/u1",
		Route: "flash-m", Model: "inception/mercury-2.5", Tokens: "2000000", USD: "0.5", Deadline: routeSeconds})
	assert.Equal(t, "0.5", args["--usd"], "the packet's dollar budget is native's --usd")
	assert.Equal(t, "2000000", args["--tokens"], "and the token budget stays beside it")
}

// A card with no route (a store with no route: a twin, one machine) runs on the
// member's override; a part the packet names wins over the override's.
func TestACardWithNoRouteRunsOnTheMembersOverride(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "override/model", "999", 9*time.Second)
	args, f := launched(t, r, member.Packet{Card: "c1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/c1"})
	assert.Equal(t, "override/model", args["--model"])
	assert.Equal(t, "999", args["--tokens"])
	assert.Equal(t, "9s", args["--deadline"])
	assert.Equal(t, "override/model", f.Model)
}

// A read's route is drawn by the ask from the reader tier and handed in its packet,
// so a reader loop needs no --model; a reader started with --model, --tokens and
// --deadline runs its reads on them over the read's route.
func TestAReadRunsOnItsRouteUnlessTheReaderNamesAModel(t *testing.T) {
	t.Parallel()
	seconds := 900 // the route's deadline, in seconds as the read card holds it
	p := member.Packet{Card: "c1.r1.reader-1", Kind: "read", Attempt: 1, Head: "abc", Brief: "c1: do it (s1) tier: flash\n\nThe task.",
		Route: "pro-a", Model: "deepseek/v4-pro", Tokens: "400000", Deadline: seconds}
	for _, c := range []struct {
		name                   string
		r                      *nativeRunner
		model, tokens, wallStr string
	}{
		{"no override: the read's route", argsRunner(t, "", "", 0), "deepseek/v4-pro", "400000", "15m0s"},
		{"the reader's --model, --tokens and --deadline", argsRunner(t, "override/model", "999", 9*time.Second), "override/model", "999", "9s"},
	} {
		args, _ := launched(t, c.r, p)
		assert.Equal(t, c.model, args["--model"], c.name)
		assert.Equal(t, c.tokens, args["--tokens"], c.name)
		assert.Equal(t, c.wallStr, args["--deadline"], c.name)
	}
}

// The pool identity the loop's nova-config argv names (--identity) is handed to
// every native launch, so no identity.tsv is written into the pool by hand.
func TestAMemberHandsItsIdentityToEveryLaunch(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "override/model", "999", 9*time.Second)
	r.identity = "pool-owner,Pool Worker,pool@example.com"
	args, _ := launched(t, r, member.Packet{Card: "c1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/c1"})
	assert.Equal(t, "pool-owner,Pool Worker,pool@example.com", args["--identity"])
}

// A card with no route on a member with no override is refused at its launch,
// naming what is missing and the two ways to give it; nothing is started.
func TestACardWithNoRouteAndNoOverrideIsRefused(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "", "", 0)
	_, err := r.Start(member.Packet{Card: "c1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/c1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nova-config route add")
	assert.Contains(t, err.Error(), "--model")
	_, statErr := os.Stat(filepath.Join(r.slots, "c1.args"))
	assert.True(t, os.IsNotExist(statErr), "nothing was launched")
}

// How a launch that did not finish ended, from native's log: the provider, the
// budget, the deadline; a TERM from outside and an ok run are none of them.
func TestTheEndOfALaunchIsReadFromNativesLog(t *testing.T) {
	t.Parallel()
	for log, want := range map[string]string{
		"NATIVE PROVIDER-5XX label=c1 ref=- wall=3.00s route=x next=- avoid=x\n":                         member.EndProvider,
		"NATIVE OK label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=1200/1000 stopped=budget\n":     member.EndBudget,
		"NATIVE OK label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=12/1000 stopped=unverifiable\n": member.EndUnverifiable,
		"NATIVE INCOMPLETE label=c1 job=j tmp=t rc=-1 wall=none harness=ok budget=10/1000\n":             member.EndDeadline,
		"NATIVE INCOMPLETE label=c1 job=j tmp=t rc=-1 wall=none harness=ok budget=10 reason=terminated":  "",
		"NATIVE OK label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=10/1000\n":                      "",
	} {
		assert.Equal(t, want, nativeEnd([]byte(log)), log)
	}
}

// oneCardSprint is a sprint of one work card for a member m1 of width 2: ready
// until taken, working after, every verb recorded.
type oneCardSprint struct {
	taken bool
	calls []string
	pkt   member.Packet
}

func (f *oneCardSprint) Run(args ...string) (int, []byte) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "queue":
		col := "ready"
		if f.taken {
			col = "working"
		}
		b, _ := json.Marshal(map[string]any{"as": "m1", "epoch": 1, "width": 2,
			"cards": []map[string]any{{"id": f.pkt.Card, "table": "fleet", "row": "m1", "col": col, "gen": 1, "packet": f.pkt}}})
		return 0, b
	case "take":
		f.taken = true
		b, _ := json.Marshal(map[string]any{"packets": []member.Packet{f.pkt}})
		return 0, b
	}
	return 0, nil
}

// A taken card the member cannot launch (here a pin with no budget or deadline,
// admitted before the lint refused it, on a member with no override) is reported at
// once as a failed finish naming why, never left working to be refused every tick;
// the refusal names the pin, not "no route".
func TestATakenCardTheMemberCannotLaunchIsFinishedFailed(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "", "", 0)
	fs := &oneCardSprint{pkt: member.Packet{Card: "s1-1.w1", Kind: "work", Attempt: 1, Gen: 1, Epoch: 1, Branch: "work/s1-1",
		Brief: "c: x (s1) tier: pro\nmodel: x/y\n\nThe task.", Route: "pin", Model: "x/y"}}
	var log bytes.Buffer
	m := member.New(member.Config{As: "m1"}, fs, r, nil, &log)
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	var finishes []string
	for _, c := range fs.calls {
		if strings.HasPrefix(c, "finish ") {
			finishes = append(finishes, c)
		}
	}
	require.Len(t, finishes, 1, "one failed finish: %v\n%s", fs.calls, log.String())
	assert.Contains(t, finishes[0], "finish --as m1 s1-1.w1@1 --failed --report launch refused: card s1-1.w1's route pin (model \"x/y\") names no tokens or deadline")
	assert.Contains(t, finishes[0], "--epoch 1")
	assert.NotContains(t, finishes[0], "has no route")
	assert.Contains(t, log.String(), "finish s1-1.w1 ok=false exit=0 launch refused route=pin model=x/y")
}

// The MEMBER line says what the member runs on: the card's route, its own model
// only as an override, and the width as its fleet row's or an override.
func TestTheMemberLineSaysTheOverride(t *testing.T) {
	t.Parallel()
	for args, want := range map[string]string{
		"--model ov/m --tokens 5 --deadline 9s": "width=row every=3s server=sprint.test:6390 harness=/bin/true model=card,override:ov/m",
		"--width 3":                             "width=override:3 every=3s server=sprint.test:6390 harness=/bin/true model=card",
	} {
		var out, errb bytes.Buffer
		cmdMember(append([]string{"--as", "m1", "--harness", "/bin/true", "--root", t.TempDir(), "--once", "--server", "sprint.test:6390"}, strings.Fields(args)...), &out, &errb, noServer)
		assert.Contains(t, out.String(), "MEMBER member as=m1 "+want, args)
	}
}
