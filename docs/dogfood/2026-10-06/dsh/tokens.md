# nova-tokens dogfood — dsh (zhi), 2026-10-06

Read as a stranger: only `nova-tokens -h`, `nova-tokens help`, `nova-tokens help <verb>`
and the tool's pages under `docs/` (CLI.md#nova-tokens, USAGE.md#nova-tokens,
SPEC-TOKENS.md). Built on the bench from the staged checkout at
6ec8bb02edc83283630f2e10e21bd035b3336f65 (`go build ./cmd/nova-tokens`) and used as
`nova-tokens devel linux/amd64 go1.26.6`: the banner's setup and `example:` block pasted
as printed, then all nine verbs with their real flags — fold on `--day` and `--all`
and `--dry-run`, report in local mode and store mode, sum, check with `--strict`,
`--no-spend`, `--through` and `--max`, sources with and without `--unattributed`,
profiles, session with and without `--out`, ledger with and without `--dry-run` against a
scratch redis this run started and stopped itself, version — plus the refusals (no
`--repos`, a missing `--out`, `--strict` with `--no-spend`, a duplicate label, `--scratch`
without `--opencode`, a bogus opencode database, a no-model session booked with `--out`,
an empty password variable, a dead store) and the `--json` renderings. About 25 minutes
of use.

## Findings

1. `nova-tokens report --redis 127.0.0.1:6401 --month 2026-09 --by day` — a store that
   does not answer is a FAILED, not a could-not-run:
   ```
   REPORT FAILED store=redis err=tokens:ledger:2026-09-01: dial tcp 127.0.0.1:6401: connect: connection refused
   ```
   It exits 1; I expected exit 2 — the set's table makes a store that did not answer a
   "could not run", and nova-tokens' own exit table names neither the dead store nor the
   empty-password case, which CLI.md calls "refused before any dial" while the tool prints
   `REPORT FAILED ... err=--user bench but NOVA_REDIS_BENCH_PASSWORD is empty` at exit 1 —
   so a script gating on 2 reads a dead store as a data-level no.
   Grade: NEXT.

2. `nova-tokens fold --out ./out2 --day 2026-09-11 --repos ./repos.tsv --claude
   bench=./badsrc` — with the only source unreadable, no day file lands at all:
   ```
   TOKENS FOLD at=2026-10-06T20:52:11Z build=devel out=./out2 sources=1 days=2026-09-11 repos=./repos.tsv
   TOKENS SOURCE label=claude:bench kind=claude path=./badsrc reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=1 messages=0 dup=0 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=0
   TOKENS NOTE a declared source could not be read whole (claude:bench): open those files to this group, or drop the flag -- a declared source is a claim that the report covers it
   ```
   CLI.md says of an unreadable source "exit 1 — and the day files still land"; here
   `./out2` held only `fold.lock`, so a following `check --out ./out2` fails as "holds no
   day file" — the sentence holds only when another source gave the day its data, and a
   stranger with one broken source gets exit 1 over an empty ledger with no day file named
   as missing.
   Grade: NEXT.

3. `nova-tokens -h` — the first run builds its fixture eight printf lines at a time:
   ```
     printf '%s' '{"type":"assistant","timestamp":"2026-09-11T09:12:' > ./transcripts/window.jsonl
     printf '%s' '00Z","message":{"id":"example-1","model":"claude-' >> ./transcripts/window.jsonl
     printf '%s' 'fable-5-1","usage":{"input_tokens":812,' >> ./transcripts/window.jsonl
   ```
   The lines run as printed and the whole example block goes green, so it works; I
   expected the sibling shape — nova-self-talk's `example` verb writes its fixture in one
   line — because a stranger's first minute here is spent assembling a one-line transcript
   a printf at a time, and one dropped quote across eight lines is a bad first run with a
   confusing payoff (a syntax error in the transcript, not a tool refusal).
   Grade: NEXT.

4. `nova-tokens version` — a plain build has no identity:
   ```
   nova-tokens devel linux/amd64 go1.26.6
   ```
   I expected the build identity to name the source it was built from; `make build` and a
   plain `go build` stamp nothing, so a report citing the binary cannot tie it to a commit
   (this one: 6ec8bb02edc83283630f2e10e21bd035b3336f65, said by me, not the tool).
   Grade: NEXT.

## What the tool got right

- Every refusal names what the flag WANTS and a runnable remedy, and one run reports
  every problem at once: `--strict` with `--no-spend` named both the conflict and the
  bad line inside the `--no-spend` file before anything ran.
- The banner's setup and six `example:` lines run as printed, green, from the binary
  alone; the session example line too.
- A dash is never a zero: `reasoning=-`, `usd=-`, `unpriced=`, and `sum` counts the
  dashes per column beside the totals (`dashes=0,0,0,0,1`).
- The exit table's `EXIT 1 STILL WRITES` is true where it applies: a fold with a good
  source wrote its day (`written=true`) while naming the unreadable one with its line and
  reason (`badline=1: lines that are not JSON`).
- `--dry-run` is the real run's own plan: fold read everything and printed
  `would_write=true` writing nothing; `ledger --dry-run` dialed no store (proved
  against a dead port).
- The password is never a flag: `--password-env`, an empty variable is caught before any
  dial with the exact remedy (`run under nova-secrets exec --only ...`).
- The Redis pair held: `ledger` indexed one hash per day into a scratch store and
  `report --redis --by tuple` grouped the month back exactly as folded, the session row
  booked under `unattributed` as documented.
- A session that names no model is refused when booking ("never under a guess; nothing
  written") at exit 2.
- `check` counts what it does not name (`gap=`, `notes=`, `stray=`), an empty `--out`
  is `CHECK FAILED`, never a green over nothing, and `--through` catches a stale ledger.
- No default paths anywhere: every path is a flag, `--out` must exist and the refusal
  says what it wants; `--scratch` without `--opencode` is refused with the sentence
  saying why.
- Every verb but `version` took `--json` with the same result as one object, and
  `help <verb>` exited 0 for all nine verbs.

READ 9/10 — the banner is a contract: how it works in five lines, the five types kept
apart, the lock, the merge semantics, the exit table with its EXIT 1 STILL WRITES clause,
and the no-defaults law stated with its three named exceptions; the eight-printf setup and
the devel identity keep it off a 10.

USE 9/10 — every verb ran first try from the banner's own example block, every refusal
carried its remedy, and the Redis pair worked against a scratch store with no setup beyond
a port; the dead-store exit code and the empty single-source ledger are the stumbles.

urgent=0 next=4
