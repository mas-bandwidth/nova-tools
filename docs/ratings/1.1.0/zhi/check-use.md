# nova-check USE rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 2c02b2aa2042
Score: 8/10

## Reasons

Cold, the tool is easy to drive. `nova-check help` answers what it does in one
line, then how it works, then a runnable `example:` block; `nova-check <verb>
-h` prints that verb's usage, flags and exit table and exits 0, so the first
question is always safe. `quickstart` on the included example runs links then
nocode, prints an OK line per check, and closes with the next verbs to try.

The real work end to end: `links` over a small record found two broken links by
file and line (`plan.md:3: steps.md (does not exist)`) and went green once the
files existed; `spelling` found two misspellings and `--write` fixed them in
place, then re-ran green. A scratch git repository let `hygiene` answer clean
for a bound path and then flag a stray file outside the bound, each with the
right exit code. `dogfood record` wrote a receipt and `dogfood ledger` read it
back.

The refusals are the best part. Four provoked — a missing flag, an unknown
flag, an unknown verb, a bad value — each printed one line naming the problem
and `run: nova-check help`, and `attest` with two missing flags named both at
once. None pointed me at the whole banner; the missing-flag line even says what
the flag wants. `--json` returns the documented one-value shape and is
actionable, including a per-finding `items` list for broken links.

What costs the score. Write verbs have no `--dry-run`: `spelling --write` and
`dogfood record` edit files with no preview, though the family standard says a
write verb has a dry run. `quickstart` and `dogfood` refuse `--json`, so the
every-verb-accepts-json shape is broken exactly on the first-run and receipt
verbs. And `dogfood --tools` re-asks all 18 binaries for their verbs on every
run, which is a lot of subprocess work for one receipt.

Not run: `convergence` needs a forge read through gh, and `floors`, `corpus`
and `attest` need a seed pair, a ledger and a manifest that a cold user would
have to construct; each was judged from its help and `-h`. I had to guess
almost nothing: every refusal told me the next command.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-check spelling --dir spelljob --write` | a write verb edits files in place with no dry-run, so a cold user commits the edit with no preview | add a dry-run that prints the planned fixes and writes nothing | M |
| 2 | `nova-check quickstart --dir linksjob --json` | quickstart and dogfood refuse --json, breaking the every-verb-accepts-json shape on the first-run and receipt verbs | accept --json on quickstart and dogfood, or state the exception in the banner | M |
| 3 | `nova-check dogfood record --tools bin --tool nova-check --verb links --by trial --ok --notes x --receipts receipts` | each dogfood run spawns all 18 binaries' help to rebuild the verb list | cache the verb list per tools directory for the run | S |
| 4 | `nova-check spelling --dir spelljob --json` | spelling --json resolves --dir to an absolute path while links --json echoes it as passed | resolve or echo the path the same way in both verbs | S |

## Good, keep
The one-line refusal with `run: nova-check help` and the hint that says what a
missing flag wants are the most useful error text I have seen; keep them. The
--json facts-plus-items shape and the exit-code discipline (0 pass, 1 finding,
2 could not run) must not be lost.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a green over zero files (spelling, links) | STILL THERE | `nova-check links --dir emptydir` prints `LINKS OK files=0 links=0` and exits 0 with no warning; nocode now warns |
| two refusals name one problem | FIXED | `nova-check attest` with no flags names --home and --manifest in one run, each with its hint |
| every remedy is the whole help | FIXED | `nova-check links` with no --dir prints one line naming --dir and its hint, not the banner |
