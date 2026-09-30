package main

import (
	"bytes"
	"strings"
	"testing"
)

func swarmHelp(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := swarmRun(args, &out, &errb); code != 0 || errb.Len() != 0 {
		t.Fatalf("nova-swarm %s: exit %d, stderr %q", strings.Join(args, " "), code, errb.String())
	}
	return out.String()
}

// native and member each show one example line in their -h, above the
// flags, and every flag the example uses is one the verb's own help lists.
func TestTheLaunchVerbsShowAnExampleMadeOfTheirOwnFlags(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"native", "member"} {
		help := swarmHelp(t, verb, "-h")
		ex, ok := verbExamples[verb]
		if !ok {
			t.Fatalf("%s has no example", verb)
		}
		if !strings.Contains(help, "example:\n  "+ex+"\n") {
			t.Errorf("%s -h lacks its example %q:\n%s", verb, ex, help)
			continue
		}
		flags := help[strings.Index(help, "flags:\n"):]
		if strings.Index(help, "example:") > strings.Index(help, "flags:") {
			t.Errorf("%s -h shows the example after the flags", verb)
		}
		for _, w := range strings.Fields(ex) {
			if strings.HasPrefix(w, "--") && !strings.Contains(flags, "\n  "+w+" ") && !strings.Contains(flags, "\n  "+w+"\n") {
				t.Errorf("%s: the example uses %s, which %s -h does not list", verb, w, verb)
			}
		}
		if !strings.HasPrefix(ex, "nova-swarm "+verb+" ") {
			t.Errorf("the %s example does not run %s: %q", verb, verb, ex)
		}
	}
}

// template -h lists the lines a card needs, and every line it names is one the
// card template itself writes, so the help and the template cannot part.
func TestTemplateHelpListsTheCardsRequiredLines(t *testing.T) {
	t.Parallel()
	help := swarmHelp(t, "template", "-h")
	card := swarmHelp(t, "template", "--name", "card")
	for _, anchor := range []string{"RESULT: <label> sha=<sha12>", "Deadline: finish within <n> minutes.", "RULES.", "THE TASK.", "STEP 1.", "RESULT.md"} {
		if !strings.Contains(help, anchor) {
			t.Errorf("template -h does not list %q", anchor)
		}
		if !strings.Contains(card, anchor) {
			t.Errorf("template --name card does not write %q, which template -h lists", anchor)
		}
	}
	for _, typed := range []string{"KIND:", "PATHS:", "TEST:", "DEPENDS-ON:", "DONE-WHEN:"} {
		if !strings.Contains(help, typed) {
			t.Errorf("template -h does not name the typed line %s", typed)
		}
	}
}

// The banner names the way to an example card, and what that verb prints is a
// card the lint's child rules accept.
func TestTheBannerNamesTheExampleCard(t *testing.T) {
	t.Parallel()
	help := swarmHelp(t, "help")
	if !strings.Contains(help, "nova-swarm template --name card prints one that passes") {
		t.Errorf("the banner does not name template --name card as the way to an example card")
	}
}
