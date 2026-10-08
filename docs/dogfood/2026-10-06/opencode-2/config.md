# Dogfood: nova-config — 2026-10-06, opencode-2

Card `dogfood-opencode-2-config-b.w1~15.g17`, tier pro, base
`sprint/mechanical-2026-10-02`, tip `768701114b5b`. A stranger's run: read only
`nova-config -h`, `nova-config help`, `nova-config <verb> -h` and
`docs/SPEC-CONFIG.md`, then used the checkout's own binary for real against a
`--file` scratch store (`scratch/try.json`), a saved copy of OpenRouter's live
model list, and a loopback Redis read-only (`apply --dry-run`/`--check`,
`status`, `inventory`; no write was made to any store). No code changed. Every
kind and every verb ran at least once, the refusals too.

## Findings

### 1. `route prices --refresh` cannot read today's OpenRouter list — URGENT

Command:

`nova-config route prices --refresh --as a1 --file scratch/try.json`

Printed (both the live fetch and `--from` of a copy saved from that URL):

```
ROUTE-PRICES REFUSED: https://openrouter.ai/api/v1/models: the models list is not the JSON OpenRouter publishes: json: cannot unmarshal array into Go struct field .data.pricing of type string; run: nova-config help
```

Expected: the list to be read and `PRICES SET`/`PRICES DRY-RUN` lines with the
enabled routes' price fields, as this verb's help and the "route prices"
section of `docs/SPEC-CONFIG.md` promise. Instead the whole verb is dead against
today's list: 80 of the 465 models OpenRouter returns carry a `pricing.overrides`
entry (an array), and the parser wants every value under `pricing` to be a
string, so one model's optional override refuses the entire refresh. `--dry-run`
does not help, and `run: nova-config help` cannot fix it. The pricing pipeline
v1.1.0 depends on is unusable at this tip.

### 2. `route prices -h` says it is a local file write — URGENT

Command:

`nova-config route prices -h`

Printed:

```
usage: nova-config route prices [flags]
from `nova-config help`:
  nova-config route prices --refresh [--provider <provider>] [--from <path>] [--pg <dsn> | --file <path> | --seat <seat>] [--dry-run] --as <name>
```

and, at the end of the same help:

```
effect: local write: writes files on this machine: writes the enabled routes' price fields in the config store
```

Expected: `effect: store write: ...`, as every other verb that writes a row
prints (`route set` says `store write: one row and its history row, in PostgreSQL or the --file`). This verb writes route rows in the config store, not
files on the machine; the only verbs that legitimately say `local write: writes files on this machine` are `login` and `logout`. A reader choosing whether to
run it, and deciding what a `--dry-run` leaves behind, is told the wrong effect.

### 3. `inventory` refuses `--json` — NEXT

Command:

`nova-config inventory --example --json`

Printed:

```
{"result":{"verb":"inventory","status":"refused","exit":2,"remedy":"nova-config inventory -h","why":["unknown flag --json; this verb takes --example, --fixture, --host, --list, --redis, --timeout"]},"facts":{}}
```

Expected: every verb accepts `--json` (the standard, and every other reading
verb here does); `inventory -h` does not list it, so the tool is consistent with
its own help but short of the one shape. `--example` and a live fixture read
both have useful JSON.

### 4. `route prices` refuses `--json` and its refusal object has no verb — NEXT

Command:

`nova-config route prices --refresh --from clean.json --json --as a1 --file scratch/try.json`

Printed:

```
{"result":{"verb":"","status":"refused","exit":2},"facts":{},"notes":["ROUTE-PRICES REFUSED: unknown flag --json; the flags of route prices are --as, --dry-run, --file, --from, --pg, --provider, --refresh, --seat; run: nova-config route prices -h"]}
```

Expected: `--json` on every verb, and — since the tool chose to answer this
refusal in its JSON shape — a `"verb"` of `route prices`, a `"why"`/`"remedy"`,
not an empty verb with the refusal buried in `notes`. Two renderings of one
value cannot drift; here they already have.

### 5. A bare bool flag refuses with a message that misstates the problem — NEXT

Command:

`nova-config machine add mt --user u --seat s --slots 1 --tla --as a1 --file scratch/try.json`

Printed:

```
nova-config machine add REFUSED: want machine add <name> --<field> <value> ...; flags follow the name; run: nova-config machine add -h
```

`nova-config loop add l3 --machine m1 --argv '["echo"]' --keepalive --as a1 --file scratch/try.json`
prints the same shape (`loop add`). Expected: `--tla` (and `--keepalive`) want
an explicit `true`/`false`, and the refusal should name the flag and its want;
instead `--tla` swallows the next token (`--as`) as its value and the reader is
told the flags do not follow the name, which they do. `--tla true` and
`--keepalive true` work, but the reader only learns that by guessing.

### 6. A default width reads `-` in list/show and `default` in `machine width` — NEXT

Command:

`nova-config machine list --file scratch/try.json`

Printed:

```
MACHINE name=m1 user=nova seat=s1 slots=8 runners=0 width=0 tla=false note=-
MACHINE name=m3 user=nova seat=s3 slots=2 runners=0 width=- tla=false note=-
CONFIG LIST kind=machine rows=2
```

The same row through the dedicated verb:
`nova-config machine width m3 --file scratch/try.json` prints
`CONFIG WIDTH machine=m3 width=default member=true`. Expected: one spelling for
"unset, so half the machine's cores at sync". `-` normally means empty, but
here it means a resolved default and a member of the sprint fleet; a stranger
reading `machine list` cannot tell machine `m1` (`width=0`, not a member) from
`m3` (`-`, a member at its default).

### 7. A `set` of the value the row already has still stamps a revision — NEXT

Command (machine `m1` was already `width=0`):

`nova-config machine set m1 --width 0 --as a1 --file scratch/try.json`

Printed:

```
CONFIG SET kind=machine name=m1 rev=24 changed=width
```

Expected: no new revision and no `changed=` when the row would be identical
(a steady apply is a no-op); instead the history grows and every later `apply`
sees a revision to compare, while `changed=width` reports a change that did not
happen. A `set` of the same `tier` array behaves the same way (`CONFIG SET kind=tier name=flash rev=20 changed=routes` after an identical set).

### 8. Raw Redis pool log lines leak to stderr before the clean refusal — NEXT

Command:

`nova-config apply --file scratch/try.json --redis 127.0.0.1:6399 --as a1`

Printed (first 3 of four such lines; the refusal is the fifth line):

```
redis: 2026/10/07 11:58:15 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
redis: 2026/10/07 11:58:15 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
redis: 2026/10/07 11:58:16 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
```

then `nova-config apply REFUSED: redis: read machines: dial tcp 127.0.0.1:6399: connect: connection refused; run: nova-config apply -h`. Expected: the tool's
one refusal line in its own grammar, without the Redis client library's internal
log lines and a `pool.go:762` filename a reader cannot act on. `status`,
`inventory --list` and `machine list --redis` all print the same noise.

### 9. Write verbs have no `--op` idempotency flag — NEXT

Command:

`nova-config machine add m9 --user u --seat s --slots 1 --op abc --as a1 --file scratch/try.json`

Printed:

```
nova-config machine add REFUSED: unknown flag --op; this verb takes --as, --dry-run, --file, --json, --note, --pg, --runners, --seat, --slots, --tla, --user, --width; run: nova-config machine add -h
```

Expected: `--op <id>` on every write verb, as the standard requires, so a retry
after a timeout returns the recorded result and changes nothing. No verb in
`nova-config` accepts it, so a retried `add` today is a duplicate refusal, not a
safe replay.

### 10. Several `run:` breadcrumbs do not run as printed — NEXT

Commands and their printed `run:` lines:

```
nova-config sprint set --decide_bounce 1.5 --as a1 --file scratch/try.json
nova-config sprint set REFUSED: sprint: decide_bounce 1.5 is not a probability in [0, 1]; set both bars (--decide_bounce and --decide_review), or both empty to turn the decide read off; run: nova-config sprint show --file scratch/try.json
```

```
nova-config route set r1 --usd 0 --as a1 --file scratch/try.json
nova-config route set REFUSED: route r1 has --usd 0; want a dollar budget above 0, or --usd "" (empty) for no cap; run: nova-config route show r1 --file scratch/try.json
```

```
nova-config status --file scratch/try.json --redis 127.0.0.1:6381
nova-config status REFUSED: Redis is not at the store's revision for 6 kind(s); run: nova-config apply --file scratch/try.json
```

Expected: the breadcrumb to be the command that fixes the refusal. The first
two send the reader to a read (`show`) when the fix is a `set`; the last drops
the `--redis` the reader named, so pasting it verbatim refuses
`--as is required ... --redis is required ...` at exit 2. The prose beside each
refusal is right; only the `run:` command is not runnable.

### 11. `route prices` unknown-provider remedy sends the reader to the banner — NEXT

Command:

`nova-config route prices --refresh --provider anthropic --as a1 --file scratch/try.json`

Printed:

```
ROUTE-PRICES REFUSED: anthropic publishes no list this verb reads; the lists: openrouter, opencode (assumed from openrouter); run: nova-config help
```

Expected: `run: nova-config route prices -h` (the verb's own help, which lists
`--provider` and its two values), the shape every other usage refusal here uses;
`nova-config help` is the banner and repeats nothing about providers.

## What was not done

- No write reached a live store: the card forbids starting a server, the only
  loopback Redis is the fleet's own applied view, so `apply`'s write path,
  `inventory` against applied state, and `machine list/show --redis` against
  live beats were exercised only read-only (`apply --dry-run`/`--check`,
  `status`, `inventory --example`/`--fixture`) or through the connection-refused
  path above. `apply --check` against the loopback Redis printed `CHECK ADD/SET/REMOVE` lines and a `CONFIG CHECK` summary at exit 0.
- `login`/`login --check` with a resolvable secret were not run: no nova-secrets
  store was available, so only the no-login and unresolvable-store refusals and
  `logout` ran. `--seat` was exercised through its unknown-seat refusal.

## Verification

Run on the build bench (go never runs on the working machine), after this file
is present:

```
go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent
go test -count=1 -timeout 600s ./internal/docs ./internal/ci
```

READ 7/10 — the banner, every verb's `-h`, and `docs/SPEC-CONFIG.md` are far
more complete than most tools here, and refusals name every independent problem
at once with a remedy; it loses points for two verbs whose `-h` omits `--json`,
one effect line that describes the wrong kind of write, and `run:` breadcrumbs
that point at a read or at a command missing the flag it was given.

USE 6/10 — the `--file` store makes every verb runnable with no database and
most behave exactly as their help says; it loses points because `route prices`
is dead against the live provider list, no write verb can be retried safely
without `--op`, a set of an unchanged value still stamps a revision, and raw
Redis pool logs precede the clean refusal.

urgent=2 next=9
