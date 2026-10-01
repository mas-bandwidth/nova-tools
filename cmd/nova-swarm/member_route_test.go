package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
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
	return &nativeRunner{self: self, sprintBin: "nova-sprint", harness: "/bin/true", model: model, root: dir, slots: slots,
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
		assert.Equal(t, c.model, f.Model, "%s: the frame names the model the card runs on", c.card)
		assert.Equal(t, c.tier, f.Tier, c.card)
		assert.NotEqual(t, "claude", cardcontract.FamilyOf("openai/gpt-mini"))
	}
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
		"NATIVE PROVIDER-5XX label=c1 ref=- wall=3.00s route=x next=- avoid=x\n":                        member.EndProvider,
		"NATIVE OK label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=1200/1000 stopped=budget\n":    member.EndBudget,
		"NATIVE INCOMPLETE label=c1 job=j tmp=t rc=-1 wall=none harness=ok budget=10/1000\n":            member.EndDeadline,
		"NATIVE INCOMPLETE label=c1 job=j tmp=t rc=-1 wall=none harness=ok budget=10 reason=terminated": "",
		"NATIVE OK label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=10/1000\n":                     "",
	} {
		assert.Equal(t, want, nativeEnd([]byte(log)), log)
	}
}
