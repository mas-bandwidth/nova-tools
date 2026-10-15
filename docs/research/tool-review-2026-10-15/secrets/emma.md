# nova-secrets AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b930c415a495
Score: 7.5/10 (42/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 3 | `nova-secrets version` -> `nova-secrets v0.0.0-20261015194509-b930c415a495 linux/amd64 go1.26.6` (exit 0), but running the `setup:` and `first value:` recipe from `nova-secrets help` in a scratch dir fails at `first value:` with `SECRETS SEAL REFUSED: ada.yaml does not exist in the store; a new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule 12); run: nova-secrets seal -h` (exit 2) |
| 2 | refusals | 3 | `nova-secrets check` -> `SECRETS CHECK REFUSED: missing --store <dir>, --as <name>, --key <path>, --sops <path>; run: nova-secrets check -h` (exit 2); however when `ada.yaml` is absent, `names`, `check`, and `exec` all advise `seal writes a seat's first value: run: nova-secrets seal ...`, which `seal` immediately refuses |
| 3 | output | 3 | `nova-secrets names --store ./secrets --as ada --json` -> `{"result":{"verb":"names","status":"ok","exit":0},"facts":{"as":"ada","keys":1,"shown":1,"sealed":1,"clear":0},"items":[{"kind":"key","fields":{"key":"SAMPLE_KEY","clear":false}}]}` (exit 0), but only `names` supports `--json`, `keygen` prints conversational sentences after `SECRETS KEYGEN OK`, `seal`/`seat` print lowercase `seal: reading ...` lines, and `gate` prints `GATE APPROVE` without the `SECRETS` prefix |
| 4 | dry-run | 4 | `nova-secrets seal --store ./secrets --as ada --key ./keys/ada.key --sops /home/nova/.local/bin/sops --name SECOND_KEY --dry-run` -> `SECRETS SEAL DRY-RUN OK name=SECOND_KEY seat=ada nothing written, no value read, no push, no gh call` (exit 0); `place` and `seat inject` also support `--dry-run`, while `keygen` and `seat add` refuse `--dry-run` |
| 5 | idempotent | 4 | `place`, `seal`, and `seat inject` update existing keys idempotently and `keygen` refuses to overwrite an existing key file, though none take `--op` |
| 6 | no prompts | 4 | `exec`, `names`, `check`, `gate`, `place`, and `placed` never prompt; `seal` without `--stdin` prompts interactively on the terminal unless `--stdin` is passed |
| 7 | exit codes | 5 | `check` -> exit 0; `gate` on an unsealed edit -> exit 1; `names` missing flags -> exit 2; `exec` missing flags -> exit 125 |
| 8 | one word | 4 | Secret key names are called `--only <NAME,...>` on `exec`/`seat add`/`seat inject`, `--name NAME` on `seal`, and `--secret <name>` on `place` |
| 9 | push | 4 | `seal` and `seat inject` without `--no-pr` poll GitHub pull request status for up to 2m waiting for gate approval |
| 10 | no leaks | 5 | `nova-secrets exec --store ./secrets --as ada --key ./keys/ada.key --sops /home/nova/.local/bin/sops --only SAMPLE_KEY --require SAMPLE_KEY -- /bin/true` -> `SECRETS EXEC OK as=ada keys=1 only=1 required=1 file=secrets/ada.yaml head=1f1a936 cmd=/bin/true` (no secret value is ever printed in output, dry-run plans, or receipts) |
| 11 | true help | 3 | `nova-secrets help` `first value:` tells the caller to run `nova-secrets seal` on a newly initialized store with no `ada.yaml`, which `seal` refuses; `example:` hardcodes `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`; `nova-secrets help seat` and `nova-secrets seat -h` dump the 115-line root banner instead of `seat <add,inject>` help |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets help` (`setup:` + `first value:`) and `nova-secrets names --store ./secrets --as ada` | Following `setup:` and `first value:` in `nova-secrets help` fails at `nova-secrets seal` with `SECRETS SEAL REFUSED: ada.yaml does not exist in the store; a new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule 12)`, while `names`, `check`, and `exec` on a missing `<seat>.yaml` still print `seal writes a seat's first value: run: nova-secrets seal ...` and `seat add` requires an already-existing `--from` seat file | Either allow `seal` to bootstrap the first `<seat>.yaml` when the store holds no `.yaml` seat files yet (or add a bootstrap flag/step to `setup:`), and update the absent-seat remedy in `names`, `check`, and `exec` so it never recommends a command `seal` refuses | M |
| 2 | `nova-secrets seat -h` and `nova-secrets help seat` | Prints the entire 115-line `nova-secrets` root banner at exit 0 instead of usage for `seat add` and `seat inject` (carried from the 2026-10-02 ratings) | Print a concise `usage: nova-secrets seat <add,inject> [flags]` summary listing `seat add` and `seat inject` and pointing to `nova-secrets seat add -h` | S |
| 3 | `nova-secrets seat add --dry-run` and `nova-secrets keygen --dry-run` | Both local-write verbs refuse `--dry-run` at exit 2 (`SECRETS SEAT ADD REFUSED: unknown flag --dry-run` and `SECRETS KEYGEN REFUSED: unknown flag --dry-run`) | Add `--dry-run` to `seat add` and `keygen` so every write verb in `nova-secrets` can plan without touching the filesystem | S |
| 4 | `nova-secrets help` (`first value:` and `example:`) and `nova-secrets keygen` | `first value:` and `example:` hardcode `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`, which fail on Linux benches (`sops binary /opt/homebrew/bin/sops is absent`); `keygen` prints `SECRETS RULE NEXT: add these two lines to .sops.yaml` after printing three `SECRETS RULE` lines, plus two unstructured prose lines (`Done. Your new key is at ...`) after `SECRETS KEYGEN OK` (carried from the 2026-10-02 ratings) | Use `"$(command -v sops)"` and `"$(command -v age-keygen)"` in `first value:` and `example:`, fix `two lines` to `three lines`, and fold the post-`OK` prose into `SECRETS KEYGEN` lines | S |
| 5 | `nova-secrets placed --machine m1 --receipts ./nodir` | Returns `SECRETS PLACED OK machine=m1 count=0` at exit 0 when an explicitly passed `--receipts` directory does not exist (carried from the 2026-10-02 ratings) | Refuse an explicit nonexistent `--receipts` directory at exit 2, or report `receipts=absent` on the `OK` line | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets check --json`, `nova-secrets version --json`, `nova-secrets placed --json`, `nova-secrets gate --json` | Only `names` accepts `--json` (carried from the 2026-10-02 ratings); all other verbs refuse `--json` at exit 2 | Add `--json` output to `version`, `check`, `placed`, `gate`, and the `--dry-run` plans of `seal`, `place`, and `seat inject` | M |
| 2 | `nova-secrets gate --store ./secrets --base HEAD~1 --head HEAD` and `nova-secrets seal --dry-run ...` | `gate` outputs `GATE APPROVE` / `GATE FAILED` without the `SECRETS ` prefix used by `SECRETS GATE REFUSED` and every other verb, and `seal`/`seat add`/`seat inject` print lowercase `seal: reading ...` lines before `SECRETS ...` lines | Prefix `gate` result lines with `SECRETS ` and use `SECRETS <VERB> STEP` for progress lines | S |

## Top 3 fixes
1. Resolve the store-bootstrap Catch-22 between `nova-secrets help` (`first value:`), `names`/`check`/`exec`'s absent-file remedy (`seal writes a seat's first value`), and `seal`'s rule-12 refusal when `<seat>.yaml` does not yet exist.
2. Make `nova-secrets help seat` and `nova-secrets seat -h` print sub-verb help for `seat add` and `seat inject` instead of dumping the 115-line root banner, and add `--dry-run` to `seat add` and `keygen`.
3. Replace hardcoded `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen` paths in `nova-secrets help` examples with `"$(command -v sops)"` and `"$(command -v age-keygen)"`, and clean up `keygen`'s unstructured trailing lines.

## Good, keep
- Strict secret containment across every verb: `exec` injects only `--only` keys into a single child process environment and returns 125 on its own refusal, `names` lists keys without decrypting, and `--dry-run` on `seal`, `place`, and `seat inject` plans the full operation without printing any secret value.
- Missing-flag refusals on `names`, `check`, and `exec` list every missing required flag in a single line (`missing --store <dir>, --as <name>, --key <path>, --sops <path>; run: nova-secrets check -h`).
- `seat add` paired with `nova-secrets gate --store <dir> --base <ref> --head <ref>` verifies that seat files are written by `nova-secrets` verbs (`GATE FAILED rule=1 check=2` on hand-encrypted files, `GATE APPROVE files=2 machines=-` on `seat add` commits; keeping the 1.2.0 `SECRETS GATE REFUSED` grammar fix).

## What could not be tried
- `nova-secrets place` (without `--dry-run`), `nova-secrets seal` (without `--no-pr` / `--dry-run`), and `nova-secrets seat inject` (without `--no-pr` / `--dry-run`): not run against fleet SSH targets or GitHub pull requests because touching live machines, remotes, or the real secrets store is forbidden by the review rules.
