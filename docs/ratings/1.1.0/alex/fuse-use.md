# nova-fuse USE rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 044b5dfe9c1b
Score: 9/10

## Reasons

Judged by use alone, from the help, every command run in a scratch directory
on boxes made there; nothing real was touched and no verb was left untried —
the tool needs no store, key or remote, so every verb ran. The banner's
example sitting ran as printed, six lines in order, and every exit matched
the banner's table: INIT OK then STATUS OK then FUSE OK, QUARANTINE OK then
FUSE FAILED at exit 1 with a pasteable lift remedy, LIFT OK twice then FUSE
OK again. Two real jobs beyond the sitting: quarantining a mixed-case
spelling (stored folded, so a lowercase check refuses it), and the escalation
— lockdown blown over a standing quarantine, bare check and surface check both
answering lockdown first, status still reporting both at exit 0, lift
quarantine working under lockdown and saying so, lift lockdown refused forever
even with flags.

The refusals are the best of it. A missing --box names what the flag wants,
in one line plus one indented hint, with the door; an unknown flag names the
offending flag AND the flags there are; an unknown verb names every verb
there is; a bad value says the unit and what 0 means; a repeated flag is
refused before any box is read; and one run with nothing at all names --box,
the surface and the reason together. The -h refusal after a verb, the one
family exception, says why and names the door that does work. The fail-closed
story held in use: a torn box reads as blown on check at exit 2, quarantine
refuses to narrow it and says which way is safe, lockdown proceeds, keeps
the corrupt bytes at a .unreadable beside it, and the box answers again.
Status is bounded with its count never capped — 25 quarantines, 20 lines,
one MORE line with the flag that widens it; --max 0 lists all; --max -3 is
a refusal.

What it costs: a re-blow of a quarantined surface rewrites its stamp and
reason and says only the new ones — the earlier record is gone with no
was-line, on the one power whose history is its audit trail; the family's
--json and --dry-run are absent and unnamed in the help, so a reader of the
set meets them as an unknown-flag refusal; and a quarantine behind a blown
lockdown is invisible through the check exit code, named in the spec and in
status, not in the check line. A 10 would need the re-blow to name what it
replaced, the missing renderings named in the help so no reader has to try
them, and the double-blown seam said out loud where a caller reads it.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse quarantine --box ./many.json surface-3 "the second reason, an hour later"` | a re-blow of an already-quarantined surface rewrites its time and reason and announces only the new ones; the earlier stamp and reason leave the box with no was-line, though a lift prints exactly that | print a was since= tail on a re-blow, or refuse a second blow naming lift first | S |
| 2 | `nova-fuse status --box ./fuse-box.json --json` | the family's --json and --dry-run are offered by no verb and named nowhere in the help; a reader of the set discovers them by refusal, honest but guessed | one banner NOTE naming the one-line grammar as the only rendering, and why | S |
| 3 | `nova-fuse check --box ./fuse-box.json a-forum` | under a blown lockdown the check answers lockdown only, so a quarantine behind it is invisible through the exit code and re-emerges only after the lockdown is replaced; named in the spec, silent at the seam a caller reads | a NOTE on the lockdown FAIL line naming the quarantines behind it | S |
| 4 | `nova-fuse status` | the missing --box hint is a continuation line opening with neither NOTE nor a grammar token; the family rule says a continuation line opens with MORE or NOTE | prefix the hint with NOTE | S |

## Good, keep

- Every refusal names the problem, the flags or verbs there are, and the
  next command; one run with nothing at all names --box, the surface and the
  reason together.
- The fail-closed story holds in use: a torn box reads as blown, quarantine
  refuses to narrow it, lockdown proceeds and preserves the bytes.
- Write verbs re-read the box and say verified, and the lift remedy prints as
  a pasteable command with the surface after --.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a re-blow silently rewrites the fuse's time and reason | STILL THERE | `nova-fuse quarantine --box ./many.json surface-3 "the second reason, an hour later"` prints QUARANTINE OK with the new since and reason only; status then shows no trace of the first |
| the unknown-option refusal omits the offending flag | FIXED | `nova-fuse status --box ./fuse-box.json --maxx 5` prints unknown flag --maxx; flags: --box, --max; run: nova-fuse help status |
