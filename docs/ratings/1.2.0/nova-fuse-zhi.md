# nova-fuse READ and USE rating, nova-tools v1.2.0

Rater: Zhi (opencode/gpt-5.6-luna via OpenCode)
Build: c76fcb249cc1
READ: 8/10
USE: 9/10

The tool as released in nova-tools v1.2.0 was read cold, including the banner,
the spec, the CLI transcript, and `help` plus `-h` probes for every verb. I then
used it on a throwaway directory and JSON box, never a live store or server.

## Reasons

READ. The banner answers what the fuse does, where state lives, and how to make
the first run. The examples are runnable, the help for each verb states its
effect and exit codes, and the refusal grammar is unusually clear about the
next command. The spec explains the safety-critical asymmetry: an unreadable
box fails closed, quarantine refuses to narrow an unreadable state, and
lockdown is the one action that can safely create or replace a box. Reading
the source after the spec confirmed that writes are verified by rereading and
that repeated lockdowns and quarantines preserve the standing audit record.

What keeps READ at 8 is contract drift in the normative output examples and
ordering statement, plus the deliberate exception from the repository's
`--json` convention. A cold caller must reconcile the spec, CLI transcript,
and binary for the exact failure token and cannot request a machine-readable
result.

USE. On the throwaway box, init, status, check, quarantine, dry-run lift, lift,
and lockdown all behaved as documented. Exit 0 clearly means permission only
for check; failed checks name the fuse and give a runnable remedy; dry-run did
not change the box; and repeated quarantine kept the original timestamp and
reason instead of silently replacing the audit fact. The tool needs no store,
server, or configuration discovery, which makes it practical for an AI to put
in front of an ingestion path.

What keeps USE at 9 is that a caller reading stdout alone misses a failed
check because the result is on stderr, and typed-line parsing remains necessary
because there is no JSON rendering. Those are manageable once learned, but
both are avoidable sources of integration mistakes.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `docs/SPEC.md:2043-2044` | The normative grammar says `FUSE FAIL`, but the released binary and CLI transcript emit `FUSE FAILED`; a caller matching the spec misses every failed gate result. | Change the normative examples to `FUSE FAILED` and keep the token equal to the binary and transcript. | S |
| 2 | `docs/SPEC.md:2185-2186` | The status section says quarantine rows are in the box's own order, but `internal/fuse/fuse.go:213-215` sorts them before output. | Specify sorted order, which is the deterministic behavior the binary provides. | S |
| 3 | `cmd/nova-fuse/check.go:59-65` | A failed `check` writes `FUSE FAILED` only to stderr, so an AI that consumes stdout as the result sees no gate answer even though exit 1 is meaningful. | Put the typed failure result on stdout, or provide a documented structured result stream that callers can consume without merging stderr. | S |
| 4 | `cmd/nova-fuse/main.go:75` | The tool refuses `--json` for every verb, leaving an AI to parse typed lines and consult the spec for the grammar. | Render the same result value as JSON under `--json` while retaining the typed-line output and exit codes. | M |

## Good, keep

The no-guessing rule for `--box`, failed-closed reads, atomic verified writes,
and explicit dry-run behavior are excellent defaults for an AI. The one-command
remedies in failed check output are actionable, and the hard lockdown refusal
prevents a tool invocation from silently restoring permission. Repeated blows
now preserve the first timestamp and reason, fixing the 1.1.0 re-blow audit
problem.

## Compared with earlier ratings

The 1.1.0 USE rating's silent re-blow finding is FIXED: a repeated quarantine
now says `already=quarantined` and keeps the standing record. The earlier
READ rating's spec token drift remains: `docs/SPEC.md` still says `FUSE FAIL`
while the binary says `FUSE FAILED`. The earlier status-order drift also
remains, while the tool's bounded totals, refusal remedies, and verified
write discipline continue to be strong.
