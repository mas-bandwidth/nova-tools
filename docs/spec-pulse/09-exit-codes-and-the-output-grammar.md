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
HANDOFF OK to=<name> inflight=<n> pending=<n> escalations=<n>
HANDOFF REFUSED: <reason> (<remedy>)
TAKEOVER OK from=<name> inherited=<inflight/pending/escalations>
TAKEOVER REFUSED owner=<name> pid=<n> host=<h> (wait, or clear the stale lock)
PROGRESS cards=<n> rc0=<n> wall_p50_s=<n> wall_p90_s=<n> usd_per_card=<x.xxxx> span_h=<n> effective_parallelism=<n.n> cards_per_hour=<n>
ESTIMATE remaining_cards=<n> hours=<n>
<TOKEN> MORE kind=<k> shown=<n> total=<t> <remedy>
<TOKEN> NOTE <something true about this run that is not a finding>
GATEFACTS OK ci-ok=success merge-tree=clean files=- paths=ok extra=- base=<sha12> head=<sha12>
GATEFACTS FAIL ci-ok=failure|pending job=<name> test=<name> merge-tree=clean|conflict files=<list|-> paths=ok|extra|- extra=<list|->
GATEFACTS REFUSED: <reason> (<remedy>)
```

`gate-facts` is the G1/G2/G3 stamp: `ci-ok` at the head, `merge-tree` against the
landing base, and card PATHS versus `git diff --name-only base...head`. One line
to stdout and optionally `--receipt-file`. A conflicting merge-tree prints
`merge-tree=conflict` and exits 2; a clean tree prints `merge-tree=clean` and
exits 0 when `ci-ok` is success and the diff stays inside PATHS. `--rollup` is
the check rollup as a file; unit tests never call GitHub.

`POOL`, `CUT`, `ADMIT`, `PULSE`, `HARVEST`, `HANDOFF` and `TAKEOVER` are the first tokens;
`OK`, `REFUSED`, `WIDTH`, `UNDER-WIDTH` and `POOL EMPTY` the verdicts and the **last** line of
a verb — except `ADMIT REFUSED`, which is an event line `launch` prints per gated card and
never its last; `harvest`'s last line is the `PULSE` line of the pulse it launched, or
`PULSE POOL EMPTY`. `OK` and `WIDTH` lines go to stdout, `REFUSED` and `UNDER-WIDTH` to
stderr. Every line is one line; a count stands where a list would be; every refusal carries
one remedy in parentheses.
