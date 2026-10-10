# nova-work READ and USE rating, nova-tools 1.2.0

Rater: a cold rater, mercurys-2.5 under opencode
Build: 17ec8d256a04943b921987ebd9c19458917b19e9
READ: 7.5/10
USE: 7/10

nova-work: every issue of an organization's repositories in one tree file, verified field for field. The tool is pre-alpha. The tool has verbs: import, verify, version, help.

## Reasons

READ. The README is clear and shows a complete first-run transcript with concrete output. The import and verify verbs are well documented. The spec is present. What holds the score: there is no documentation about the tree file format.

USE. The import command fetches issues from GitHub. The verify command checks the tree file against the repo. What holds the score: there is no way to update an existing tree file, and the verify command does not show what differences were found.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-work/main.go:import | no --update flag to update an existing tree file | add --update that compares and patches existing tree | M |
| 2 | cmd/nova-work/main.go:verify | verify output does not show the actual differences | add a --diff flag to show differences | M |
| 3 | README.md | no documentation of the tree file (Lisp) format | add a section describing the tree format | M |
| 4 | cmd/nova-work/main.go:import | page-size default is not documented | document the default page-size in import help | S |

## Good, keep

- Complete first-run transcript with concrete numbers.
- Dry-run mode shows plan before executing.
- Verify command confirms tree file integrity.
- GitHub API calls are tracked and reported.

## Compared with earlier ratings

This tool was not rated in 1.1.0 as it did not exist. The first rating shows a functional GitHub issue importer.
