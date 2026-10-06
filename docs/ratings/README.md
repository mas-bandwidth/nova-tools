# docs/ratings: cold ratings of the tools

One file per release, tool and rater. `go test ./internal/docs -run TestRatingFileIsInForm`
(internal/docs/ratings_form_test.go, checker in internal/docs/ratings_form.go) holds every
file to the form below and names the file and each missing part when one is out of form. It
reads only this tree and runs in well under a second.

## The form, from release 1.2.0 on

Every release directory at or after 1.2.0 holds only rating files named
`docs/ratings/<release>/<tool>-<friend>.md`, one combined READ and USE rating each. Every such
file has:

- above its first `##` heading, the header lines
  - `READ: n/10` and `USE: n/10`, where n is a number from 0 to 10 with at most one decimal
    (`7`, `7.5`, `10`);
  - `Build: <commit>`, the hex commit (7 to 40 characters) of the build that was rated;
- the sections, each as a `##` heading:
  - `## Reasons`
  - `## Findings`: a table whose header has a `where` column (the place: a file and line, or
    the command that shows the finding) and a `fix` column, with at least one row, and a place
    and a fix in every row;
  - `## Good, keep`
  - `## Compared with earlier ratings`

A file missing any of these fails the test, for example
`docs/ratings/1.2.0/nova-swarm-<friend>.md: missing section ## Good, keep` or
`docs/ratings/1.2.0/nova-swarm-<friend>.md: missing finding 3: a fix`. Any other file or directory
inside a release directory fails as `not a <tool>-<friend>.md rating file`.

## Earlier releases and snapshots

`1.1.0/` is the earlier split form (a `<tool>-read.md` and a `<tool>-use.md` per tool, with
`Score:` lines, the friends' files in their own directories) and `snapshots/` holds copies taken
at a commit. Both are records, kept as written; the test does not check them.
