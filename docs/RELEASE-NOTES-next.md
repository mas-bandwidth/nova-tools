# nova-tools next release (since v0.15.2)

A cheerful workshop keeps growing, and this release is two new benches and a
handful of verbs friends kept reaching for. `nova-pulse` runs a whole card
cycle with no model tokens of its own, `nova-secrets` holds credentials nothing
else can read, and `nova-wake`, `nova-bus` and `nova-swarm` each got the one
word they were missing — presence, replies and the native worker. Reading stays
bounded, refusals name their remedy, and every new line is one a transcript can
rely on. Pick the row that is your actual problem today; one new verb is a fine
number.

## nova-bus

- **New verb: `close`** — `nova-bus close --bus <dir> --as <name> --before <RFC3339>` answers the whole backlog at once: every open note dated before the stamp is closed by one receipt note each, batched in one commit, and the notes stay where they are. `--dry-run` reports and writes nothing.
- **New flags:** `draft --reply-to <id-or-path-or-subject>` is the bounded reply transaction — the header is the tool's, not hand-built — with `--body-file`, `--draft-dir`, `--max-body-bytes`; `draft --file <path>` is the redirect in flag form; `inbox`/`wait` take `--bodies [--max-notes <n>] [--max-bytes <n>] [--after <token>]`; `wait --beat`/`--beat-lease` control the presence beat; a `To:` line may name the broadcast aliases `all` and `table`.
- **New flags for a harness that cannot loop:** `wait --until <RFC 3339 instant>` is an absolute deadline beside `--timeout` and the earlier of the two ends the call; `wait --idle-exit <n>` is the exit code a TIMEOUT returns instead of 0, so a harness branches on the code rather than parsing (1 and 2 are refused, they are the tool's own). A caller that passes neither sees today's lines byte for byte. `inbox --max-commits <n>` (default 500) bounds the since-walk, and the count that decides it is now asked with its bound, so a cursor 500 commits behind and one 50,000 commits behind cost the same 501 commits.
- **Changed output lines:** `SEND OK … attempts=1` now ends `wakes=1` (the `To:` count); every `inbox`/`wait` return has exactly one `INBOX OPEN` line.
- **Changed behaviour:** `wait` blocks only when nothing is new and is byte-identical to `inbox` otherwise; with `--advance` it skips heard notes before blocking. A wait writes a `BEAT` with `until=` on entry, tick and exit. `--receipt-max-words` may come from a `receipt-max-words=<n>` line in `<bus>/.nova-bus/defaults` or `NOVA_BUS_RECEIPT_MAX_WORDS`. Unchanged unreadable notes collapse to one line; the terminal rearm line quotes its own arguments.

## nova-wake

- **New verbs:** `probe` and `awake`. `probe --bus <dir> --line <name> --state <file>` is the one verb that gates: 0 is `PRESENT` or `ANSWERED`, 1 is `SILENT`, `PINGED`, `UNAVAILABLE`, `UNRECONCILED` or `RESTING`, and it never writes a cause. `probe --here [--quiet-load <x>]` reads this bench's load, CPU count and process count. `awake --bus <dir> [--window <seconds>] [--max <n>]` reads presence over the bus cursors: one `FRIEND` line per line, one `AWAKE OK` verdict.
- **New flags:** the watch gained `--pr`, `--owned-prs` (with `--not-mine`), `--ref`, `--run`, `--lock` and `--forge-interval`; `probe` takes `--silent-after` (default 5m) and `--answer-within` (default 2m); the repeated flags live in a config file, `<cwd>/.nova-wake/config` or `NOVA_WAKE_CONFIG`, with `bus=`, `state=`, `as=`, `max=` and `window=` keys.
- **Changed output lines:** the `WAKE CHANGE` verdict gained `prs=0 runs=0 branches=0 locks=0`; `probe --here` prints `WAKE HERE at=… load=… cpus=… procs=…`.
- **Changed behaviour:** `awake` honours the beat lease — a wait's `until=` on the `BEAT` file keeps a working duty cycle reading `awake` between waits.

## nova-review

New: one bounded, exact-revision **review packet**, no opinion and no merge.

- **Verbs:** `packet`, `version`, `help`. **Flags:** `packet --lane <dir> (--pr <n>|--branch <name>) --who <name> --out <file> [--head <sha>] [--spec <path>]... [--rule <spec>:<n>]... [--max <n>] [--max-bytes <n>] [--reuse <file>] [--timeout <seconds>]`.
- **Output lines:** `PACKET OK`, `PACKET STALE` (exit 1, the head moved), `PACKET REUSE`, `PACKET REFUSED`.
- **Changed behaviour:** base and PR heads come from the `--repo` remote, fetched before the range; `--out` must be relative under the cwd or absolute under the cwd or the lane; packets are immutable, so an existing `--out` is a refusal; a missing-entry refusal names the remedy.

## nova-pulse

New: one tool, five verbs, no model call — every token is a card's.

- **Verbs:** `pool`, `cut`, `launch`, `harvest`, `manager`. `harvest` is no longer marked `(not yet implemented)`.
- **Flags:** `pool --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]`; `cut --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]`; `launch --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]`; `harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]`; `manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n> [--max <n>]`.
- **Changed behaviour:** `pool --max` is defined and honored; `manager` is the bounded controller — each cycle is wait, notes, harvest, triage, merge, refill and one `MANAGER` line, and `--hours 0` runs one cycle and ends `SHIFT END`; `cut` routes by the cheapest capable model from the cost table (`benches.tsv` or `routes.tsv`) and prints `CUT ROUTE route=<model> reason=<class>` per card.

## nova-secrets

New: credentials for seats, pools and services, sealed with age and sops and decrypted only for one call.

- **Verbs:** `exec`, `names`, `check`, `keygen`. **Flags:** `--store`, `--as`, `--key`, `--sops`, `--age-keygen`, `--only`, `--require`, `--max`.
- **Changed behaviour:** no verb prints a secret value and no flag makes one — `get`, `print`, `show` and `cat` are refused forever; `exec` injects the named keys into one child command and nothing else.

## nova-swarm

- **New verbs:** `native` (one frozen OpenCode run, `--harness --model --card --slot --root --deadline`, plus `--config` and `--auth`), `bench` (the SPEC-SWARM benches, `table-and-probe` and `remote-run`), `publish` (push by refspec and a draft PR by the tool), `verify` (`--result --contract --label`, with `--card`, `--run-record`, `--usage`).
- **New flags:** `run --no-auto-retry`; the card-form batch gained `--id --cards --deadline --runner --root [--idle <s>] [--then <command>] [--benches <file> --bench <name>]`; `add`/`batch`/`requeue` gained `--label`, `--template`, `--deadline`.
- **Changed output lines:** `RUN POOL workers=… hours=… worker=… model=… pool=…` now carries `auto_retry=true`; the native capture is `harness-output.log` and a job log is appended to, never truncated — `--no-wall` captures `harness.log` like the walled path.
- **Changed behaviour:** a silent harness is never OK — `reason=harness-silent`; batch holds a slot lock, refuses a live slot and takes over a stale one, abstain names its reason, and `--then` runs the follow-on only on `done=n`; native `--config` admits a keyless provider (a `baseURL` with no `apiKey`) so local models run walled; relative `--root`/`--slot` are absolutized at admission; private repositories are refused at admission; `TMPDIR` is exported outside any git repo (`slot/tmp/<label>`); nothing runs unwalled without a flag.

## nova-tokens

- **New verb: `profiles`** — `nova-tokens profiles --swarm-root <dir>` folds, per model, the card count, the median `tokens_out` and the overshoot past each card's own budget line (`PROFILES MODEL` lines, one `PROFILES OK`).
- **New flags:** `sum` gained the ledger form `sum --swarm-root <dir> --day <YYYY-MM-DD> --out <ledger.tsv>`; help lists the `--swarm-root/--day/--ledger` form; the `--provider` kind `xai` accepts grok usage JSON; `check --strict` and `check --no-spend <file>`; `sources --unattributed`.
- **Changed output lines:** `sum` prints `TOKENS AVG day=… model=… tokens=… usd=… usd_per_mtok=…` — average cost per token per model per day. `CHECK OK` and the `CHECK FAIL` count line gain `gap=<n> notes=<n>`; `SOURCES OK` gains `unattributed=<n|->`; `SOURCES UNATTRIBUTED stem=<path> tokens=<n>` is a new listing.
- **Changed behaviour:** `sum` reports missing or malformed cost fields as unknown, never 0; `fold` merges by source and refuses a partial shrink before the write; `report` can report a quiet source on a selected day. **`check` can go green on a real directory:** a calendar day with no file is counted as `gap=` and named `CHECK MISSING` only under `--strict`, or under a `--no-spend` list that does not account for it; a `*.md`, a `*.log` or a `pre-*` archive beside the day files is counted as `notes=` instead of named as a stray. `--strict` restores the old reading whole. On this repository's own `reports/tokens` that is 40 findings — 36 days nobody worked and 4 files a person put there on purpose — turned into two counts on a green line.

## nova-version

- **New verb: `snapshot`** — `nova-version snapshot --bin <dir> --out <manifest> [--owner <name>]` writes the rule-2 manifest that `report` reads, one line per installed tool, from a bin directory.
- **Changed behaviour:** usage and refusal say what `--file` is and its shape — one line per tool, six tab-separated fields, written by hand.

## nova-check

- **New flags:** `links --exclude <prefix>` (repeatable) leaves a subtree unscanned and skips links into it, with the count on the `LINKS OK` line.

## nova-sandbox

- **Changed behaviour:** a Linux (Landlock) backend now runs, so remote benches run walled; `--tmp` confines zsh's `TMPPREFIX` as well as `TMPDIR`/`TMP`.
- **Changed output lines:** `SANDBOX OK` gains `ancestors=<n>`; `CHECK OK` gains `hosts=none`.

## nova-merge, nova-update, nova-board

No new verbs or flags. **`nova-merge simulate` now leaves no worktree behind and keeps the exit table docs/CLI.md documents:** the scratch worktree's directory *and* git's entry for it under `.git/worktrees/` both go (the old removal used `git worktree remove --force`, which the lane's git seam refuses in any argument, so it never ran and its error was discarded), and an invalid invocation — an unreadable or non-numeric `--entries`, a `--repo` that is not a repository, a `--base` or a `pull/<n>/head` the origin does not have — is exit 2 as the docs say, not 1. `nova-merge`'s fetched-tip fold is lock-free and retries only the failed records; `nova-update`'s reporter-death cases are staged and tested; the spec-versus-code drift that kept their docs honest was closed.

## Every tool: `<verb> --help` answers

`<tool> <verb> --help` (and `-h`) now prints THAT verb's usage on stdout and
exits 0, in every binary here. It used to exit 2 with `flag: help requested` --
package flag's own sentinel text, handed to somebody who asked a reasonable
question -- because every verb parses with a `flag.ContinueOnError` set whose
output is discarded, and the sentinel was treated as a parse failure. A
dogfooder measured it across the family on 2026-09-18. `internal/cliflags` is
the one answer, and a class test walks every flag set in `cmd/` and `internal/`
so it cannot come back. A bad invocation is still exit 2: a mistyped flag, a
missing one, a positional argument where none is taken.

## Upgrading

```
go install ./cmd/...
nova-version snapshot
nova-update watch --adopt
```

`go install` puts every new and old tool on your bench; `nova-version snapshot` writes the manifest the next `nova-version report` reads, so the friend sequence starts from the tools you actually have; `nova-update watch --adopt` is where a declared version stops being a note and starts being a decision the update reader keeps.