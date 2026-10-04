# nova-cairn USE rating, nova-tools 1.1.0

Rater: qwen3.8-flash
Build: c28448a54d56
Score: 9.5/10

## Reasons

Judged from the binary's help alone, in a scratch store, nothing real touched. The first run is the help's own four examples in one sitting and every line came out as stated: open makes the store, append keeps the exact words, index lists sessions and entries with counts, receipt returns the text quoted byte for byte. A second, different job ran end to end: open with a --source, append from a file and from stdin, a repeat append answered duplicate=true at exit 0, a same-id-different-words append refused at exit 1 and named the exact receipt command that reads the holder, a re-open with the recorded policy said the record stands and a re-open with another policy named the matching one. All four refusal classes (missing required flag, unknown flag, unknown verb, bad value) named the problem, printed every problem at once, and gave the next command to run; the "no such session" refusal even embedded the full open command to run. --json is one parseable object on every verb including refusals, with result, why and a remedy carrying a runnable command; --dry-run writes nothing (the refused store dir stayed absent and persisted=false). --max truncates with an INDEX MORE line that says how to raise it. Multi-line text with non-ascii came back exact (bytes=36). What a 10 still needs: the flat-record entry layout the help invites is not shown anywhere; --json after a stray positional silently loses its effect; and a NOTE line in the usage block reads like a verb.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn index --store ./flat` | after hand-writing a flat record flat/s9.md with entry heading `## e1`, the index says sessions=1 entries=0 and receipt refuses the entry: the help points at flat records `<store>/<session>.md` but never shows the entry header the tool reads, which append itself writes as `## <rfc3339> — <entry>` | state the flat entry grammar in `append -h`, or make index report headings it did not read as entries | M |
| 2 | `nova-cairn open --store ./cairns --session z2 --publish manual --bogus 1 --json` | flag parsing stops at the stray positional `1`, so the trailing --json is never seen and the refusal prints as plain prose; help says every verb takes --json but no ordering rule is given | collect --json regardless of position, or print refusals as JSON once --json appears anywhere in the line | S |
| 3 | `nova-cairn help` | the usage block carries a line beginning `nova-cairn NOTE:`, so a reader enumerating verbs from the usage can mistake NOTE for one (it is not: `nova-cairn NOTE` is refused as an unknown verb) | keep the note but drop the `nova-cairn ` prefix so only real verbs start a usage line | S |

## Good, keep

- Refusal lines that name every problem at once and end with the exact next command to run, including the embedded full open command for an unopened session.
- One JSON object on every verb and every status, with result/why/remedy, so failures are actionable without parsing prose; --dry-run writes nothing and says so with persisted=false.
- Exact-text durability: same words answer duplicate=true exit 0, other words refuse exit 1 with the remedy; receipt --text returns multi-line non-ascii words byte for byte.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| index cannot list sessions (USE 2026-10-01 and 2026-10-02) | FIXED | `nova-cairn index --store ./cairns` prints `INDEX OK sessions=2 entries=3` then one `INDEX ENTRY` line per entry, exit 0 |
| a re-open looks like a first open (USE 2026-10-02) | FIXED | `nova-cairn open --store ./cairns --session s2 --publish manual` exits 1 with OPEN FAILED naming the recorded `publish=deferred source=repo:docs/STANDARD.md` and the matching open to run; re-opening with the same policy exits 0 |
| durable exact-text retrieval, duplicates, the conflict remedy and JSON all worked (USE 2026-10-02) | STILL THERE | `duplicate=true` at exit 0 on a repeat append; conflict exits 1 with a runnable receipt remedy; `--json` gives one object with status, why and remedy on ok, failed and refused alike |
