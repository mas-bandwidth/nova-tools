# nova-fuse AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: 52ada6aff220
Score: 9.0/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-fuse help` -> `nova-fuse init --box ./fuse.json` then `nova-fuse check --box ./fuse.json discord` (`FUSE OK surface=discord state=clear`, exit 0) |
| 2 | refusals | 4 | `nova-fuse check` -> `FUSE REFUSED: check needs --box <path>; pass --box <path> or run: nova-fuse help check` (exit 2); `nova-fuse check --bogus` -> `FUSE REFUSED: unknown flag --bogus; the flags of check are --box, --json; run: nova-fuse help check` (exit 2, hides missing `--box` and `<surface>`) |
| 3 | output | 4 | `nova-fuse status --box ./fuse.json --json` -> `{"result":{"verb":"status","status":"ok","exit":0},"facts":{"state":"LOCKED DOWN","quarantines":1,"shown":1,"since":"2026-10-15T19:10:02Z","why":"incident"}}` (on `check` failure, JSON omits `surface` and `state` from `facts`) |
| 4 | dry-run | 5 | `nova-fuse quarantine --box ./fuse.json --dry-run slack "webhook leak"` -> `QUARANTINE OK surface=slack since=2026-10-15T19:09:57Z dry_run=true: webhook leak` (exit 0, box unchanged) |
| 5 | idempotent | 3 | `nova-fuse quarantine --box ./fuse.json discord "second reason"` -> `QUARANTINE OK surface=discord already=quarantined since=2026-10-15T19:09:57Z: Phishing link (standing record kept; the new reason was not recorded: second reason)` (exit 0; `init` on an existing box exits 1 and `--dry-run` on an already-blown surface drops `dry_run=true`) |
| 6 | no prompts | 5 | `nova-fuse lockdown --box ./fuse.json "active incident"` -> `LOCKDOWN OK since=2026-10-15T19:10:02Z: active incident` (exit 0, non-interactive across all verbs) |
| 7 | exit codes | 5 | `nova-fuse check --box ./fuse.json discord` -> exit 0 when clear, exit 1 when quarantined or locked down, exit 2 on missing or malformed box |
| 8 | one word | 5 | `nova-fuse help` and `<verb> -h` use `--box`, `surface`, `quarantine`, `lockdown` consistently across every synopsis, flag description, and receipt |
| 9 | push | 5 | Local file-backed circuit-breaker box with no daemon, polling loop, or background wait |
| 10 | no leaks | 5 | `nova-fuse check --box ./missing.json discord` -> `FUSE REFUSED: cannot read "./missing.json": file does not exist; initialize an empty box with: nova-fuse init --box ./missing.json` (no Go stack traces, raw struct dumps, or secrets) |
| 11 | true help | 4 | `nova-fuse help` and `<verb> -h` match real flags and examples (`--json` on every verb, fixed since the 2026-10-02 rating), but `help lift` omits `effect:` and `Help:`, `init -h`, `status -h`, and `path -h` print a `--` positional separator row when they take no positionals, and `docs/SPEC.md` still prints `FAIL` where the binary writes `FAILED` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse quarantine --box ./fuse.json --dry-run discord "second reason"` | When a surface is already quarantined (or the box is already locked down), passing `--dry-run` prints `QUARANTINE OK surface=discord already=quarantined since=...` (and `LOCKDOWN OK already=locked_down ...`) without `dry_run=true` in either text or `--json` `facts`, breaking dry-run schema consistency (`cmd/nova-fuse/main.go:530,618`) | Include `dry_run=true` on the `already=quarantined` and `already=locked_down` paths when `--dry-run` is set | S |
| 2 | `nova-fuse check --bogus` | Prints `FUSE REFUSED: unknown flag --bogus; the flags of check are --box, --json; run: nova-fuse help check` (exit 2) and returns before checking `--box` and `<surface>`, so an AI fixes `--bogus` only to hit a second refusal for missing `--box` and `<surface>` (still present from the 2026-10-02 rating) | Continue after a flag-parse failure to report missing `--box` and missing positional arguments in the same refusal | S |
| 3 | `nova-fuse init --box ./fuse.json` | Running `init` a second time on an already-initialized empty box exits 1 with `INIT FAILED: box "./fuse.json" already exists; read it with: nova-fuse status --box ./fuse.json`, and running `init --box .` on a directory suggests `nova-fuse status --box .`, which then fails at exit 2 with `not a regular file` (still present from the 2026-10-02 rating) | Make `init` idempotent (exit 0 with `already=exists` when the existing file is a valid fuse box) or check `fi.IsDir()` before suggesting `status --box` | S |
| 4 | `nova-fuse check --box ./fuse.json --json discord` | When `discord` is quarantined or the box is locked down, text mode prints `FUSE FAILED surface=discord state=quarantined since=...` on the headline, but `--json` puts only `{"since":"..."}` in `facts` and drops `surface` and `state`, forcing JSON callers to regex-parse `why[0]` | Add `Fact("surface", surface)` and `Fact("state", "quarantined")` (or `"locked_down"`) to the failed `check` output in `cmd/nova-fuse/check.go` | S |
| 5 | `nova-fuse quarantine --box ./fuse.json discord` | Missing `<reason>`, blank `<surface>`, or missing both all produce the identical refusal `QUARANTINE REFUSED: quarantine needs a surface and a reason; run: nova-fuse help quarantine` without naming which positional argument was missing or blank (still present from the 2026-10-02 rating) | Name the specific missing or empty positional argument (`<surface>`, `<reason>`, or both) in `cmd/nova-fuse/main.go` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse help lift` | Prints usage and exit codes without the `effect:` or `Help:` lines present on every other verb page, and `nova-fuse help help` is refused at exit 2 as an unknown verb (still present from the 2026-10-02 rating) | Add `effect:` and `Help:` lines to `help lift` and accept `help help` as an alias for `nova-fuse help` | S |
| 2 | `nova-fuse init -h`, `status -h`, `path -h` | The shared flag block prints `--  treat the remaining words as arguments, not flags` on verbs that accept zero positional arguments (still present from the 2026-10-02 rating) | Print the `--` flag row only on verbs that take positional arguments (`check`, `quarantine`, `lockdown`, `lift quarantine`) | S |
| 3 | `nova-fuse lift quarantine --box ./missing.json discord` | Creates an empty `./missing.json.lock` sidecar file before discovering `./missing.json` does not exist, and no help page mentions `<box>.lock` (still present from the 2026-10-02 rating) | Document the `<box>.lock` sidecar in `help init` and avoid creating `<box>.lock` on `lift` when the box file itself is absent | S |
| 4 | `docs/SPEC.md:2043` | The grammar block in `docs/SPEC.md` still shows `FUSE FAIL`, `LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL`, and `INIT FAIL` while the binary emits `FAILED` (still present from the 2026-10-02 rating) | Update the `docs/SPEC.md` grammar tokens from `FAIL` to `FAILED` to match the binary | S |

## Top 3 fixes
1. Preserve `dry_run=true` in text and `--json` `facts` when `quarantine --dry-run` or `lockdown --dry-run` hits an already-quarantined surface or already-locked-down box (`cmd/nova-fuse/main.go:530,618`).
2. Include `surface` and `state` in `--json` `facts` on `nova-fuse check` failures so JSON output carries the same structured fields as the text headline (`cmd/nova-fuse/check.go:46-66`).
3. Report missing `--box` and missing positional arguments alongside unknown-flag errors in a single run, and distinguish missing `<surface>` from missing `<reason>` (`cmd/nova-fuse/main.go`).

## Good, keep
- Fail-closed reads: missing, empty, `null`, JSON array, unknown top-level key, directory, symlink, and unreadable files all exit 2 with a specific refusal and remedy, never `FUSE OK` (preserved from the 2026-10-02 rating).
- Every verb now supports `--json` (fixing finding 21 from the 2026-10-02 rating) and `<verb> -h` / `--help` exits 0 on stdout (`version -h` fixed since 2026-10-02).
- Surface normalization folds case and whitespace consistently across `quarantine`, `check`, and `lift quarantine`, and re-quarantining preserves the original timestamp and reason while noting the unrecorded second reason.

## What could not be tried
Every verb (`init`, `status`, `check`, `quarantine`, `lift quarantine`, `lockdown`, `lift lockdown`, `path`, `version`, `help`) was run end to end in a scratch directory because `nova-fuse` operates purely on a caller-specified `--box <path>` file with no daemon or live shared state.
