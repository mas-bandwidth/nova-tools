# nova-logs — one open-source logging stack so every mechanical part can be read after the fact (draft 1, 2026-09-17)

Glenn, 2026-09-17: *"Is there something we can use to get better logging, better awareness of
what mechanical parts of the self are doing? If you could inspect things quickly from the logs,
you could answer questions faster. These parts would leave more information, so you can inspect
that, verify they did what you expect, read and see what is going on, why are they hung, read
when they fail and see why. Consider this across yourself, across the managers, across the tools,
across the fleet, across the work language. The ability to introspect is key. Logging gives this
to you. Try not to build your own logging. Look for an open source best-work-practices logging
solution you can use."*

Today every mechanical part writes its own file — the timers `~/hygiene.log` and `~/mirror.log`
per bench, the loops a session scratch dir, each card `harness-output.log` and `usage.tsv` — and
reading any of it is `ssh` and `grep` by hand. This spec chooses one stack for all of it. It
invents nothing: the lines go out through Go's own `log/slog` and the systemd journal, and the
reading, shipping and alerting are open-source programs. Related: [SPEC.md](SPEC.md) (an event is
exactly one line), [../internal/oneline](../internal/oneline/oneline.go) (the one escape),
[SPEC-STATE.md](SPEC-STATE.md) (Postgres is the durable record; **logs are not the record**),
[SPEC-SECRETS.md](SPEC-SECRETS.md), [SPEC-BUS-DELIVERY.md](SPEC-BUS-DELIVERY.md),
[SPEC-PULSE.md](SPEC-PULSE.md) (the loops), [SPEC-SWARM.md](SPEC-SWARM.md) (the harness),
[SPEC-MERGE.md](SPEC-MERGE.md) (the queue).

## Part 1 — the choice, and why

The choice is **Grafana Loki for storage, Grafana Alloy to ship, Grafana to read, LogQL to
ask, and Loki's own ruler for alerts, all on `space`.** The engineering reason is one sentence:
**a log line here is a machine event with known fields, not free prose to be full-text searched,
so index the labels and compress the rest** — which is exactly what Loki does and exactly what
the other two refuse to do.

| | Loki + Alloy | OpenSearch | ELK (Elasticsearch + Logstash + Kibana) |
|---|---|---|---|
| Cost on a 4-bench fleet | One binary on `space`, object storage optional; GB/day compressed 10–20x | JVM heap per node, shards to size; wants a cluster | Three JVMs (ES, Logstash, Kibana) before it is useful; heaviest |
| Index | **Labels** (`bench`, `verb`, `card`, `source`, `event`); the line is compressed, not indexed | Full-text inverted index over every field | Full-text inverted index over every field |
| Query | **LogQL**: label selectors + `\| json` + pattern, metric queries in the same language | Query DSL / Lucene, then PPL for metrics | KQL/Lucene in Kibana, separate for metrics |
| Alerting | Loki ruler, the **same LogQL**, one rule file | Alerting plugin or OpenSearch monitors | Watcher, licensed tier for the good parts |
| systemd journal | Alloy's `loki.source.journal` reads it natively | Filebeat/journald input then parsing | Filebeat `journald` input then parsing |
| OpenTelemetry later | Alloy is an OTel Collector distribution: `otelcol.receiver`, traces into Tempo, one agent | OTel via Data Prepper, second pipeline | ES OTLP endpoint, but a second stack beside it |

Full-text search is the wrong default for us and it is the cost: we do not want to pay to index
the body of a harness log, and we do not need to, because the fields we ask questions with are
fixed (Part 2) and the body is evidence we read rarely and can compress. Loki's label index is
small and cheap; its rules are the same language as its queries, so the alert in Part 4 is a
query a person already wrote by hand. Alloy replaces the hand-rolled shipper and is the OTel
path when traces arrive. Grafana is the reader because it is one pane over Loki today and Tempo,
Postgres and Prometheus later.

**What would change the choice.** (a) A query that must full-text search arbitrary body text as
the *common* case, not the rare one — then OpenSearch. (b) A fleet where a bench cannot reach
`space` and must hold and search its own store — then a per-bench OpenSearch or the existing
files. (c) A mandate to use an existing managed ELK — then ELK, and this spec's fields and
queries carry over unchanged. None of that is true today. Nothing here is invented: `slog`,
journald, Alloy, Loki and Grafana are the open-source best practice, wired together.

## Part 2 — what every part logs

**One shape for every part: one JSON object per state change, one line.** Go verbs write it
through `log/slog` with `slog.NewJSONHandler` to **stderr** and, on a bench, to the **systemd
journal** (a unit's stderr is the journal, so Alloy reads one source). The line is written
**beside** the one output line the human already reads on stdout, never instead of it: the
stdout line stays the SPEC.md event, unchanged, and the JSON line is the same event with the
fields a query needs. The fields are fixed, so `\| json` never guesses:

```json
{"ts":"2026-09-17T16:56:03.412Z","level":"INFO","source":"nova-pulse","bench":"space",
 "verb":"fill","job":"","card":"8973x","pr":0,"run":"","slot":"3","guid":"a1b2…",
 "event":"start","msg":"fill: cut one card","dur_ms":0,"err":""}
```

| field | meaning | absent as |
|---|---|---|
| `ts` | UTC RFC3339Nano, the writer's clock | never |
| `level` | `DEBUG`/`INFO`/`WARN`/`ERROR` | never |
| `source` | the tool: `nova-swarm`, `nova-pulse`, `hygiene`, `mirror`, … | never |
| `bench` | the machine, by its fleet name (`space`, `bench-1`) | `""` |
| `verb` | the verb within the tool (`launch`, `harvest`, `merge`) | `""` |
| `job` / `card` / `pr` / `run` / `slot` | the work item's ids, `0`/`""` when not this scope | `""`/`0` |
| `guid` | one per process run (the `/proc` boot id + pid + start), so a run's lines group | never |
| `event` | the state change: `start`, `refuse`, `retry`, `done`, plus the part's own nouns | never |
| `msg` | one human sentence, **escaped through `oneline`** so it cannot add a line | never |
| `dur_ms` | milliseconds from `start` to this event | `0` |
| `err` | the error's text through `oneline.Err`, else `""` | `""` |

The `start`/`refuse`/`retry`/`done` cycle is the spine: every part emits `start` when it takes
work, `refuse` when it declines with a reason in `msg`, `retry` when it tries again, and `done`
with `dur_ms` and `err` when it finishes. A hang is then just a `start` with no `done`.

- **Go verbs.** Every verb gets the slog handler once at `main`; the existing stdout line is
  untouched. Which verbs is not a choice to make per verb: **a verb that changes state logs the
  event**, and `end2end`/tests of the emitter are red if a state change prints only stdout.
- **The timers (`hygiene`, `mirror`).** One event per pass: `start`, then one event per action
  (`delete`/`keep` with the path and the rule that decided it; `fetch`/`skip` with the ref and
  the verdict), then `done` with `dur_ms`. The two timer logs Alloy reads become structured; the
  old `~/hygiene.log` stays as the fallback line.
- **The pull worker.** `start` with the stream and lane it reads, `claim` with the card and its
  attempt, `reclaim` with the lapsed lease it took, `clip`/`ack` on a landed card, `refuse` when
  a card is too big or the input limit bites, `retry` on a reclaim.
- **The harness run.** `start` with the model, the provider and the card; one `tool_call` event
  per call **folded from `timeline.tsv`** (its six columns become the fields); then
  `deadline` when the card's budget ends, `abstain` when it declines, and `done`/`fail` with the
  result and the token counts. `harness-output.log` and `harness.log` stay the pinned evidence;
  the events point at them, they do not replace them.
- **The merge queue.** `enqueue` with the PR and head sha, `group_start` with the group name and
  the run id, `group_verdict` with the conclusion, the failing test and the poison verdict, and
  `park` when a PR is set aside with the reason and the age.
- **The coordinator's loops.** One event per action, not one per turn: `harvest` per RESULT.md
  disposed, `sweep` per pool folded into the ledger, `fill` per card cut or refused (with the
  source and the dedup that stopped it). The turn's `WIDTH`/`MANAGER` line stays as today's one
  human line.
- **The work language's expander.** One `derive` event per node derived from the graph (the
  node id, the parent, the rule), one `cut` event per card cut (the card id, the pool candidate,
  the template and the route). The journal stays the replayable truth; these events are how a
  person watches the expansion without replaying it.
- **What must never be logged.** A **secret value** — never a `nova-secrets exec` plaintext, an
  age key, a token, a password, a private key; the field may name the secret, never its value.
  A **private bus body** — a note's body is prose with a scope and a set of readers; log the
  note id, the scope and the receipt, never the body. A **card path is fine** (it is already an
  event field). The leak is caught before the line leaves the process (Part 5), not in review.

## Part 3 — the questions we ask by hand today, and the LogQL for each

All queries assume the labels `source`, `bench`, `verb`, `event` are promoted by Alloy (the
other fields parse with `\| json`).

**Why is this card hung?** The last event the card wrote, and how long ago.
```logql
{card="8973x"} | json | ts > 1h ago
| line_format "{{.ts}} {{.source}} {{.event}} {{.msg}}"
```
The hung test is the card whose last event is `start` (or any non-`done`) and whose age is past
its budget: `{card="8973x"} | json | event!="done" | last_over_time(...)`.

**What did hygiene delete in the last hour, and why?**
```logql
{source="hygiene", bench="space"} | json | event="delete"
| msg=~"" | line_format "{{.ts}} {{.msg}}"
```
(`event="delete"` and the rule is in `msg`; add `| keep` for what it spared.)

**How wide are we now?** Cards that started and have not finished in the window:
```logql
sum(count_over_time({source="nova-swarm", event="start"}[5m]))
- sum(count_over_time({source="nova-swarm", event="done"}[5m]))
```

**Which groups failed on which test since noon?**
```logql
{source="nova-merge", event="group_verdict", verdict="fail"} | json
| ts >= "2026-09-17T12:00:00Z"
| line_format "{{.ts}} pr={{.pr}} group={{.group}} test={{.failing_test}}"
```

**What did the fill loop launch and refuse?**
```logql
{source="nova-pulse", verb="fill"} | json
| line_format "{{.ts}} {{.event}} {{.card}} {{.msg}}"
```

**Which bench refused a launch, and why?**
```logql
{source="nova-pulse", event="refuse", verb="launch"} | json
| line_format "{{.bench}}: {{.msg}}"
```

**What did a tool do between two timestamps?**
```logql
{source="nova-merge"} | json
| ts >= "2026-09-17T15:00:00Z" and ts <= "2026-09-17T15:30:00Z"
| line_format "{{.ts}} {{.event}} {{.card}} {{.msg}} dur={{.dur_ms}}ms"
```

## Part 4 — alerts (Loki ruler rules, the same LogQL)

- **A bench under 25 GB free** — from the survey's `disk_free_gb` event, per bench:
  `min by (bench) (disk_free_gb) < 25`.
- **A loop with no event for 15 minutes** — `absent_over_time({source=~"nova-(pulse|swarm)"}[15m])`,
  for each loop that must beat.
- **A job past its deadline** — a `start` older than its `budget` with no `done`:
  `{event="start"} | json | dur_ms==0` joined against the card's budget, or the simpler
  `count_over_time({event="start", card!=""}[1h])` minus `done` on the same card.
- **A group failure twice on one PR** —
  `sum by (pr) (count_over_time({source="nova-merge", event="group_verdict", verdict="fail"}[24h])) > 1`.

## Part 5 — the first slice on `space`, its measure, and the red tests

**The slice, on one bench (`space`).** Loki with local disk storage, one Alloy reading the
systemd journal (so every verb and every service is already a source), plus the two timer logs
(`hygiene`, `mirror`) and the fill loop's log. Grafana pointed at Loki, with **one dashboard**:
fleet width, queue depth, cards per hour, minutes per card, and free disk per bench. Nothing else
changes; the files stay the record, Postgres stays the durable record, and the JSON lines are the
new second copy. Old readers (`ssh` and `grep`) still work, so the slice is additive.

**The measure.** **Seconds to answer "why is card X hung"** — today an `ssh`, a `find` across
benches, and a `grep` of several scratch dirs; the target is the Part 3 query, one pane, under
five seconds. The slice is done when that number is printed, not when Loki is installed.

**The red tests** (one per promise, seen red first):

- `a-slog-line-from-a-verb-appears-in-loki-within-five-seconds-with-its-labels` — run a verb
  that changes state; within five seconds `{source, verb, card}` returns the JSON line in Loki.
- `a-secret-value-in-a-message-is-refused-by-the-test-hook-before-it-leaves-the-process` — put a
  known secret value in a `msg`; the emitter's hook refuses it and no line reaches the journal.
  The hook lives beside the handler, so the refusal is a test of the emitter and not of review.
- `the-hung-card-query-returns-the-card-s-last-event` — a card that `start`s and never `done`s;
  the Part 3 hung query returns that card's last event and its age.
