# nova-fuse READ rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash (cold reader, reading only)
Build: 9607fcfef15b
Score: 7.5/10
README: 7/10

## Reasons

The README line: 7/10. The page is clear, one row per tool, the nova-fuse row states the job and the harness obligation, and the top answers "what do you want to do". It loses a point because it still opens its trial section on the previous release: README.md:50 says "These are the Nova Tools 1.0.0 commands" and README.md:55 pins the install at the older tag, while this tree ships 1.1.0 and carries its notes at docs/RELEASE-NOTES-1.1.0.md.

First confusion: README.md:50. A cold reader cannot tell whether the table describes the commands in this checkout or the previous release, and the install line names a tag the checkout has moved past.

First boredom: cmd/nova-fuse/main.go:38. The banner's exit-code paragraph (cmd/nova-fuse/main.go:63) and its status `--max` paragraph (cmd/nova-fuse/main.go:84) repeat what each verb's own help and docs/CLI.md:329 already say, and they stand between the three answers and the `example:` block at cmd/nova-fuse/main.go:96.

First doubt: docs/SPEC.md:2050. The normative grammar prints the gate as `FUSE FAIL`, and the same block prints `LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL` and `INIT FAIL`, while the binary prints `FAILED` (cmd/nova-fuse/main.go:518) and the executed transcript agrees with the binary (docs/CLI.md:316). The spec is the document a caller builds against, so the drift is the one that costs a caller a match.

The trust is real. One state file, one locator flag, no environment fallback; unreadable and absent both read as BLOWN (internal/fuse/fuse.go:203); the write is temp-file plus rename so a crash leaves the old box or the new one (internal/fuse/fuse.go:28); the gate is one verb with exit 0 as the only yes (cmd/nova-fuse/main.go:486); every write re-reads the box and says `verified`; `lift lockdown` refuses before any flag or file (cmd/nova-fuse/main.go:331); the double-blown blind spot is named rather than hidden (docs/SPEC.md:2304); and cmd/nova-fuse/firstrun_test.go runs the documents' transcripts line for line. This is a tool I would trust to gate a read path, and code I would enjoy reading once its package doc is cut.

A ten would need: the spec's grammar samples equal to the bytes the binary writes; the README on the shipped version; the banner cut to its three answers, the verb table and the example; the verb list written once and pointed at; the package doc's rules in present-tense prose rather than shouted headings; and the family shape either adopted or its exception argued where the standard can see it.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:2050 | the normative grammar prints `FUSE FAIL`, `LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL` and `INIT FAIL`; the binary prints `FUSE FAILED` (cmd/nova-fuse/main.go:518) and the transcript matches the binary (docs/CLI.md:316), so a caller matching the spec's token misses every refusal | print the spec's samples as the bytes the binary writes, `FAILED` throughout | S |
| 2 | docs/SPEC.md:2192 | the spec says status lists quarantines "in the box's own order"; the binary sorts them (internal/fuse/fuse.go:190, cmd/nova-fuse/main.go:478), and the transcript order is the sorted one | say the listing is sorted, which is what makes two runs print the same bytes | S |
| 3 | README.md:50 | the trial section says "These are the Nova Tools 1.0.0 commands" and README.md:55 installs the older tag, while the tree ships 1.1.0 with notes at docs/RELEASE-NOTES-1.1.0.md | move the sentence and the install example to the shipped version | S |
| 4 | cmd/nova-fuse/main.go:38 | the banner is about seventy lines; exit codes, the `-h` rule, the box JSON sample and the `--max` explanation stand before the `example:` block at cmd/nova-fuse/main.go:96, so a reader's first run is buried under prose the verb help repeats | keep the three answers, the verb table and the example; move the rest to `help <verb>` and docs/CLI.md | M |
| 5 | internal/fuse/fuse.go:1 | the package doc opens on sixty lines of shouted numbered headings ("THE READ HAS ONE YES AND TWO NOES", "MALFORMED IS UNREADABLE"), so a reader meets capitals before a sentence | state each rule once, present tense, in ordinary prose; the design essay belongs in a design note | M |
| 6 | cmd/nova-fuse/main.go:146 | the verb list stands in at least four places: the `usage` constant (cmd/nova-fuse/main.go:48), the `verbs` slice, the `fuseHelps` map (cmd/nova-fuse/help.go:28) and the unknown-verb sentence (cmd/nova-fuse/help.go:108), so a new verb can be added in one and missed in another | one list feeds the dispatcher, the help table and every refusal | M |
| 7 | docs/SPEC.md:2069 | the tool has no `--json` and never calls the shared skeleton, while docs/STANDARD.md tells every tool to render one value as lines or JSON; the spec argues the exception in one sentence, but the family contract cannot see the argument | build the one value and encode it on request, or cite the exception beside the standard's rule | M |
| 8 | cmd/nova-fuse/main.go:517 | a quarantine behind a blown lockdown is invisible through the gate: both states exit 1 on the lockdown line, named as a limit at docs/SPEC.md:2304 | on a lockdown failure print the quarantine count and the status command, so the hidden fuse re-emerges on the line | S |

## Good, keep
- The refusal grammar and its remedies: one line, every independent problem at once, a command that runs, and the box path quoted so a dash or a control byte stays data (cmd/nova-fuse/main.go:140, cmd/nova-fuse/lift_remedy.go:12).
- The read discipline: absent and unreadable are both CANNOT TELL treated as BLOWN, told apart only so the remedy is right (internal/fuse/fuse.go:193), and every write is verified by re-reading (cmd/nova-fuse/main.go:603).
- cmd/nova-fuse/firstrun_test.go runs the docs/CLI.md and docs/TESTS.md transcripts line for line, so the document is the contract and cannot drift silently.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| its own output dialect | CHANGED | the typed one-line grammar is deliberate and consistent, but the spec still prints `FUSE FAIL` where the binary writes `FUSE FAILED` (docs/SPEC.md:2050, cmd/nova-fuse/main.go:518), and no `--json` renders the same value |
| shouted and historical comments | CHANGED | no dated or ticketed history remains in the non-test source; the shouting is still there in the package doc's capitals (internal/fuse/fuse.go:1) |
| three copies of its verb list | STILL THERE | the list stands in the `usage` constant (cmd/nova-fuse/main.go:48), the `verbs` slice (cmd/nova-fuse/main.go:146), the help map (cmd/nova-fuse/help.go:28) and the unknown-verb sentence (cmd/nova-fuse/help.go:108) |
| permissive box decoding | FIXED | internal/fuse/fuse.go:214 fails closed on malformed JSON, the state is documented at internal/fuse/fuse.go:23, and the caller answers exit 2 at cmd/nova-fuse/main.go:510 |
| lost concurrent updates | CHANGED | internal/fuse/fuse.go:28 now names the tradeoff: temp-file plus rename means two writers lose one write but neither can leave a torn box, so it is a stated limit rather than an unstated defect |
| README 6.5 to 8.4 | CHANGED | the README is clear and one-row-per-tool, but README.md:50 still says the trial commands are 1.0.0 while the tree ships 1.1.0 |