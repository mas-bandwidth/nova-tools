# nova-work

## What it is

nova-work: every issue of an organization's repositories in one tree file, verified field for field

## Why use it

Keep every GitHub issue of an organization in one file you can check. nova-work is pre-alpha: not ready for production use.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-work@latest
nova-work version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-work).

Run by `cmd/nova-work/firstrun_test.go` against a recorded conversation with
GitHub (`internal/workgh/testdata/reliable`: one public repository of twenty
issues, read at fifteen a page), so no network is used and no process is
started. `$ORG` and `$REPO` are yours: the test stands them for the recording's
organization and repository, and the counts below are that repository's. `gh=`
names the gh a run used; yours is the gh on your PATH, and here it is `./gh`,
where the test says it found one, while every call is answered from the
recording. The `sha256` is the tree file's, and the tree records the instant it
was fetched, so it differs on every real run; the test passes a fixed time in
so the value below reproduces. `./tree.lisp` is a file in a directory of the
test's own. The usage banner's `example:` block is this same sitting, line for
line.

```text
$ nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --dry-run
IMPORT OK org=$ORG out=- repos=1 issues=20 comments=74 references=4 linked_prs=2 bytes=65206 sha256=492ee7e0aaeb987b3c8935a01dd5194bb8826f9742d20575d24d090d04fb4a71 calls=3 points=3 rest=0 seconds=0.0 gh=./gh dry_run=true
IMPORT PLAN repos=1 issues=20 est_calls=3 max_calls=1500 page_size=15
IMPORT REPO repo=$ORG/$REPO issues=20 comments=74 references=4 linked_prs=2 calls=2
IMPORT NOTE the dry run read GitHub as the import does (calls=3, read-only) and wrote nothing

$ nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --out ./tree.lisp
IMPORT OK org=$ORG out=./tree.lisp repos=1 issues=20 comments=74 references=4 linked_prs=2 bytes=65206 sha256=492ee7e0aaeb987b3c8935a01dd5194bb8826f9742d20575d24d090d04fb4a71 calls=3 points=3 rest=0 seconds=0.0 gh=./gh
IMPORT PLAN repos=1 issues=20 est_calls=3 max_calls=1500 page_size=15
IMPORT REPO repo=$ORG/$REPO issues=20 comments=74 references=4 linked_prs=2 calls=2

$ nova-work verify --tree ./tree.lisp --repo $ORG/$REPO --page-size 15
VERIFY OK tree=./tree.lisp sha256=492ee7e0aaeb987b3c8935a01dd5194bb8826f9742d20575d24d090d04fb4a71 repos=1 issues=20 comments=74 calls=3 points=3 rest=0 seconds=0.0 differences=0 missing=0 extra=0 drift=0 gh=./gh
```

## Verbs

The [nova-work section of the command reference](../../docs/CLI.md#nova-work) documents every verb's flags, effect and exit codes.

- `import`
- `verify`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-WORK-V1.md](https://github.com/mas-bandwidth/nova-sprint/blob/main/docs/SPEC-WORK-V1.md).
