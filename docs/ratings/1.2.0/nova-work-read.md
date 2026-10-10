# nova-work READ and USE rating, nova-tools 1.2.0

Rater: a cold rater,
build: 17ec8d256a04
READ: 7/10
USE: 7/10

## Reasons

READ. The README states the tool in one sentence: "every issue of an organization's repositories in one tree file, verified field for field". The First run block shows import and verify commands with expected output.

What holds READ at 7: the First run transcript uses `$ORG` and `$REPO` placeholders without explaining how to substitute actual values. The --page-size flag is shown but the README doesn't explain what a reasonable page size is. The spec link at docs/SPEC-WORK-V1.md is referenced but not summarized.

USE. The tool uses gh (GitHub CLI) to read issues. The import verb reads issues from GitHub and writes a tree file. The verify verb checks that the tree file matches the current state. The --dry-run flag shows what would be read.

What holds USE at 7: the First run block doesn't show what a verification failure looks like. The gh dependency is mentioned but the README doesn't show how to install or configure it. The sha256 of the tree file is shown but the README doesn't explain how the checksum is used.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:26-46 | The First run transcript uses $ORG and $REPO placeholders without explaining substitution | Add a note showing how to substitute actual values | S |
| 2 | README.md:34 | The --page-size flag is shown but the README doesn't explain what a reasonable value is | Add a note suggesting page sizes | S |
| 3 | README.md:58-62 | The gh dependency is used but the README doesn't show how to install it | Add a gh install step or prerequisite section | S |
| 4 | README.md:37-40 | The First run block shows import but no verify failure example | Add an example showing what a verification failure looks like | S |
| 5 | README.md:44 | The sha256 is shown but the README doesn't explain how it's used | Add a note explaining the checksum usage | S |

## Good, keep

The install section shows the go install pattern. The import verb shows the full flag set. The verify verb shows the expected output format. The spec link is explicit.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0: nova-work existed, rating at docs/ratings/1.1.0/work-read.md | NEW | 1.2.0 re-rate |
