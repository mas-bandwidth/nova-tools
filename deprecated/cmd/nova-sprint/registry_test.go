package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRegisteredVerbDispatchAndHelp(t *testing.T) {
	register(Verb{
		Name:    "probe",
		Summary: "exercise the registration seam",
		Run: func(_ context.Context, args []string, out, _ io.Writer) int {
			if len(args) != 1 || args[0] != "value" {
				return 2
			}
			_, _ = io.WriteString(out, "PROBE OK\n")
			return 0
		},
	})
	t.Cleanup(func() { delete(verbs, "probe") })
	code, stdout, stderr := runSprint("probe", "value")
	if code != 0 || stdout != "PROBE OK\n" || stderr != "" {
		t.Fatalf("registered verb: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runSprint("help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "  probe  exercise the registration seam\n") {
		t.Fatalf("help omits registered verb: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if !strings.HasSuffix(stdout, "  nova-sprint table --check --redis 127.0.0.1:6379\n") {
		t.Fatalf("registration displaced the runnable examples: %q", stdout)
	}
}
