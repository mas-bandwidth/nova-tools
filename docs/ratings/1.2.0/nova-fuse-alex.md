# nova-fuse READ and USE rating, nova-tools 1.2.0

Rater: cold reader (independent review)
Build: 7acb90e18a764f0e728cd5ed701196a34405a824
READ: 8/10
USE: 9/10

Built and run on a Linux bench machine. No live store, no server started on this machine.

## Reasons

READ. The banner first line is the README row sentence, and the next four lines say the whole contract: one JSON file named with --box; one lockdown (every untrusted read stops) and one quarantine per surface; each carries its time and reason. The six examples are one sitting and run as printed. Every verb answers help <verb> with its usage and flag list. The spec opens with what the tool refuses to be, and every refusal names the next command, quoted for the shell.

What keeps READ at 8. The usage block is 73 lines (cmd/nova-fuse/main.go:37-109) and its example: block starts at main.go:95, so 58 lines of exit codes and other prose stand before it that each verb help repeats. The spec grammar samples (FUSE FAIL) do not match the binary bytes (FUSE FAILED). --json is refused by design, so an AI parses typed lines by hand.

USE. The banner example ran clean. The tool refuses --box when missing and says refusing to guess, with a clear remedy. --dry-run on init, lockdown, quarantine and lift quarantine checks every write and writes nothing, saying dry_run=true. Exit codes are clear: 0 clear, 1 blown, 2 could-not-run. A cold reader needs only one turn to recover from any mistake.

What keeps USE at 9. The exit-code contract is stated in the banner and kept: 0 clear, 1 blown, 2 could-not-run. --dry-run writes nothing and says so. Status bounds its list with a MORE line whose total is never capped. The refusal grammar names every independent problem at once and gives a command that runs.

A 10 would need: the spec grammar samples equal to the binary bytes; the README on the shipped version; the banner cut to its three answers, the verb table and the example; and --json rendering of the one result value.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:2043; cmd/nova-fuse/check.go:59 | the normative grammar prints FUSE FAIL, LOCKDOWN FAIL, etc.; the binary prints FUSE FAILED, LOCKDOWN FAILED, etc. | print the spec samples as the bytes the binary writes, FAILED throughout | S |
| 2 | docs/STANDARD.md (one value, two renderings) | the tool explicitly refuses --json with no alternative machine-readable format | render the one result value as JSON under --json, keeping the exit codes | M |
| 3 | README.md:86 | the trial section says These are the Nova Tools 1.0.0 commands while the tree ships v1.1.0/v1.2.0 | update to the shipped version | S |
| 4 | cmd/nova-fuse/main.go:37-109 | the banner is 73 lines; 58 lines of exit codes and other prose stand before the example: block at main.go:95 | keep the three answers, the verb table and the example; move the rest to help <verb> and docs/CLI.md | M |
| 5 | internal/fuse/fuse.go:1 | the package doc opens with shouted numbered headings | state each rule once, present tense, in ordinary prose | M |

## Good, keep

- The refusal grammar and its remedies: one line, every independent problem at once, a command that runs, and the box path quoted so a dash or a control byte stays data.
- The read discipline: absent and unreadable are both CANNOT TELL treated as BLOWN, told apart only so the remedy is right, and every write is verified by re-reading.
- cmd/nova-fuse/firstrun_test.go runs the docs/CLI.md and docs/TESTS.md transcripts line for line, so the document is the contract and cannot drift silently.
- The exit-code contract is stated in the banner and repeated per verb: 0 clear, 1 blown, 2 could-not-run.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| FUSE FAIL in spec vs FUSE FAILED in binary | STILL THERE | docs/SPEC.md:2043 vs cmd/nova-fuse/check.go:59 |
| banner length | STILL THERE | 73-line banner; example at main.go:95 |
| shouted package doc | STILL THERE | internal/fuse/fuse.go:1 |
| multiple verb list copies | STILL THERE | usage constant, verbs slice, help map |
| no --json | STILL THERE | explicitly refused by design |
| README version | STILL THERE | 1.0.0 references |
| exit codes clear | CHANGED | 0 clear, 1 blown, 2 could-not-run explicitly stated |
| --dry-run on all writes | STILL THERE | init, lockdown, quarantine, lift quarantine |
