# nova-bus READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: bc60d1f260ea
Score: 8/10
README: 8/10

## Reasons
Read cold as writing and as an architectural design: README top to bottom, docs/CLI.md, docs/SPEC-BUS.md, then cmd/nova-bus from main into supporting packages, help text read as text, nothing executed.

The first place a cold reader was confused was README.md:24, where the first command for nova-bus passes --full and references ./trial-bus, but the setup directions for creating and committing that repository do not appear until README.md:67. The first boredom was cmd/nova-bus/help.go:67, where the root help embeds a 4-line printf command constructing participants.json rather than pointing to a clean canned fixture or template command. The first doubt was docs/CLI.md:428, where the text promises that no rejected push ever reaches a person, but cmd/nova-bus/send.go:179 prints SEND FAILED and returns 1 when retries are exhausted.

The help surface is the strongest attribute of the tool. The banner answers the three core questions in sequence (cmd/nova-bus/help.go:6-14), states the exit table directly in the banner (cmd/nova-bus/help.go:16-19), provides each verb with an explicit effect line covering inspection to delivery (cmd/nova-bus/help.go:77-89), and the setup instructions can run in an isolated scratch directory. The refusal grammar is uniform and every refusal provides a concrete remedy. The command reference presents measured data for retry counts (docs/CLI.md:527), and the cursor documentation states its trade-offs plainly (docs/CLI.md:648). The entry point clearly dispatches verbs (cmd/nova-bus/main.go:96-125), nouns are transparent (lane, note, receipt, cursor, roster), and package documentation explains the rationale in the present tense (cmd/nova-bus/main.go:1-45). Tests thoroughly verify status grammar, first-run sequences, and single-turn refusals.

What holds the tool back from a 10 is concentrated in the read path implementation and interface completeness. The primary read path remains a single 466-line function (cmd/nova-bus/inbox.go:285) handling participant resolution, cursor validation, history cuts, bounded walks, body paging, and output formatting. No verb supports --json, departing from the standard family convention (docs/SPEC.md:138), requiring programmatic consumers to parse human-readable text. Cursor advance helpers take thirteen and fourteen positional parameters (cmd/nova-bus/inbox.go:838, cmd/nova-bus/inbox.go:911) with adjacent booleans across multiple call sites. Furthermore, docs/SPEC-BUS.md:7 marks the wake reading mode as unimplemented despite code shipping in cmd/nova-bus/wait.go:113, while help synopses omit the mode entirely (cmd/nova-bus/help.go:38). Finally, REFUSED prints at exit 1 on cursor errors (cmd/nova-bus/inbox.go:326), three retired flags remain declared (cmd/nova-bus/wait.go:112), and docs/CLI.md:428 overstates push guarantees beyond what bounded retries can deliver.

A 10 would need: modularizing inboxListing into single-screen functions; implementing --json across all verbs from canonical result structs; passing structured options to cursor advance functions; aligning docs/SPEC-BUS.md and help synopses with the shipped wait --on-note mode; and restricting REFUSED strictly to exit 2 invocations.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-bus/inbox.go:285 | a reader tracing one inbox run walks a 466-line function that opens the bus, validates participants, verifies cursors, draws the legacy line, bounds the walk, pages bodies, and prints frames — one body with many concerns across the read path every call takes | split it along the three distinct parts — scope and cursor validation, the bounded walk, and output frame rendering — into named helper functions so each fits in one screen | L |
| 2 | cmd/nova-bus/help.go:21 | no verb takes --json: the synopses carry no such flag on any verb, while the family contract says every reading verb does (docs/SPEC.md:138), so an automated caller that reads objects from the other tools must parse plain text lines, and the one JSON it prints (prepare) is an artifact file, not a rendering of the listing | build each verb's one result value and render it as lines or as JSON from the same value, with bodies carried as their own bounded field | L |
| 3 | cmd/nova-bus/inbox.go:838 | two helpers take fourteen and thirteen positional parameters, noPush and noBeat adjacent booleans among them, called positionally across five sites (cmd/nova-bus/inbox.go:166, cmd/nova-bus/inbox.go:168, cmd/nova-bus/inbox.go:917, cmd/nova-bus/wait.go:771, cmd/nova-bus/wait.go:778), where one swapped pair compiles and silently discards or commits a file | pass an options struct similar to inboxOpts rather than thirteen or fourteen positional arguments | M |
| 4 | docs/SPEC-BUS.md:7 | the spec status line says the wake verb group is specified with no code, yet the wake read half ships: the flag is declared (cmd/nova-bus/wait.go:113), its refusals print (cmd/nova-bus/wait.go:155), and its wake line prints (cmd/nova-bus/wait.go:828); the spec is normative, so spec and code disagree | restate the status: the wake read half is implemented and pinned by tests; the verdict receipt stays absent | M |
| 5 | cmd/nova-bus/help.go:38 | the shipped wait --on-note mode is named in the flag list of wait -h but absent from the banner's wait synopsis, from the synopsis at docs/SPEC.md:2871, and from the wait section of the command reference (docs/CLI.md:598), so a reader who never opens the flag list cannot learn it exists | add --on-note to both synopsis lines and document it beside the other wait modes in the command reference | S |
| 6 | cmd/nova-bus/inbox.go:326 | the REFUSED word prints at exit 1 on five read-path refusals (cmd/nova-bus/inbox.go:326 to line 364) while the family grammar puts refused at exit 2 (docs/SPEC.md:145); the banner's exit table names the exception (cmd/nova-bus/help.go:16), but one status word spanning two exit codes complicates programmatic inspection | keep REFUSED for exit 2 and print FAILED on the exit-1 lines, or document the convention in the family spec | M |
| 7 | cmd/nova-bus/wait.go:112 | three retired flags remain declared on the CLI; each prints one NOTE line naming them retired and ignored (cmd/nova-bus/wait.go:282), which alerts the caller, but the public interface continues to advertise flags that perform no action | schedule deprecation and removal of the retired flags once callers have migrated | S |
| 8 | docs/CLI.md:428 | the opening claims a rejected push never reaches a person, but while bounded retry handles concurrent pushes within the attempt bound (docs/CLI.md:527), retry exhaustion emits SEND FAILED and git stderr output (cmd/nova-bus/send.go:179) — the prose promise is broader than the code | qualify the claim: retry handles concurrent pushes within the bound, and exhaustion produces SEND FAILED with the transcript | S |

## Good, keep
- The entry-point comment that teaches the tool by the failures it closed, in the present tense (cmd/nova-bus/main.go:1-45): a reader learns the verbs, the cursor's rationale, and the bus's architecture in one pass.
- The cursor design paragraph that states the price of the read plainly, twice, before and after the mechanism (docs/CLI.md:646-648).
- Every verb's effect line at -h and in the reference, from inspection to delivery, with the exact flag that strengthens it (cmd/nova-bus/help.go:77-89).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 467-line function on the read path | STILL THERE | cmd/nova-bus/inbox.go:285, 466 lines, one function handling cursor validation, bounded walk, body paging, and output formatting |
| 16-parameter calls | CHANGED | narrowed to fourteen and thirteen positional parameters (cmd/nova-bus/inbox.go:838, cmd/nova-bus/inbox.go:911), called positionally at cmd/nova-bus/inbox.go:166-168 |
| war-story comments | CHANGED | comments in cmd/nova-bus/main.go:29-41 state the failure each verb closes in the present tense; past-tense issue references remain in tests |
| the checkout lock and read refusals printed at exit 1 | STILL THERE | five INBOX REFUSED lines return 1 (cmd/nova-bus/inbox.go:326-364) against the family grammar's refused at 2 (docs/SPEC.md:145) |
| retired flags still declared | CHANGED | each retired flag now prints one NOTE line naming it retired and ignored (cmd/nova-bus/wait.go:282-283) |
| the prose promising more than bounded retries deliver | CHANGED | docs/CLI.md:527 states measured attempt counts beside the retry mechanism; the unreserved claim remains at docs/CLI.md:428 |
