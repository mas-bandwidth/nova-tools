## Exit codes and the output grammar

Per SPEC.md: **0** the verb ran and the state it reports is consistent — a pool written, cards
cut, a batch admitted, a harvest folded, a width that is not under; **1** the tool saying NO —
a harvest with `mismatch > 0` or `abstain > 0`, a cut with `skipped > 0`; **2** could not run
— every refusal the rules name and `UNDER-WIDTH` (rule 16: the alarm is a state the
coordinator must act on, and it exits like a refusal so a wake fires on it). An
`ADMIT REFUSED` line is an event, not a verdict: it leaves the exit code alone (rule 9).

```
POOL OK sources=<n> candidates=<n> issues=<n> audits=<n> slices=<n> roadmap=<n> prs=<n> next=<n> plan=<n> seen=<n> took=<d> out=<path>
POOL REFUSED source=<kind>:<locator>: <reason> (<remedy>)
CUT OK cards=<n> skipped=<n> zero=<n> flat=<n> metered=<n> out=<dir>
CUT ROUTE route=<model> reason=<class>
CUT SKIPPED source=<kind> id=<id> template=<name>: no template
CUT REFUSED template=<name>: <which rule> (<remedy>)
PULSE OK id=<id> n=<n> free-before=<n> queued=<n> batches=<n> deadline=<s>
ADMIT REFUSED card=<label> gate=<spend|attempts|scope> <value> (<remedy>)
PULSE REFUSED: <reason> (<remedy>)
HARVEST PR repo=<owner/name> pr=<n> label=<label> branch=<name>
HARVEST RETRY label=<label> card=<path>: <last permission or refusal line, escaped>
HARVEST OK id=<id> done=<n> pushed=<n> prs=<n> abstain=<n> mismatch=<n> retry=<n> usd=<sum|-> took=<d>
HARVEST REFUSED id=<id>: <reason> (<remedy>)
PULSE WIDTH in-flight=<n> free=<n> pool=<n> queued=<n> headroom=<n> hours=<n>
PULSE UNDER-WIDTH pool=<n> free=<n>: launch
PULSE POOL EMPTY in-flight=<n>
coordinator harvest tick=<n> msg=<quoted>
coordinator sweep tick=<n> msg=<quoted>
coordinator fill tick=<n> source=<repo>#<n> msg=<quoted>
HANDOFF OK to=<name> inflight=<n> pending=<n> escalations=<n>
HANDOFF REFUSED: <reason> (<remedy>)
TAKEOVER OK from=<name> inherited=<inflight/pending/escalations>
TAKEOVER REFUSED owner=<name> pid=<n> host=<h> (wait, or clear the stale lock)
PROGRESS cards=<n> rc0=<n> wall_p50_s=<n> wall_p90_s=<n> usd_per_card=<x.xxxx> span_h=<n> effective_parallelism=<n.n> cards_per_hour=<n>
ESTIMATE remaining_cards=<n> hours=<n>
<TOKEN> MORE kind=<k> shown=<n> total=<t> <remedy>
<TOKEN> NOTE <something true about this run that is not a finding>
```

`POOL`, `CUT`, `ADMIT`, `PULSE`, `HARVEST`, `HANDOFF` and `TAKEOVER` are the first tokens;
`OK`, `REFUSED`, `WIDTH`, `UNDER-WIDTH` and `POOL EMPTY` the verdicts and the **last** line of
a verb — except `ADMIT REFUSED`, which is an event line `launch` prints per gated card and
never its last; `harvest`'s last line is the `PULSE` line of the pulse it launched, or
`PULSE POOL EMPTY`. `OK` and `WIDTH` lines go to stdout, `REFUSED` and `UNDER-WIDTH` to
stderr. Every line is one line; a count stands where a list would be; every refusal carries
one remedy in parentheses.

`coordinator harvest`, `coordinator sweep` and `coordinator fill` are the coordinator loop's
event lines (#2186): `nova-pulse run` prints them to stdout during a tick, in step order —
harvest, then sweep, then fill — and always before that tick's `PULSE WIDTH tick=<n> ...`
line. Like `ADMIT REFUSED` they are events, never a verb's last line, and they leave the exit
code alone. `tick=` is the tick they belong to; `msg=` is Go-quoted (`%q`), so a path or a
title never breaks the line. Their counts are the WIDTH line's:
- `coordinator harvest` — one line per RESULT.md the tick's harvest disposed, summed over
  every bench root's `HARVEST OK|INCOMPLETE done=<n>`, so the lines on a tick equal its
  `harvested=`; `msg` is `"disposed RESULT.md in <root>"`. A root with no `cards.tsv`, or a
  harvest with `done=0`, prints none.
- `coordinator sweep` — at most one line per tick, printed only when the sweep moved
  something (`swept=` = enqueued + stale + closed > 0); `msg` is
  `"folded pool <owner/repo> enqueued=<n> stale=<n> closed=<n>"`.
- `coordinator fill` — one line per WORKSET candidate the refill considered (an open,
  non-draft PR owed a read; an open issue no open PR claims, owed a fix): `source=` is
  `<repo>#<n>`, and `msg` is `"cut"` (a card was cut; these lines equal `refilled=`),
  `"refused"` (`cut --kind` refused it) or `"dedup: <mark> already has a card"` (a card for
  that PR head or issue is already pending, launched, or for a read, done). A refill that
  is off its cadence, at its floor, or has an empty WORKSET considers nothing and prints
  none.
