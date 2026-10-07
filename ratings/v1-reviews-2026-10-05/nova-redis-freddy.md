# nova-redis review, 2026-10-05

Rater: inception/mercury-2.5
Build: 3baf154bb6ef
Verdict: GOOD WITH FIXES for an AI to use
Score: 7/10

## Reasons
The help page (`nova-redis help`) is thorough and the `example:` block provides runnable commands. Each verb's usage is clear and the refusal grammar is consistent. However, there are a few friction points for an AI using this cold.

First confusion: the `spill` and `recall` verbs use `--ttl` for duration but there's no example showing valid duration formats beyond `10m` in the help. The spec mentions "time to live" but doesn't show enough examples to be sure what's accepted (is `10` valid? `10s`? `1h30m`?).

First doubted claim: the `fn load` and `fn check` verbs are described as handling the "nova_sprint function library", but there's no way to see what functions are in that library from the tool itself. An AI needing to call these functions can't discover them without reading the source.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis help` usage block | No example showing valid `--ttl` duration formats (is `10` valid? `10s`? `1h30m`?) | Add `--ttl 10m` to the spill example and mention "Go duration (e.g., 10m, 1h)" in the flag description | S |
| 2 | `nova-redis fn check` output | Doesn't show what functions are in the library, only the sha | Add a `-v`/`--verbose` flag that prints the function names alongside the sha | M |
| 3 | `nova-redis help` example | The spill/reall example doesn't show what happens on failure (missing key) | Add an example of `recall` returning MISSING when a key doesn't exist | S |

## Good, keep
The refusal grammar is excellent - all missing flags are reported in one run, and each names what the input WANTS. The `--password-env` pattern keeps secrets out of arguments and integrates cleanly with `nova-secrets`. The key form `<owner>:<name>` with explicit TTL requirements prevents unbounded keys by design.
