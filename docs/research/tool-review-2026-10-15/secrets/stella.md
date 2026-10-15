# nova-secrets AI-usability review, 2026-10-15

Reviewer: mercury-3-preview
Build: d7e47f037ea8
Score: 7.5/10 (41/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-secrets help` -> `usage:` block with runnable `example:`; `nova-secrets names --store store --as ada` -> `SECRETS NAMES OK as=ada keys=3 shown=3 sealed=2 clear=1` |
| 2 | refusals | 3 | `nova-secrets names` -> `SECRETS NAMES REFUSED: missing --store <dir>, --as <name>; run: nova-secrets names -h`; but `nova-secrets names --max notanumber` -> `invalid value for --max: ...` carries no `run:` remedy |
| 3 | output | 4 | `nova-secrets names --store store --as ada --json` -> `{"result":{"verb":"names","status":"ok","exit":0},...}`; stable `SECRETS NAME key=... clear=...` line; but only `names` accepts `--json` |
| 4 | dry-run | 3 | `nova-secrets place ... --dry-run` -> `SECRETS PLACE REFUSED: sops binary ./nonexistent-sops is absent or not executable; run: go install github.com/getsops/sops/v3/cmd/sops@latest`; dry-run exists for place/seal/seat inject, missing for keygen and seat add |
| 5 | idempotent | 1 | `nova-secrets place --op abc123` -> `SECRETS PLACE REFUSED: unknown flag --op; ...`; no write verb takes `--op` |
| 6 | no prompts | 3 | seal reads a value from the terminal unless `--stdin`; no other verb waits on input |
| 7 | exit codes | 4 | `nova-secrets names` -> EXIT 2; `nova-secrets names --store store --as ada` -> EXIT 0; help states 0/1/2, exec adds 125 (unverified) |
| 8 | one word | 5 | verbs `exec, names, check, gate, keygen, place, placed, seal, version, help`; two-word groups `seat add`, `seat inject`; no duplicates |
| 9 | push | 5 | n/a: no waited-on state; one-shot commands, gate inspects git refs once |
| 10 | no leaks | 5 | no output or refusal line leaked a value across every run |
| 11 | true help | 4 | exit-code table holds; but examples hardcode `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`, which do not exist on Linux |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets place --json`, `check --json`, `keygen --json` | `SECRETS PLACE REFUSED: unknown flag --json; the flags of place are --as, --dry-run, ...` | accept `--json` on every verb, per the house standard | M |
| 2 | `nova-secrets help` example block | `... --sops /opt/homebrew/bin/sops ...` and `--age-keygen /opt/homebrew/bin/age-keygen` are macOS-only paths, not true on Linux | use `"$(command -v sops)"` and `"$(command -v age-keygen)"` in every example | S |
| 3 | `nova-secrets names --max notanumber` | `SECRETS NAMES REFUSED: invalid value for --max: it wants n names to list before a MORE line; 0 lists all (default 20)` has no `run:` next command | append a `run: nova-secrets names -h` remedy to value refusals | S |
| 4 | `nova-secrets seal` without `--stdin` | reads the value from the terminal and can block an unattended AI | make the default read stdin, or refuse with a `run:` remedy when stdin is not a pipe | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | write verbs | the house standard asks for idempotent `--op`; no verb takes it | add `--op <id>` to write verbs | M |
| 2 | `keygen`, `seat add` | no `--dry-run` for writes that make files | add `--dry-run` | S |
| 3 | `exec` exit codes | help adds 125 for a refused exec; only 0/1/2 elsewhere | keep, but document 125 in a table with 0/1/2 | S |

## Top 3 fixes
1. Make `--json` work on every verb.
2. Replace platform-hardcoded `sops`/`age-keygen` paths in examples with `command -v`.
3. Give every refusal a `run:` next command, including bad-value refusals.

## Good, keep
- `names` runs cold on a store and its `--json` is a single parseable object with facts and typed items, never a leaked value.
- Refusals name the missing flags, the known verbs, and a remedy in one line for the common cases.
- The zero-leak discipline held: no value, sealed or clear, appeared in any output.

## What could not be tried
- `keygen`, `seal`, `place`, `check`, `exec`, `place`, `seat add`, `seat inject`, `gate`: each needs to run `age-keygen` or `sops`, whose binaries the sandbox denies execution of, or a live git upstream; judged from help and `--dry-run` behavior up to the point they call `sops`. `names` was exercised against a scratch store.
