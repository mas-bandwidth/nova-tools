# nova-up READ and USE rating, nova-tools 1.2.0

Rater: a cold rater,
build: 17ec8d256a04
READ: 5/10
USE: 5/10

## Reasons

READ. The README states the tool in one sentence: "sets up nova on one machine, from nothing to a first sprint". The First run block shows three commands: `nova-up --local --dry-run --root ./nova-try`, `nova-up up -h`, and `nova-up version`.

What holds READ at 5: the First run block shows no output, so a cold reader doesn't know what to expect. The --local flag is used without explaining what it does. The Verbs section points at docs/SPEC-UP.md but doesn't summarize what the spec covers. The --root flag is shown but the README doesn't explain what nova-up creates in that directory.

USE. The tool runs without a server. The up verb sets up nova on a machine. The --local flag skips remote setup. The --dry-run flag prints what would be done.

What holds USE at 5: there is no example showing the full up flow with output. The spec at docs/SPEC-UP.md is referenced but the README doesn't show what files nova-up creates or how to verify the setup.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:25-28 | The First run block shows commands but no output | Add example output showing what nova-up creates | S |
| 2 | README.md:25 | The --local flag is used without explaining what it does | Add a flag description or link to spec | S |
| 3 | README.md:25 | The --root flag is shown but the README doesn't explain what nova-up creates in that directory | Add a section describing the created structure | M |
| 4 | README.md:37-40 | The spec link is referenced but not summarized | Add a one-sentence summary of what the spec covers | S |

## Good, keep

The install section shows the go install pattern. The --dry-run flag is documented.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0: nova-up did not exist | NEW | first 1.2.0 rating |
