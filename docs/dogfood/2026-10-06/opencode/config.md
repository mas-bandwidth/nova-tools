# nova-config dogfood — opencode, 2026-10-06

This dogfood was run on a Linux bench. The binary was built from the staged checkout at commit 62d1a251aa84857342114f90e3f5a58a40131e00 (sprint/mechanical-2026-10-02) using `go build -o $JOB/bin/nova-config ./cmd/nova-config` with `go version go1.26.6 linux/amd64`. The version line printed was `nova-config devel linux/amd64 go1.26.6`. I read the help for every verb with `-h` and then ran each against a scratch store (a local JSON file with `--file try.json`).

## Findings

1. `nova-config route add r1 --tier flash --provider test --model m1 --deadline 30s --actor a1 --file try.json`
   Printed:
   ```
   nova-config route add REFUSED: --deadline "30s": want a non-negative integer; run: nova-config route add -h
   (one line printed)
   ```
   I expected the deadline to accept duration strings like "30s" (30 seconds) as is common in CLI tools.
   Grade: NEXT (friction, a missing flag, or unclear help)

## What held

The following verbs ran clean: help, version, kinds, migrate (twice), status, apply, inventory, logout, machine add, machine set, machine list, machine show, machine history, machine width, machine self, machine remove, friend add, friend list, friend show, friend history, friend set, friend remove, sprint show, sprint set, sprint history, tier show, tier add, tier set, tier remove, tier list, tier history, route list, route add, route set, route remove, route show, route history, route prices, loop add, loop list, loop show, loop set, loop remove, loop history, loop run, login --check (expected refusal), fleet show, fleet set, fleet history, and migrate --dry-run.

READ 8/10 — The help is thorough with good examples but could use clearer flag type descriptions for things like deadline.
USE 7/10 — Generally easy to use once you know the pattern; the strict deadline format is a small friction point.

urgent=0 next=1
