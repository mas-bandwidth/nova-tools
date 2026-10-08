# nova-decide READ and USE rating, nova-tools 1.2.0

Rater: Alex
Build: 67eabcfe338eae3ea43d199625a55ff84996e577
READ: 8/10
USE: 8/10

## Reasons

READ. Read cold nova-decide help and every verb's -h, docs/SPEC-NOVA-DECIDE.md, and docs/CLI.md's nova-decide section. The help structure is clean: one usage line per verb, exit table, effect line on every verb. The banner's how paragraph uses pseudo-run notation (state R?, id=f) that doesn't match actual output. The spec title says "trained from its own record" but the export and training verbs are not built; section 1 says they're not built. The record's TLA+ model (tla/DecideRecord.tla) is still owed; tla/ holds no decide module. Defaults set in code (calibrate --bars, findings --bar) are not printed by -h. Brief's minutes question doesn't list options in help or refusal.

USE. Used for real on a throwaway directory with the fixed backend, no key, no network, no live store. Wrote one schema, asked two states, calibrated the record. Thirty concurrent asks into one record all succeeded. Provoked ~20 refusals: missing flags, unknown flags, unknown verb, bad backend, timeout 0, missing inputs, malformed answers file. Most exits were 2 with clear remedies. A few issues: with no --op, the same ask run seconds apart records two decisions (stamp hashes into id); some remedies aren't runnable commands; minutes options not listed; --json on help is refused or silently ignored.

A 10 needs: default id function of schema+state only; every remedy a runnable command; option names in help/refusal for fixed file choices; flag defaults printed by -h; banner's how paragraph matching actual output; spec titled for what is built; record's model landed.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-decide/main.go:751 | without --op, id hashes RFC 3339 stamp, so same ask seconds apart records two decisions | hash schema+state only like gate does; document in help that repeat returns recorded decision | S |
| 2 | cmd/nova-decide/main.go:778 | fixed answers file misfit prints "run: make --answers..." which isn't a command | make remedy a runnable command; name the answers file to fix | S |
| 3 | cmd/nova-decide/main.go:739 | brief misfit prints "run the same line again" when card says recorded=no; retry repeats failure | name the answers file and question's options as remedy | S |
| 4 | cmd/nova-decide/main.go:191 | help brief names minutes but not options; refusal doesn't list options either | list options in help brief and refusals | S |
| 5 | cmd/nova-decide/main.go:59 | how paragraph is pseudo-run not matching actual output | quote real example output or name nouns | S |
| 6 | cmd/nova-decide/main.go:57 | "jev or fixed" glossing missing; cold reader learns jev needs JEV_API_KEY only from key refusal | add gloss: jev=hosted model with JEV_API_KEY, fixed=local answers file | S |
| 7 | cmd/nova-decide/main.go:240,317 | calibrate --bars and findings --bar defaults not printed by -h | print each flag's default in -h flag list | S |
| 8 | cmd/nova-decide/main.go:174 | nova-decide help --json refused as unknown; help ask --json silently ignored | accept --json on help or refuse unknown flag after verb | S |
| 9 | nova-decide ask | missing flags/bad --backend/timeout 0/missing files all say "run: nova-decide help" instead of verb-specific help | make every verb refusal's remedy "nova-decide <verb> -h" | S |
| 10 | internal/decide/decide.go:111 | Problems is a switch; one problem per question reported; spec says every problem in one error | test each rule separately and append every problem | S |
| 11 | nova-decide ask --schema --state --answers | refusal names missing schema/state but not missing answers file | read every named input before refusing | S |
| 12 | internal/decide/import.go:136 | import glob matching no files is IMPORT OK with counts 0, exit 0, silent | refuse source glob that matches no file, or print matched=0 | S |
| 13 | nova-decide ask --backend fixed --dry-run | dry run prints only OK line, no ANSWER lines even though fixed answers known | print the answers a fixed dry run would record | S |
| 14 | nova-decide gate | with no --bars, GATE OK doesn't say bars were unset | print bars=unset on GATE OK | S |
| 15 | cmd/nova-decide/main.go:67 | every deciding verb's effect is "delivery" even with --backend fixed; classed delivery suggests network send | class effect as "local write"; note that jev sends | S |
| 16 | nova-decide outcome | conflicting label is OUTCOME FAILED at exit 1 with no run: remedy | add remedy: label another decision or keep recorded label | S |
| 17 | docs/SPEC-NOVA-DECIDE.md:192 | spec says backend failure prints "<VERB> FAIL"; tool prints "ASK FAILED" | say FAILED in the spec | S |
| 18 | docs/SPEC-NOVA-DECIDE.md:195 | dry-run list omits score and import | add score and import to the list | S |
| 19 | docs/SPEC-NOVA-DECIDE.md:1 | title says "trained from its own record" but export and training verbs are not built | title it "calibrated from its own record" | S |
| 20 | docs/SPEC-NOVA-DECIDE.md:101 | record's TLA+ model (tla/DecideRecord.tla) is still owed | land module with Ask, Replay, Attach, Act; check with TLC | M |
| 21 | docs/CLI.md:2542 | first run writes ./decisions.jsonl into checkout root; README rule says ./trial- name | use --record ./trial-decisions.jsonl in the first run | S |
| 22 | cmd/nova-decide/main.go:137 | grade's usage line omits --examples, --held-out, --seed | add them to the usage line | S |

## Good, keep

Refusals that name every missing flag in one turn with what each wants. "did you mean" for near flags. Key refusal that prints exact nova-secrets exec line. Misfit answers file reported whole. --op replay (recorded=existing), repeated label (changed=false), conflicting label at exit 1 naming both. Record under file lock: thirty concurrent asks gave thirty whole lines. calibrate refusing one-sided record and misspelt option. effect: on every verb. --json carrying refusal's why and remedy.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| without --op the id folds the clock in, so same ask run twice records twice (1.1.0 READ and USE) | STILL THERE | main.go:751; two runs a second apart gave two ids |
| banner's how paragraph is pseudo-run not matching output (1.1.0 READ) | STILL THERE | main.go:59 |
| spec title claims training while training verbs not built (1.1.0 READ) | STILL THERE | docs/SPEC-NOVA-DECIDE.md:1 |
| record's model owed (1.1.0 READ) | STILL THERE | docs/SPEC-NOVA-DECIDE.md:101; no decide module in tla/ |
| minutes options not named in help or refusal (1.1.0 USE) | STILL THERE | main.go:191 |
| misfit answers remedy not a command (1.1.0 USE) | STILL THERE | main.go:778, :739 |
| help trips on --json (1.1.0 USE) | STILL THERE | nova-decide help --json is unknown verb "--json", exit 2 |
| calibrate default bars undocumented (1.1.0 USE) | STILL THERE | main.go:240, :317; -h prints neither |
| --dry-run omits fixed answers (1.1.0 USE) | STILL THERE | ask --dry-run prints only OK line |
| GATE OK doesn't say bars were unset (1.1.0 USE) | STILL THERE | GATE OK route=caused beside class=flaky |
