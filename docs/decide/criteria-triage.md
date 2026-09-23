# Criteria: the issue pre-triage question

version: 2026-09-19.2

Question file: `questions-triage.json`, which names this file and its version. The pair is loaded
together and this text is carried INTO the request above the state. `internal/decide` refuses the
pair when the versions disagree, and refuses a state that does not carry a declared typed field,
before any provider call is made.

**The answer routes and never admits.** No classification at any confidence cuts a card, lifts a
hold or skips a read. The counts below are observations from one day in one repository; they are
kept because they say which FACTS the question was missing, and they tune nothing.

## The failure this version fixes

On 2026-09-19 Jev triaged 49 open issues and a manager took its 32 `mechanical-card` picks to the
tree. **Two of the 32 could be cut.** The other 30 failed on facts Jev could not see, because it
was shown the issue and never the repository:

| why the pick could not be cut | n |
| --- | --- |
| the file is held by an open pull request (alone, or with too-big or an infra prefix) | 16 |
| already fixed or half-fixed on dev, or the cited path is not on dev in that shape | 6 |
| the ask is a design ruling the issue's own text asks for | 5 |
| an infra prefix this lane may not touch | 2 |
| not a card: a sweep of N cards rather than one | 1 |

**Twenty-two of those thirty are answered by three yes/no facts.** So the asker computes them BEFORE the
question and the question carries them.

## The three tree facts the asker computes first

| field | how | refutes |
| --- | --- | --- |
| `held_by_open_pr` | `gh pr list --state open --json number,files` and match the issue's named paths | 16 of the 30 |
| `cited_path_exists_on_dev` | the issue's `file:line` against the current dev tree | the not-on-dev and half-fixed classes, 2 of the 30 |
| `last_comment_says_fixed` | the issue's last comment and its state | the already-fixed class, 4 of the 30, where NO commit cites the issue |

## The issue fields the state also carries

`title` and `labels` as GitHub holds them; `body_bytes`, the body's size, because a long body that
names no file is usually a discussion and not a defect; `files_named`, the paths the body cites;
`age_days`; and `issue_is_open`, because four of the day's thirty were closed
already and nothing in the thread said so.

## The five answers

* **mechanical-card** — the seven conditions below.
* **design-ruling** — the issue asks a question rather than reporting a defect.
* **already-fixed** — the behaviour is on dev, or the issue is closed.
* **held** — `held_by_open_pr=yes`. A fact about the tree, not a judgement about the issue.
* **`repository-machinery`** — the work is in the repository's own machinery rather than its tools: CI
  workflows, `tools/` and `scripts/`, the swarm and pulse launchers, the sandbox, or anything
  touching keys, credentials or a destructive operation's guard. A stronger model and a person own
  this class, and a card lane may not touch those prefixes at all.

## What "mechanical" means

ALL of these, and the absence of any one of them is not mechanical:

1. exact PATHS, or a red test that exists and the issue names;
2. the repair is a procedure — a wrong line, a missing flag, a stale count, a refusal with no door;
3. acceptance is one sentence;
4. no design ruling is needed;
5. `held_by_open_pr=no`;
6. `cited_path_exists_on_dev=yes`;
7. `last_comment_says_fixed=no`.

## Three that were mechanical (a local trial record, 2026-09-19)

* **#1808** — `internal/worklang/expand.go:173/176/189` return at the FIRST missing required
  `:node` field. Exact lines; `internal/worklang` held by no open pull request; no test in the tree
  asserts the three refusal strings. Cut, landed as PR #1888.
* **#1788** — `internal/jobs/jobs.go:77-78` append the forward and reverse edge with no set check,
  and `cmd/nova-work/deps.json` carries the receipt `"needs":["b","b","b","b"]`. `internal/jobs`
  held by no open pull request. Cut, landed as PR #1889.
* **#1767** — `docs/TESTS.md` shows escaped double quotes where `board.Quote` single-quotes them,
  found by a comparator that already exists. One line, one expected string.

## Three that were not, and which field would have said so (same trial)

* **#1716** — Jev answered `mechanical-card` at 0.98 and it is a correct reading of the issue. PR
  #1873 was already open carrying exactly that repair. `held_by_open_pr=yes`.
* **#1509** — Jev answered `mechanical-card` at 0.74. The issue's own last paragraph says "What I
  am asking for, not deciding", which makes it a `design-ruling`; `docs/TESTS.md` is also held by
  13 open pull requests. `held_by_open_pr=yes` and the body's own words.
* **#1849** — Jev answered `mechanical-card` at 0.98. Commit `e61ec2da` on dev implements it in
  full and no issue comment says so. `cited_path_exists_on_dev` is yes but the behaviour is
  already there; `last_comment_says_fixed=no` is exactly why a human had to look, and it is why
  the asker computes the fact rather than trusting the comment thread.
