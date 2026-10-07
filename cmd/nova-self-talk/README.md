# nova-self-talk

## What it is

nova-self-talk: flags sentences where a writer passes a standing verdict on themselves

## Why use it

Review how you write about yourself.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-self-talk@latest
nova-self-talk version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-self-talk).

Fixture: `cmd/nova-self-talk/testdata/example-pages`, built into the binary:
`nova-self-talk example ./pages` writes it to `./pages`, which is what the lines below type.

```text
$ nova-self-talk ./pages/journal.md   # Stderr: whole
! SELFTALK FAIL ./pages/journal.md:4: STANDING match="cannot check": I cannot check my own work, so the second read went to someone else.
! SELFTALK FAIL ./pages/journal.md:10: RANKING match="worst habit I have": It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=1
SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2
SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

$ nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md   # Stderr: whole
SELFTALK RULEDOC ./pages/RULES.md: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule
! SELFTALK FAIL ./pages/RULES.md:8: VERDICT-IDIOM match="dead as a practice": A rule weakened to improve a score is dead as a practice: the score got better and the wall got thinner.
! SELFTALK FAIL ./pages/journal.md:4: STANDING match="cannot check": I cannot check my own work, so the second read went to someone else.
! SELFTALK FAIL ./pages/journal.md:10: RANKING match="worst habit I have": It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=2
SELFTALK FAIL files=2 claims=2 standing=1 installations=2 dated=1 shown=3
SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

$ nova-self-talk --skip RULES.md ./pages/RULES.md ./pages/journal.md   # Stderr: whole
SELFTALK SKIP ./pages/RULES.md (--skip)
! SELFTALK FAIL ./pages/journal.md:4: STANDING match="cannot check": I cannot check my own work, so the second read went to someone else.
! SELFTALK FAIL ./pages/journal.md:10: RANKING match="worst habit I have": It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=1
SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2
SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.
```

## Verbs

The [nova-self-talk section of the command reference](../../docs/CLI.md#nova-self-talk) documents every verb's flags, effect and exit codes.

- `scan`
- `shapes`
- `example`
- `version`
- `help`

## Spec

The contract is [docs/SPEC.md](../../docs/SPEC.md).
