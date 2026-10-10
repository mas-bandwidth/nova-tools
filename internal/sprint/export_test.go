package sprint

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Exported helpers for the tests of package sprint_test, which reach the package from
// outside: test-only, so no production code calls them.

// FleetBackTables is the tick's table updates with the presence part
// FleetBackPresence(a): a store's Updates.
func FleetBackTables(a Adopter) []TableUpdate {
	fn := FleetBackPresence(a)
	out := make([]TableUpdate, len(TickTables))
	for i, u := range TickTables {
		out[i] = TableUpdate{Table: u.Table, Parts: append([]TickPartDef(nil), u.Parts...)}
		for j := range out[i].Parts {
			if out[i].Parts[j].Name == "presence" {
				out[i].Parts[j].Fn = fn
			}
		}
	}
	return out
}

// ReadDrift reads the drift facts of a clone whose branches are as fetched (the binding
// fetches dev and the base first): the base against dev, and the live server's build commit
// against the base (none read when server is ""). The gate is not git's: the binding sets
// Gate from the last whole-tree gate run at the base. An error is a fact that could not be
// read; a server commit the clone does not hold is read, and not On.
func ReadDrift(ctx context.Context, run GitRunner, dir, base, dev, server string) (DriftFacts, error) {
	f := DriftFacts{Base: base, Dev: dev}
	if base == "" || strings.HasPrefix(base, "-") || dev == "" || strings.HasPrefix(dev, "-") {
		return f, fmt.Errorf("the base %q and dev %q are not branch names", base, dev)
	}
	tip, err := run(ctx, dir, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return f, fmt.Errorf("%s has no tip in %s: %w", base, dir, err)
	}
	ahead := DriftAhead{Tip: tip}
	stamps, err := run(ctx, dir, "log", "--format=%ct", "--end-of-options", dev+".."+base)
	if err != nil {
		return f, fmt.Errorf("git log %s..%s in %s: %w", dev, base, dir, err)
	}
	for _, l := range strings.Fields(stamps) {
		sec, err := strconv.ParseInt(l, 10, 64)
		if err != nil {
			return f, fmt.Errorf("git log %s..%s in %s: a commit time %q", dev, base, dir, l)
		}
		ahead.Commits++
		if t := time.Unix(sec, 0).UTC(); ahead.Oldest.IsZero() || t.Before(ahead.Oldest) {
			ahead.Oldest = t
		}
	}
	f.Ahead = &ahead
	if server == "" {
		return f, nil
	}
	sv := DriftServer{Commit: server}
	full, err := run(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", server+"^{commit}")
	if err != nil {
		sv.Why = "a commit in no branch fetched"
		f.Server = &sv
		return f, nil
	}
	sv.Commit = full
	_, err = run(ctx, dir, "merge-base", "--is-ancestor", "--end-of-options", full, tip)
	var ee *exec.ExitError
	switch {
	case err == nil:
		sv.On = true
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		sv.Why = "not an ancestor of " + base
	default:
		return f, fmt.Errorf("git merge-base --is-ancestor %s %s in %s: %w", full, base, dir, err)
	}
	f.Server = &sv
	return f, nil
}

// Stats is the pass's numbers over the snapshot's work, fleet and readers tables
// (loaded with StatsRecords).
func Stats(s *Snapshot) PassStats { return StatsSince(s, time.Time{}) }
