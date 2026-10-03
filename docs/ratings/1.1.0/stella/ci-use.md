# nova-ci USE rating, nova-tools 1.1.0

Rater: gpt-5.6-sol
Build: 606d93e836d5
Score: 9.5/10

## Reasons
The first successful run is immediate: `nova-ci slowtests --example --budget 60 --load 4 --cpus 16` needs no setup and returns a precise slow-package finding plus a clearly non-verdict load measurement. On a separate event stream, package and test budgets produce compact findings with package, test, elapsed time, budget, and slowest test, so an AI can decide what to inspect next without guessing. The structured form is a single valid object whose result, facts, and items preserve the same decision.

The four refusal classes are unusually strong. Missing receipt fields are reported together, unknown flags enumerate the valid set, an unknown verb enumerates the verbs, and a malformed value names the expected type. Every refusal ends with the exact help command to run. Dry runs are equally explicit: the receipt path says that nothing was dialled or written, while the two scaffold paths list every proposed file and say how to perform the write.

The remaining half point is automation polish. Structured output is offered only for `slowtests`; receipt and scaffold dry runs require parsing stable-looking prose. `functional` emits the selected package and exact pattern, but an AI still assembles the test command and supplies the functional build tag. The main help is also long because the detailed budget contract appears inline before verb-specific help. A 10 would print a directly runnable functional command, offer structured output consistently, and keep the first screen centered on choosing a verb while retaining the excellent detail in `help <verb>`.

The service-writing verb was not tried against a real service, and `local` was judged from help because it requires this repository's own checkout. No key, remote, or service was used.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci new-rule --root ../repo --dry-run sample-check` | The dry run clearly lists three proposed paths, but it has no structured form for an agent to consume without parsing prose. | Add a common `--json` result shape to dry-run-capable verbs. | M |
| 2 | `nova-ci functional ./...` | The selector finds the package and exact test pattern, but an agent must assemble the test command and know to add the functional build tag. | Print a complete runnable test command after the selection. | S |
| 3 | `nova-ci help` | The first-run path is clear, but the top-level help embeds the full advanced budget contract and takes time to scan. | Keep a concise verb summary here and move the detailed contract to `help slowtests`. | S |

## Good, keep
Keep the setup-free built-in event stream and the exact commands in every refusal.
Keep the explicit dry-run statements about what was not touched and the next write command.
Keep deterministic load inputs and the distinction between measurements and enforced verdicts.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| The allowlist bound and SLEEPS marker are absent from help. | FIXED | `nova-ci help slowtests` describes both row formats, their enforcement behavior, and the cache caveat. |
| An unknown option omits the offending flag. | FIXED | `nova-ci slowtests --example --bogus` prints `unknown flag --bogus`, lists valid flags, and points to `nova-ci slowtests -h`. |
| `functional ./...` prints a path rejected by the test runner. | FIXED | From the sample module, `nova-ci functional ./...` prints `.` and `^(TestFunctionalRoundTrip)$`; the emitted selection passes with `go test -p 2 -count=1 -timeout 600s -tags functional . -run '^(TestFunctionalRoundTrip)$'`. |
