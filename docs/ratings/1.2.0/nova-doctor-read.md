# nova-doctor READ and USE rating, nova-tools 1.2.0

Rater: a cold rater,
build: 17ec8d256a04
READ: 7/10
USE: 7/10

## Reasons

READ. The README states the tool in one sentence: "says what is missing for the nova tools to work, and the one line that fixes each". The Verbs section points at docs/SPEC-DOCTOR.md for the full reference.

What holds READ at 7: the First run block shows `run --check harness` but docs/SPEC-DOCTOR.md lists more check kinds than the README documents. The README says "Find out what is missing for the nova tools to work on this machine" but the spec defines what "this machine" means: the host OS, installed tools, and configuration files.

USE. The tool runs without a server. The run verb checks registered diagnostics and prints failures with remediation lines. The --local flag skips remote checks. The --json flag prints results as JSON.

What holds USE at 7: the README doesn't show what a check failure looks like or what exit code a failure produces. The --json output shape is mentioned but not illustrated.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:19-22 | The First run block shows `run --check harness` but the spec at docs/SPEC-DOCTOR.md:1 lists more check kinds | List the check kinds or point at the spec section | S |
| 2 | README.md:12-14 | The Why use it section says "Find out what is missing" but doesn't define what "this machine" means | Add a sentence defining the host, tools, and config scope | S |
| 3 | README.md:25-28 | The First run block shows commands but no output, and the README doesn't show what a failure looks like | Add an example output block showing a failure line | S |

## Good, keep

The install section shows the go install pattern and version check. The --local and --json flags are documented. The spec link is explicit.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0: nova-doctor did not exist | NEW | first 1.2.0 rating |
