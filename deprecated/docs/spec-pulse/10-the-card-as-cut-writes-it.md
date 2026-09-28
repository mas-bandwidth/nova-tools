## The card, as `cut` writes it

```
RESULT: <label> sha=<sha12>
You are a worker. Job directory only; the TMPDIR the runner already exported, never one of your own; never /tmp, ~ or ..; no stdlib or toolchain source; the deadline is the machinery's: <s> s.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<owner>/<name>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints <head>. Else write RESULT.md with line 2 BLOCKED head=<yours> and stop.
STEP 2..n. <the template's numbered steps, one command per line, one check each>
STEP last. Write RESULT.md: line 1 exactly the line 1 of this card; line 2 one of DONE, ABSTAIN <why>, BLOCKED <why>; then BRANCH <branch>; REPO <owner>/<name>; then the RESULT template's sections.
```

Line 1 carries the colon: `RESULT: ` is SPEC-SWARM's form (SPEC-SWARM.md:969,976) and the one
form every writer and reader converges on (SPEC-TOOLWORK §5 rule 7; the plain `cut` template
path, `internal/pulse/cut.go:472`, is the renderer that still follows, by that rule's card).
**A card of a kind in SPEC-TOOLWORK §5's table carries five typed header lines between the role
line and `STEP 1`** — `KIND:`, `PATHS:`, `TEST:`, `LEGS:`, `SOURCE:`, and `MODE: explore` with
`TURNS: <n>` when the card has more than three model steps — so for those kinds `STEP 1` is line
8 (or 10), inside the fifteen the admission check reads (SPEC-TOOLWORK §5 rule 1). The templates
of this document that are not kinds in that table keep the shape above: `STEP 1` on line 3.

`<sha12>` is the first twelve hex of the SHA-256 of everything below line 1, so the contract
line binds the card it heads; the swarm records the same hash at admission and refuses a
`RESULT.md` whose line 1 differs (SPEC-SWARM, **gather**). Text templates add rule 6's line
after line 2. `<head>` is the repo's default-branch head at cut time, read once per repo per
`cut` run; a candidate whose repo cannot be read is `skipped` with the reason, never cut blind.
