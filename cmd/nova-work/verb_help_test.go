package main

import (
	"bytes"
	"strings"
	"testing"
)

var all51Verbs = []string{
	"accept",
	"acknowledge",
	"ask",
	"asks",
	"attest",
	"attempt",
	"axis",
	"cell",
	"check",
	"clip",
	"config",
	"correct",
	"decline",
	"decompose",
	"dep",
	"dependencies",
	"event",
	"events",
	"evidence",
	"execution",
	"friend",
	"goal",
	"heartbeat",
	"help",
	"machine",
	"model",
	"node",
	"observe",
	"offer",
	"operation",
	"prioritise",
	"profile",
	"proving-run",
	"query",
	"ready",
	"redo",
	"redo-plan",
	"release",
	"render",
	"responsible",
	"roadmap",
	"route",
	"savepoint",
	"session",
	"source",
	"state",
	"take",
	"undo",
	"undo-plan",
	"verify",
	"version",
}

func TestEveryVerbHelpExits2(t *testing.T) {
	if len(all51Verbs) != 51 {
		t.Fatalf("expected 51 verbs, got %d", len(all51Verbs))
	}

	for _, v := range all51Verbs {
		t.Run(v, func(t *testing.T) {
			args := []string{v, "--help"}
			var out bytes.Buffer
			code := run(args, &out, &out)
			if code != 2 {
				t.Errorf("expected exit code 2, got %d. Output: %s", code, out.String())
			}

			outStr := out.String()
			if !strings.Contains(outStr, v) {
				t.Errorf("output missing verb %q. Output: %s", v, outStr)
			}
			if strings.Contains(outStr, "flag: help requested") {
				t.Errorf("output still contains 'flag: help requested'")
			}
		})
	}
}
