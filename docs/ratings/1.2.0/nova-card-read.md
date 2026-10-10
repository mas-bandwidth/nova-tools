# nova-card READ and USE rating, nova-tools 1.2.0

Rater: a cold rater,
build: 17ec8d256a04
READ: 6/10
USE: 6/10

## Reasons

READ. The README states the tool in one sentence: "writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help". The First run block gives three verbs with flags and expected output. The Verbs section points at docs/CLI.md for full documentation.

What holds READ at 6: the First run transcript uses `generate --from findings` but the CLI reference documents `generate` with `--input` and `--format`, not `--from`. The tool says "pre-alpha" in the Why use it section but gives no indication of what features are missing or what would make it production-ready. The spec link points at docs/SPEC-CARD-CONTRACT.md but neither the README nor CLI.md mentions the contract's name.

USE. The tool runs without a server or network. The generate verb writes markdown files to an output directory. The lint verb checks a card file and prints either LINT OK or LINT FAIL. The template verb is listed but the README gives no example of how to invoke it.

What holds USE at 6: the lint verb accepts `--card <file>` but the README doesn't show what a lint failure looks like or what the remediation is. The template verb has no example transcript, so a cold reader doesn't know what it produces or when to use it.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:28-35 | The First run transcript uses `--from findings` and `--file` but docs/CLI.md:250 uses `--input` and `--format` | Update the README transcript to match the CLI reference flags | S |
| 2 | README.md:12-14 | The Why use it section says "pre-alpha" but gives no details on what's missing or what to expect | Add a feature checklist or migration note for production readiness | S |
| 3 | README.md:44-48 | The template verb is listed with no example or description of its output | Add a First run block showing template usage and output | M |
| 4 | README.md:52 | The spec link uses docs/SPEC-CARD-CONTRACT.md but no part of the README or CLI mentions what the contract covers | Add a one-sentence description of the contract's scope | S |

## Good, keep

The install section shows the go install pattern and version check. The First run block shows the full flag set for generate. The lint verb has a clear invocation pattern.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0: nova-card did not exist | NEW | first 1.2.0 rating |
