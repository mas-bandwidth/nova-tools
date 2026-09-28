# SPEC-LOGS — the structured event line

A part that changes state writes one structured event line **beside** the one human line it
already writes, never instead of it. The primitive is `internal/log`: Go's own `log/slog`
with `slog.NewJSONHandler`, the fixed field list below, the one-line escape of
`internal/oneline`, and a redaction pass that keeps secret values out of every line. Nothing
here is a second logging system. Related: [SPEC.md](SPEC.md) (an event is exactly one
line), [SPEC-SECRETS.md](SPEC-SECRETS.md) (the secrets a line must never carry).

## Part 1 — the primitive

`internal/log` holds three things:

- **`Line`** — one event as a value. `New(clock, guid, source)` fills `ts` from the injected
  clock, `guid` from the injected source, `source` from the caller, and `level` as `INFO`;
  the caller fills the rest and `Write(w)` renders it.
- **`LongVerb`** — the sink a long-running verb holds for its whole run: the writer, the
  tool (`source`), the host (the `bench` field) and the verb's own name, fixed from the
  first event to the last. `Event(label, msg)` writes one line through `Line.Write`. A nil
  writer writes nothing, so a caller that names no sink keeps its exact stdout and stderr,
  and a line that cannot be written never stops the work.
- **`Redact`** — the redaction pass `Write` runs on every field whose content comes from
  outside the program (Part 2).

The clock and the guid are always injected: a test passes fixed ones and never reads
`time.Now` or `/proc`. The production guid is `ProcessGUID`: the kernel's boot id, the pid
and the process start time, joined as `<boot>-<pid>-<start>`. **The guid is a run's identity**:
a run's lines share it, and with both halves read from `/proc` two runs that share a pid after a
reboot still differ. Where `/proc` is absent, `noboot` and `nostart` stand in and the guid is
only the pid between them, so on such a host it names no run uniquely and nothing here claims it
does. The writer is
injected too: stderr in production (a unit's stderr is the systemd journal), a buffer or a
file in a test.

## Part 2 — the line

**One JSON object per state change, one line.** The stdout line stays the SPEC.md event,
unchanged; the JSON line is the same event with the fields a query needs. Every object
carries all fifteen fields, so a reader never guesses. An example line (the values are
illustrative):

```json
{"level":"INFO","msg":"sweep: pass ends","ts":"2026-09-17T16:56:03.412Z","source":"example-tool",
 "bench":"bench-1","verb":"sweep","job":"","card":"","pr":0,"run":"","slot":"","guid":"a1b2…",
 "event":"done","dur_ms":1840,"err":""}
```

| field | meaning | absent as |
|---|---|---|
| `ts` | UTC RFC3339Nano, from the injected clock | never |
| `level` | `DEBUG`/`INFO`/`WARN`/`ERROR`; `INFO` by default and for any other word, `WARNING` is `WARN` | never |
| `source` | the tool that wrote the event | never |
| `bench` | the machine, by its fleet name | `""` |
| `verb` | the verb within the tool | `""` |
| `job` / `card` / `pr` / `run` / `slot` | the work item's ids | `""`, and `0` for `pr` |
| `guid` | the run's identity (see `ProcessGUID` above for when it is unique) | never |
| `event` | the state change: `start`, `refuse`, `retry`, `done`, or the part's own noun | never |
| `msg` | one human sentence, **escaped through `oneline.Field`** so it cannot add a line | never |
| `dur_ms` | milliseconds from `start` to this event | `0` |
| `err` | the error's text, **escaped through `oneline.Escape`** | `""` |

An id that is not this event's scope is written as `""` (or `0` for `pr`), never omitted, so a
query on `card=""` means exactly "not this scope". The `start`/`refuse`/`retry`/`done` cycle is
the spine: `start` when a part takes work, `refuse` with the reason in `msg`, `retry` when it
tries again, `done` with `dur_ms` and `err` when it finishes. A hang is a `start` with no `done`.

**What must never be logged.** A **secret value**: a `nova-secrets exec` plaintext, an age key,
a token, a password, a private key. A field may name the secret, never its value. The rule is
enforced inside the emitter, before the line leaves the process: `Write` passes `msg`, `err`,
`bench`, `job`, `card`, `run` and `slot` through `Redact`, which replaces each secret-shaped
value with the fixed mark `[redacted]` and keeps the line. The shapes, from the specific to the
general:

1. a PEM private key block;
2. a provider-stamped key (`sk-`, `sk_`, `ghp_`/`gho_`/`ghu_`/`ghs_`/`ghr_`, `github_pat_`,
   `glpat-`, `xox?-`, `AKIA`/`ASIA`, `AGE-SECRET-KEY-1`, `dckr_pat_`, `npm_`);
3. an `Authorization` credential (`bearer`, `basic`, `token` and what follows);
4. the value of a keyed assignment whose key names a credential (`api_key=`, `token:`,
   `password=`, …) — the key stays, the value goes;
5. a run of 32 or more characters of the credential alphabet with at least three upper-case
   letters, three lower-case letters and three digits.

The last rule is narrow on purpose: a 40-character lowercase-hex sha, a card id, a PR number and
an ordinary sentence survive it. The fixed vocabulary the program writes itself — `ts`, `level`,
`source`, `verb`, `event`, `guid` — is never redacted, so a redaction can never rename an event
out from under a query. A private bus note's body is never an event field: log the note id, the
scope and the receipt.

## Part 3 — a long-running verb

A verb that does one unit of work and exits builds a `Line`, writes it, and is gone. A verb that
changes state many times over one run holds a `LongVerb`: the run's `source`, `bench` and `verb`
ride on every line as labels, and each `Event` carries the same fifteen fields, escaped and
redacted the same way as a `Line`. Those labels are shared by every run of that verb on that
host, repeated or concurrent, and `LongVerb` enforces no exclusion between runs; one run's lines
are the ones carrying its `guid`.

## Tests this spec demands

The tests run through injected clocks and guids against `bytes.Buffer` sinks and `t.TempDir()`
files: no network, no real `/proc`.

1. `TestLineCarriesTheSpecFieldsAndNoMore` — one JSON object, one line, and its keys are the
   fifteen fields exactly (ts, level, source, bench, verb, job, card, pr, run, slot, guid, event,
   msg, dur_ms, err), none missing, none invented.
2. `TestLineWritesAbsentIdsAsEmptyNotOmitted` — an id that is not this event's scope is `""` (or
   `0` for pr) and the key is still written.
3. `TestLineEscapesMsgThroughOnelineField` — `msg` goes through `oneline.Field`, so a newline in
   the sentence cannot add a second line.
4. `TestLineEscapesErr` — `err` goes through `oneline`, the same one-line promise.
5. `TestLineCarriesTheCallersLevel` — the level the caller sets is the level the object carries,
   in upper case.
6. `TestRedactRemovesEverySecretShape` — every secret shape above is replaced with `[redacted]`.
7. `TestRedactLeavesTheFieldsWeQueryWithAlone` — a git sha, a card id, a PR number and an
   ordinary sentence survive redaction.
8. `TestWriteRedactsEveryVariableField` — the pass is inside `Write`, so a secret is caught
   whatever field carried it, and the line stays one line.
9. `TestWriteKeepsTheFixedVocabulary` — redaction never touches ts, level, source or event.
10. `TestLongVerbWritesJSONLines` — a run that changes state five times writes five JSON lines to
    a file, each carrying the run's host, verb and event label, parsed back from the file.
