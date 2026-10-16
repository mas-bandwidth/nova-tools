# Tool review, 2026-10-15: is this a good tool for AIs to use?

One review per tool and reviewer at `<tool>/<reviewer>.md`, one `<tool>/SUMMARY.md` per tool that merges them, and one `RANKING.md` over every tool. Each review runs the tool's own help and real read-only commands, scores the eleven criteria below, and sorts every finding into 10/10 blockers (broken behaviour, jank, friction, wrong or missing help or docs, bad or unparseable output, a refusal that does not name the next command, a rough first run) and nice-to-haves (everything else, including any new feature).

## Rubric (each 0 to 5, the evidence the exact command and its output line)

1. cold: usable cold from the tool's own help, with no other doc.
2. refusals: every refusal names its cause and the next command to run.
3. output: parseable, a stable line grammar, and --json where the help offers it.
4. dry-run: a --dry-run exists for every write, and it writes nothing.
5. idempotent: a write takes --op, and the same op again changes nothing.
6. no prompts: nothing waits on an interactive answer.
7. exit codes: meaningful and as the help states them.
8. one word: one word per thing, with no duplicate verbs or tools.
9. push: where state is waited on, it is pushed rather than polled.
10. no leaks: no error or output line leaks a secret.
11. true help: every claim in help is true when run.
A criterion the tool has no surface for (a read-only tool has no write to dry-run) scores 5 with the evidence `n/a: <why>`. Score: the sum of the eleven over 55, then out of 10 to the nearest half point.

## Form

```
# nova-<tool> AI-usability review, 2026-10-15

Reviewer: <your model as you know it; no person, machine or friend>
Build: <the 12-hex head you built>
Score: <n>/10 (<sum>/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | <0-5> | `<command>` -> `<output line>` |
(one row for each of the eleven)

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `<exact command>` | <the bad output or behaviour, quoted> | <the one change> | <S, M or L> |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|

## Top 3 fixes
1. <the fix that moves the score most, ranked>
2. ...
3. ...

## Good, keep
<two or three lines that must not be lost>

## What could not be tried
<each verb not run, and why>
```
Blockers are ordered by what each costs the score, most first. S is under 30 lines.
