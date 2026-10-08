# nova-check review, 2026-10-05

Rater: inception/mercury-2.5 (acting for friend freddy)
Build: 3baf154bb6ef
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons

nova-check is a rigorous record-layer verification tool that enforces the no-guessing law: every path, file, and budget must be supplied via flags, missing flags are refused at exit 2 with per-flag hints, and output is machine-parseable key=value tokens with bounded listings. I read docs/CLI.md:7-240, docs/SPEC-CHECK.md:1-152, and cmd/nova-check/main.go. The core checks—attest, links, kernel, nocode, corpus, floors, quickstart—are well-designed: attest names non-canonical and absolute paths while continuing, kernel handles the --max-tokens budget, and corpus separates malformed rows from missing anchors.

However, several mechanical defects hinder unattended AI operation:
1. dogfood record and convergence lack --dry-run support despite documentation implying dry runs; automated agents cannot safely validate declarations or preview convergence without side effects.
2. Refusals across verbs point to "run: nova-check help" instead of "run: nova-check help <verb>", forcing callers to digest a 120+ line banner.
3. --json on convergence only formats successful readings; error and refusal paths emit plain text to stdout, breaking machine parsers.
4. The example-self fixture fails floors verification out of the box (lacks docs/SEED.md and the floors section in SEED-CORE.md).
5. Hygiene's flag parser discards specific error details, printing "bad flags" instead of the exact malformed flag name.

A 10 would need:
- --dry-run flags on dogfood record and convergence.
- Contextual refusal pointers to the specific subcommand help.
- Structured JSON error output when --json is specified on refusal paths.
- A self-consistent example-self fixture that passes all documented checks.
- Specific flag parsing error messages in hygiene.

First confusion: docs/CLI.md:41-43 and cmd/nova-check/main.go:354. The usage says to run quickstart, then kernel, attest, floors, corpus. But running floors against the bundled example-self fails immediately because it lacks docs/SEED.md and SEED-CORE.md lacks the floors section. An AI following the tool's own guidance hits a broken check on its designated example fixture.

First doubted claim: docs/SPEC-CHECK.md:108-110. The spec says --json prints the reading once as one object, replacing lines. But when convergence encounters an invocation error (missing --since, invalid --state, etc.), the code writes plain text to stdout without any JSON. An agent invoking --json to obtain machine-readable output receives unparseable freeform text upon failure.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-check/dogfood.go:336-353 | dogfood record synopsis lacks --dry-run. Automated callers cannot validate receipt parameters and declarations without writing files. | Add --dry-run flag to cmdDogfoodRecord that validates tool/verb but skips dogfood.Record call. | S |
| 2 | cmd/nova-check/convergence.go:82-105 | convergence lacks --dry-run. AI wanting to test convergence without altering --state file has no safe path. | Add --dry-run flag that skips next.Save(state). | S |
| 3 | cmd/nova-check/main.go:273-278 | Refusals across subcommands print "run: nova-check help" instead of "run: nova-check help <verb>". Forces scanning 120+ line banner. | In refuse(), pass subcommand name to construct "run: nova-check help <verb>". | S |
| 4 | cmd/nova-check/convergence.go:168-175 | --json on convergence only formats success. Refusals print plain text to stdout, breaking JSON parsers. | Format refusals and parse errors as JSON error objects when --json is set. | M |
| 5 | cmd/nova-check/testdata/example-self/ | example-self fails floors verification (lacks docs/SEED.md and ## The floors section). Follows the tool's own guidance. | Add minimal docs/SEED.md and complete ## The floors section in SEED-CORE.md. | S |
| 6 | cmd/nova-check/dogfood.go:385-388 | --closes validation error lacks the standard refusal suffix "; run: nova-check help". Diverges from binary's grammar. | Route --closes validation through refuse(stderr, " dogfood record", ...). | S |
| 7 | cmd/nova-check/hygiene.go:42 | Hygiene discards flag parse error details, printing "bad flags" instead of exact flag name. Other verbs report specifics. | Include oneline.Cap(err.Error(), oneline.TailBytes) in the refusal message. | S |
| 8 | cmd/nova-check/main.go:343-354 | quickstart prints "QUICKSTART OK" twice even when worst-exit=1. The pass token OK appears before checks run. | Print "QUICKSTART FAIL" on both lines when worst != 0. | S |

## Good, keep

- Strict non-guessing ergonomics: every path and budget must be supplied explicitly; missing arguments trigger exit 2 and name all omitted requirements with actionable hints.
- Bounded and machine-parseable output: all listings enforce --fail-max caps with explicit MORE lines carrying the exact run to lift the cap.
- Separation of verification concerns: quickstart executes zero-config structural checks, dogfood enforces non-author gates, and convergence mechanizes health metrics.
