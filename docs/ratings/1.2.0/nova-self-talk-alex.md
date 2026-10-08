# nova-self-talk rating, nova-tools 1.2.0

Build: e8f3f7579764
READ: 9/10
USE: 9/10

## Reasons

Cold start, no service needed. `nova-self-talk help` shows the full banner with usage, classes, output format, flags, exit codes, and a first-run example. The `help <verb>` form works for each verb. The `shapes` verb prints the 22 shape rows with their find/pass sentences and patterns. The `example` verb writes two example markdown files that can be scanned immediately.

Scans behave as documented: findings on stderr in line order, DATED and closing counts on stdout, exit 1 on findings, exit 0 on green. The `--json` flag works on scan, shapes, and example. The `--max` truncation adds a MORE line with remedy.

What a 10 needs: `help <unknown-verb>` returns general help with exit 0 instead of saying the word is not a verb with exit 2. This prevents automated tools from detecting misuse.

## Findings

tool	verb or file:line	defect	fix
nova-self-talk	help <unknown-verb>	help unknownword returns general help with exit 0; an AI cannot tell fallback from answer	print "unknown-verb is not a verb (verbs are scan, shapes, example, version, help)" and exit 2

## Good, keep

- The banner is self-contained: usage, classes, output format, flags, exit codes, and first-run example are in one place.
- The shapes verb is machine-readable and shows exactly what the scanner looks for.
- The example verb is idempotent and shows what it wrote so the next run is obvious.
- Findings go to stderr, counts go to stdout, and --json works on multiple verbs.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0 READ: 8.5/10 | READ: 9/10 | the banner is clearer; shapes verb is now exposed with --json |
| 1.1.0 USE: 9.5/10 | USE: 9/10 | help unknownword still returns exit 0; that finding from 1.1.0 remains |
