package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// runSetting is `setting set sprint.<name> <value> [flags]`: one field of the sprint row
// by its dotted name, the same write as `sprint set --<name> <value>` and refused the same
// way (a policy number out of its range names the number and its range; the owner,
// 2026-10-02: "i just want to set numbers as I see fit directly in nova-config"). apply
// --kind sprint delivers it to sprint:<name>, which the sprint's tick reads on its next
// pass (internal/config/policy.go, SprintPolicies).
func runSetting(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	verb := "setting"
	if len(args) > 0 && args[0] == "set" {
		verb = "setting set"
	}
	verbflag.HelpIfAsked(args, verb)
	if len(args) == 0 || args[0] != "set" {
		return refuse(stderr, "setting", "want setting set sprint.<name> <value> --as <name>")
	}
	if len(args) < 3 || strings.HasPrefix(args[1], "-") {
		return refuse(stderr, verb, "want setting set sprint.<name> <value> --as <name>; flags follow the value")
	}
	k, _ := config.Lookup(config.KindSprint)
	var names []string
	for _, f := range k.Fields {
		names = append(names, f.Name)
	}
	name, ok := strings.CutPrefix(args[1], config.KindSprint+".")
	if !ok || !slices.Contains(names, name) {
		return refuse(stderr, verb, fmt.Sprintf("no setting %s; want sprint.<name>, a field of the sprint row: %s", oneline.Quote(args[1]), strings.Join(names, ", ")))
	}
	return runKindWrite(ctx, k, false, append([]string{"--" + name, args[2]}, args[3:]...), stdout, stderr, d)
}
