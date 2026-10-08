# nova-cairn dogfood — opencode, 2026-10-06

Testing nova-cairn as a stranger would: help, then every verb with real flags. The bench is a Linux bench, the binary built from the staged commit at go build -o $JOB/bin/nova-cairn ./cmd/nova-cairn, version: nova-cairn devel linux/amd64 go1.26.6. Read cold: help, version, open, append, index, receipt.

## What held

open, append, index, receipt, and version all ran clean with real flags. Refusals for missing required flags work correctly, naming each missing flag with its purpose. The help output provides runnable examples in the example: block.

READ 8/10 — help is clear with runnable examples, each verb documented.

USE 9/10 — tool works as expected, dry-run available, refusals carry remedies.

urgent=0 next=0
