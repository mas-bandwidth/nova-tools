# nova-swarm dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-swarm -h`, `nova-swarm help`, `nova-swarm <verb> -h` and the page under `docs/`. Built from the staged checkout at
c02c00769c229a6e012b0dff03ef8929016f3b23 and used as
`nova-swarm v1.0.1-0.20261006193659-927b3a9496f0 darwin/arm64 go1.27.1`: the
no-setup verbs (`template`, `lint`, `lint --rules`, `doctor`, `verify`, `version`)
once each in a scratch dir, a template card written and linted, and the refusals.

## Findings

1. Verb-level refusals omit the `REFUSED` status word, while the same tool's
   bare and unknown-verb refusals carry it:
   ```
   nova-swarm lint: --card is required; it wants the path to the card file whose shape is checked before any spend; ... refusing to guess
   nova-swarm template: --name wants one of capacity, card, drift, fit, ... got "nope"
   nova-swarm verify: --result is required; it wants the path to the job's RESULT.md ... refusing to guess
   ```
   against `nova-swarm REFUSED: unknown verb "bogus"; ...`. docs/STANDARD.md
   section 2 gives one grammar, `VERB REFUSED: <reason>; run: <remedy>`, and a
   reader (or a script) matching `REFUSED` misses these three. The wants are
   named, so this is the grammar, not the remedy.
   Grade: NEXT.

2. `nova-swarm doctor` prints a build stamp that is not the build answering:
   ```
   DOCTOR OK stamp=nova-swarm v1.2.0-dev.7a1152a darwin/arm64 go1.26.6
   ```
   while `nova-swarm version` in the same binary prints `nova-swarm v1.0.1-0.20261006193659-927b3a9496f0`. The doctor's line is the
   `~/.local/bin` stamp (its subject), but it does not say so, so a reader who ran
   the two verbs cannot tell which build is speaking; the doctor is the one place
   that matters.
   Grade: NEXT.

## What the tool got right

- The no-setup first run is real: `template --name card` writes a 24-line card,
  `lint --rules` prints 45 rules each with its remedy, and `lint card.md` returns
  `LINT OK card=card.md checks=45 bytes=2929 cap=12000` plus per-placeholder
  `LINT NOTE ... remedy=` lines.
- The bare command names the one verb that only looks (`template --name card`)
  and the unknown verb names every verb.
- `template --name nope` and `lint` on a missing file name what the input wants
  and the alternatives.
- `doctor` reports each harness with its binary, version and login state, and
  exits 0.

READ 8/10 — the banner answers what it does, how it works and where its state
lives, and `lint --rules` is a complete reference; the refusal grammar and the
doctor stamp keep it from a 9.

USE 8/10 — `template`, `lint` and `doctor` run with nothing set up and a card
can be written and linted in two commands; a verb-level refusal that does not
lead with `REFUSED` is the stumble for a reader matching the grammar.

urgent=0 next=2
