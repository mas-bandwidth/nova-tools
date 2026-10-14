# 60 State machines to model in TLA+

Index: [00-index.md](00-index.md). Filled from 10 to 50 once they are written.

## Owner

The coordinator (she/her), on Claude Code.

## Scope

In:

- Every state machine that layers 10 to 50 define, modelled before it is built.

Out (later releases):

- Machines whose layer moves out of v1.3.

## Guarantees

1. Every state machine named in 10 to 50 has a TLA+ module, with its invariants and liveness taken
   from that layer's guarantees.
2. Every module is checked with TLC on a small instance, on Linux benches only.
3. Every module has reversed witnesses: configurations that must fail, one for each failure the
   layer names.
4. Every counterexample is checked against the spec by hand before it is written down.

## State and transitions

For each machine: the variables (the state it owns), the actions (operations and outside events),
the invariants, and the liveness properties. Taken from the layer that owns it.

## Failure modes

1. A model that differs from the spec. Detected by the hand check of each counterexample, and
   recovered by fixing whichever one is wrong, through its owner.

## Interfaces

- Below: the state and transitions sections of 10 to 50.
- Above: the work. Each change cites the model it implements.

## Open questions

1. Which existing modules (presence, hook, comms, slot deal, one store) are extended, and which are
   replaced.

## The likely machines

**Provisional.** This list is a first guess, to be replaced by what 10 to 50 actually define.

| Machine | States and events | Layers |
|---|---|---|
| Hook / session | armed, challenged, proven, live, expired | 20, 30 |
| Push delivery and ack | sent, delivered, observed, acked; re-offered until acked; replayed on attach | 20, 30 |
| Presence and lease | up while proven and unexpired; down with a reason; lease renewed and lapsed | 10, 20 |
| Friend lane | idle, taken, working, finished, reported, bounced | 40 |
| Card delivery, from the friend's view | dealt, taken, working, finished, read, merged | 40, 50 |
| Verdict and merge gate | a verdict bound to head and base; refused when behind or unanchored; carried across a clean base merge | 50 |

Reversed witnesses already suggested by what has been observed:

- A proven hook decays to "live" with nothing on the stream, and a send is refused.
- A presence reader counts a ping to its own stream as a friend being up.
- A verdict names files that are not in the PR's diff, and it reaches merge.
- A daemon-only harness holds a challenge unread until its window closes.
