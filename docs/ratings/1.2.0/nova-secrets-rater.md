# nova-secrets READ and USE rating, nova-tools 1.2.0

Rater: a sprint worker on a friend's re-rate card
Build: d665016b9693
READ: 7/10
USE: 6.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. Built and run on a Linux bench machine with real sops and age in a scratch directory; no live store, no server, no gh call. Every verb was tested: version, keygen, names, check, seal, seat add, seat inject, gate, place, placed, and exec.

## Reasons

READ. The banner's first line says what the tool is; `how it works:` names the store's nouns (sops.yaml, recovery.pub, seat files); `setup:` gives a paste-able block; every verb answers `-h` with usage, effect, flags, and exit codes. Refusals follow one grammar: `SECRETS <VERB> REFUSED: <reason>; run: <remedy>`.

What keeps READ at 7. The help is complete and well-structured. However, `setup:` in the banner doesn't create a functional first seat (see findings). `first value:` in the banner can't run after setup: `seal` refuses new seats; `seat add` needs a source seat with values. The example uses hardcoded `/opt/homebrew/bin/sops` paths that won't work on Linux. The exit-code paragraph appears under every verb, including version and keygen where it doesn't apply.

USE. Once a seat file exists, the core loop works: exec puts values in the child environment and passes through the child's exit; names and check read files without printing values; seal and seat inject work with --dry-run and --no-pr; gate judges PR changes; place and placed manage machine secrets. Refusals are usually runnable and carry remedies.

What keeps USE at 6.5. The tool has no way to create the first seat's first value via printed commands: seal refuses new seats; seat add needs a source. check doesn't verify that --key can open --as's file (passes even with wrong key). After --no-pr operations, exec can't see the new names until a manual merge/push. The --json flag exists on names only, not other verbs.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | nova-secrets help setup/first value | After setup, seal refuses "ada.yaml does not exist; never by seal, use seat add"; seat add needs a source seat with values. No printed command creates the first seat's first value. | Let seat add with no source write an empty sealed file for a first seat, and document that in setup. | M |
| 2 | check with wrong key | `check --as ada --key wrong.key` passes with mine=0; check -h says it proves --key opens --as's file. | Fail if --key's public half is not a recipient of <as>.yaml. | S |
| 3 | help examples | Examples hard-code `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`; flag text says `command -v sops`. On Linux, first value fails. | Use `$(command -v sops)` in all examples. | S |
| 4 | exit codes in help | Every verb's -h shows the full exit-code paragraph about check and exec, including version and keygen where it doesn't apply. | Show each verb's own exit causes only. | S |
| 5 | exec with wrong key | `exec --key wrong.key` gives "sops failed: exit 128" without explaining the key isn't a recipient. | Compare key's public half with file's recipients first, then give a better message. | S |
| 6 | gate/placed grammar | Gate and place verdicts use `GATE REFUSE rule=0` instead of `SECRETS GATE REFUSED` with a `run:` line. | Route all verdicts through the one grammar. | S |
| 7 | --json flag | Only names supports --json; check, exec, seal, gate, and place refuse it. | Give --json to all verbs that have result lines. | S |

## Good, keep

The tool follows a consistent grammar: SECRETS <VERB> REFUSED: <reason>; run: <remedy>. Once set up, exec is the right shape for an AI: values go only into the child's environment, the OK line names the seat/file/head without printing the value, and the child's exit passes through. Refusals are usually runnable as printed. seal with --stdin keeps values off argv; --dry-run on seal, inject, and place prints PLAN lines showing recipients and branches without reading values. gate refuses rules missing the recovery key.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| first-value workflow missing | STILL THERE | setup doesn't create a first seat; seal refuses new seats |
| examples use hardcoded paths | STILL THERE | /opt/homebrew/bin/sops in first value and examples |
| check doesn't verify key opens file | STILL THERE | wrong key passes check with mine=0 |
| --json only on names | STILL THERE | check, exec, gate, seal refuse --json |
