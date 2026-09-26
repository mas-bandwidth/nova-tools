# SPEC-SPRINT.md — the bounded set, its wall, and where each task goes

Normative for the sprint table (nova-tools #2593; its first host, `nova-pulse
sprint`, is deleted, and the live model is nova-sprint's copy model: primaries
fanned out as worker copies, each worker pulling from its own ready set). Where this document and
the code disagree, one of them has a bug and the tests decide which.

A **SPRINT** is any current bounded set of tasks toward a goal: friends, friends
and swarm, or swarm only. Work run on the swarm is **swarm work**, never "a
sprint"; the per-bench table is the **swarm table**. As above, so below — the
same shape holds a week of sprints, a sprint, and a card's steps.

## The shapes

The store is the fleet Redis, **core types only**: ids, counts and event ids,
never a diff, a prompt, a transcript or a disposition. Every query that is not a
count lives in the fold over `ev:cards`.

```
sprint:<name>        hash    {goal, opened_at, closed_at, planned_close_at}
sprint:<name>:tasks  set     of task ids
task:<id>            hash    {kind, ref, owner, route, route_reason, state,
                              est_minutes, leased_at, done_at, actual_minutes,
                              evidence, depends_on, paths, leg, locality,
                              isolation, routes, cost_ceiling_usd, repo, base,
                              reader, priority, created_at}
q:<consumer>         stream  retired with the friend queues: a worker now pulls
                             its copies from <consumer>:cards:ready
friend:<name>        string  the heartbeat, TTL 90s, written by the friend's harness
bench:<name>         hash    the bench's own row, TTL 5s, written by its own seat
```

`depends_on`, `paths` and `routes` are comma lists. A sprint or task name may
not carry `": "` — it is the one-line grammar's separator, and a name with one
makes a status line unreadable by the thing that reads it.

**state** is COWS: `closed`, `open`, `working` (leased, with a live heartbeat).
`S` is the sprint that bounds them, and only inside one is C/(C+O+W) a fraction
that means anything.

## The line

One active sprint prints exactly:

```
14/23 61% -> ~4h
```

the fraction, the whole percent, then the **wall**. More than one active sprint
prints a table with the sprint name as the leftmost column and the same line.
`Nh left` is appended when the sprint carries `planned_close_at`. `--verbose`
adds `C=… O=… W=…`, the open items by owner and route, each item's `kind=` and
`depends=` (a comma list of task ids, or `-`), the lanes, the critical lane
and the splittable tasks on it. Nothing else goes on the line.

Hours read as `~4h`, `~1.5h`, `~45m`. A set whose open tasks carry no estimate
prints the fraction alone: an estimate nobody made is not an estimate of zero.

## The wall

The wall is **how long the sprint still takes**, never the sum of the work.

- A lane is one consumer. A consumer does one task at a time.
- A task also waits for every task it TRULY depends on: a shared PATH, or a
  symbol one task produces and another consumes. Nothing else is a dependency.
- Two tasks with the same owner and disjoint paths are **parallel**; they are
  serial only because one person types one thing at a time, which is a false
  dependency and is named as such.
- The wall is the last finish over that layout (Amdahl: the serial fraction
  bounds it). A dependency cycle is a refusal, not a number.
- **Splittable** on the critical lane = every task after its head that has no
  dependency inside the lane and no file in common with anything else on it.

`sprint wall --move <ids>` answers "what would the wall be if these moved?" and
writes nothing: it is the number to put in front of the owner before asking.

## The router

Applied mechanically, and the reason is recorded on the task. A route with no
reason is a guess.

1. **SPLIT-FIRST** — a task whose paths span more than one package comes back
   with its seams named BY FILE and is not routed until it is split. A split is
   real only when the children's paths are disjoint; otherwise the task is one
   task with two names and a rebase, and stays serial. The original owner is the
   required reader on every child.
2. A **live or security path** (`internal/secrets/`, `cmd/nova-secrets/`,
   `internal/safepath/`, `infra/`, `fleet/`, `tools/deploy/`) goes to a friend.
   A source file is not a live path because the thing it builds matters; that is
   what the required reader is for.
3. A **read, decision, review or ruling** goes to a friend.
4. Everything else is matched field by field (below).
5. **Never route to a consumer whose presence is not up.** Presence is the
   heartbeat key and nothing else; an away consumer is not in the table.

**Hand-over** is what an owner's yes looks like: the owner becomes the required
READER, the task's owner is cleared, and the router places it on the emptiest
consumer that can take it. The typing moves; the verdict does not.

## Requirements and capabilities

**There is no fixed set of queues.** `q:flash` and `q:pro` appear nowhere: there
is no evidence that a model tier is the meaningful split of the fleet (#2564),
and the things that actually decided where a card could run this week were the
toolchain leg, network locality, a warm mirror, the wall budget, the isolation
level, the dependency wave, whether the task calls a model at all, and cost.

So every task carries REQUIREMENT fields and every consumer carries CAPABILITY
fields; the match is a field-by-field comparison; and the stream is
`q:<consumer>` where the consumer is the match's **output** (that stream is the
retired friend-queue shape; the live output is the worker's `cards:ready` set).
Adding an axis is adding a field to both sides, never a new stream. The model **tier** is a field
on the task (`routes`) that the launcher reads.

| requirement (task) | capability (consumer) | refusal when they disagree |
|---|---|---|
| `leg` | `legs` | `no <leg> leg` |
| `locality` house/datacenter/any | `locality` | `locality <x>, wants <y>` |
| `repo`+`base` | `mirrors` | never refuses; a warm mirror scores higher |
| `est_minutes` | `wall` (max) | `wall <n>m over its <m>m bound` |
| `isolation` | `iso` | `no <mode> isolation` |
| `kind` | `kinds` | `does not take <kind>` |
| `routes` (tier) | `routes` | `no route in <a,b>` |
| `owner` | the consumer's name | `owned by <name>` |
| friend-only (rules 2 and 3) | the consumer's kind | `is a bench, and this wants a friend` |
| — | `present` | `not present` |

Ties go to the warm mirror, then the owner, then the **emptiest lane**, then the
name — so the answer is the same on every machine and the wall gets shorter for
nothing.

**The consumers table is not a new file.** A bench's capabilities are
`key=value` tokens in its row's notes in the machines registry
(`legs=`, `locality=`, `wall=`, `iso=`, `kinds=`, `routes=`, `width=`,
`mirror=`), beside the `allow-shared=` and `certified=` tokens already there; an
unknown token is ignored, a malformed value for a known one is refused by name.
The friends are the bus roster (`participants.json`), each a consumer of width
four.

## Refill

A consumer's queue is topped to its **width** the moment it drops below the low
water mark of **two**, from the sprint's open list, **ownership first then age**.
A friend's width is four; a bench's is its own number. Read debt — an open pull
request with no typed line — is a task owned by exactly one friend, never
unassigned.

This is the **dealer's rule** (`internal/deal`), the one mechanism a bench's
card dealer and a friend's refill both call. A friend is a consumer like a
bench: same task shape, same queue, same lease, same heartbeat, same refill.

## DONE

A task's state flips to closed **only from a primary record**:

- a pull request merged (evidence: the merge commit),
- an issue closed,
- a friend's typed `DISPOSITION` line **at the current head** (a carried line is
  evidence about a commit that is no longer there; a line by a login outside the
  friends list is not a friend read),
- a card landed on `ev:cards` (#2587), behind the `Cards` interface.

`actual_minutes` is measured from `leased_at` to the record's time. A closed
task with no lease has **no actual**, and the calibration counts it as missing
rather than as a zero that pulls the average down.

## Calibration

`sprint calibration` prints the error distribution by kind and by owner over
closed tasks, and the estimate each kind's measurement argues for beside today's
default (read 10, card 30, fix 120, decision 30, repair 90, evaluation 120
minutes). The defaults move when a person moves them, with the numbers on the
line — do not guess, measure.

## Exit codes

`0` the verb ran; `1` the check ran and said no (a task nothing could route);
`2` the invocation was unusable, one line naming what was wrong and the remedy.
