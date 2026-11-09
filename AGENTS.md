# AGENTS.md

One toolkit for a team of AIs on different models. Each tool can be used on its own.

## Start here

Read [the detailed guide](docs/README.md) to decide whether a tool fits the task,
install it and try it. That is the shared guide for humans and AIs; the root
[README](README.md) is the short human introduction. Each tool's README is its
reference, linked from the guide. [llms.txt](llms.txt) is a compact index with
plain Markdown links.

Choose the tool that solves the current problem. Check its prerequisites,
effects and limits before running it, use a disposable example first, and
compare the result with the documented output. A successful installation or
service startup does not establish that a workflow completed; for messaging,
verify the receiving AI's reply as well as transport delivery.

The sections below are rules for contributors changing nova-tools.

## Contributing

Read the [Contributor Assignment Agreement](https://github.com/mas-bandwidth/.github/blob/main/CAA.md)
before submitting a contribution. It assigns copyright in the contribution to
Más Bandwidth LLC and grants you a license to use your own contribution.
The agreement itself gives the full terms; the pull request's CAA check must
pass before merge. Report a reproducible problem or propose a focused change,
and use the build and review rules below.

## Tests

- Per-commit CI finishes in under 2 minutes per job (aim for 1). Nightly (schedule) jobs have no limit.
- Unit tests use no real time, sleep, network, sockets or subprocesses. Time is an argument, and clocks start at 1000.
- Every test calls `t.Parallel()`. Functional tests are named `TestFunctional*`; anything that needs a real clock, socket, process, PostgreSQL, Redis, sops or age is functional.
- Name test files and functions for the behaviour they check.
- Unit tests use fake stores, clocks and process runners. Functional tests use disposable directories, processes and services, never a live store.
- A real UDP socket, even on 127.0.0.1, never asserts 100% delivery.
- Mark every unmeasured number as a guess where it is defined.
- A functional test whose dependency is missing skips; report skips with the result. A passing run with skips does not establish the skipped cases. CI sets `NOVA_TEST_NO_HOST=1` for every job; set it when running the checks by hand.

## Design

- The tools work the same on 10,000 benches as on 10. A caller's cost is O(its own work). Idle costs about nothing: push, never poll.
- Nothing is hardcoded to one deployment: no org, host, provider, model, identity or person. Examples use neutral placeholders (`alice`, `bench-1`, `friendA`).
- Every state machine has a TLA+ model beside its code, and the code is bound to it by trace checks. A model that does not match the code is a defect: change both together. A new state machine gets its model before it ships.
- Every model has reversed witnesses (the model with one fault, which TLC must reject). Run TLC on a bench or CI runner, never on the coordinator machine.
- A refusal is one stderr line naming the reason and ending in a pasteable remedy `run: <command>`. No error is silent. An unknown name or a typo is a refusal, never an empty answer.
- Reach another tool only through its documented command line. Reach the bus only by executing `nova-bus`.
- Keep no dead code. Delete code only with evidence it is unused.
- Tools write no secret value to an argument, log, transcript or message.

## Change discipline

- Fixes are minimal and surgical: the best code, not the most. No feature creep.
- Fix a failure at its source and delete the workaround it replaces. Do not add a watcher, side loop or second mechanism around a tool.
- Commits use the one commit identity the contributor agreement allows.
- A gate marked DO NOT REMOVE is never removed or weakened.
- Check for stuck by count. The same problem again, a third pass over the same area, or no gain in a measurement over three fixes means stop and report.
- Zero compiler and vet warnings. `gofmt -l .` prints nothing.
- A new direct dependency goes into the allowlist in `tests/boundary` in the same change, with its purpose in the table below.

## Docs and comments

- Document the code, not the conversation. A comment states what the code does and the technical reason; delete a comment that is only history.
- Docs describe the tool as it is, never its history: no "used to", "now" or "no longer", no version or round numbers, no status notes or to-do lists.
- Every command, output line and refusal quoted in a doc must match what the binary prints. Change the doc in the same commit as the string. `TestDocs` (`tests/contract/readme_test.go`) checks every command's verb and flags, runs the help commands, and holds each quoted refusal to its entry in `docRefusals`: a new one needs an entry, the command that reaches it cold or why it cannot.
- This file holds contributor and internals instructions. User documentation is the root `README.md`, `docs/README.md`, `docs/SECURITY.md` and each tool's `README.md`. Harness entry points in `docs/integrations` and `llms.txt` link to that shared guide; they do not duplicate it.
- No people's names in code, comments, docs or tests.

## Layout and boundaries

- Executables live in `cmd/<command>/`: `nova-bus`, `nova-cairn`, `nova-config`, `nova-fleet`, `nova-reserve`, `nova-sandbox` and `nova-secrets`. Each command is one directory of `package main` files and is useful without knowing the workflow of the team that built it. Reusable packages live in `modules/<module>/`; `modules/config` is the shared config reader.
- Beside `cmd` and `modules`: `docs` (user docs and images), `tests` (checks that span tools: `tests/boundary` layout and imports, `tests/contract` the command-line contract, `tests/tla` the TLC runner and pinned jar checksum, `tests/BUDGET`) and `.github` (CI). Each command keeps its guide, embedded assets and models beside its Go files; models live in `cmd/<command>/tla/`.
- Root files are exactly `README.md`, `AGENTS.md`, `LICENSE`, `go.mod`, `go.sum`, `.gitignore`, `.gitattributes` `context7.json`, `llms.txt` and `gemini-extension.json`, plus the `.claude-plugin` marketplace directory. The Context7 configuration indexes the public usage guides and excludes contributor instructions and test fixtures. An extra top-level entry fails the layout test, even when Git ignores it. No top-level or recursive symlinks (`.git` and `docs` are skipped). Build outputs live outside the checkout.
- A tool imports no other package of this module. A few repeated lines beat a shared layer. The one exception is `modules/config` (package `config`): nova-config's reader of its own store, so a consumer reads the same rows through the same query. It imports nothing from this module, only nova-config imports it, and its lines count in config's budget (`toolLibs` in the boundary test). Another library needs the case made in the same change.
- A tool does not call a sibling nova tool by name to do its own job. nova-fleet is the exception: it execs `nova-config` (inventory), `nova-secrets` (values a play needs) and `nova-bus` (end-to-end check) from PATH and imports none of them. Workflow policy belongs in a consumer, not here.
- `go.mod` has no `replace` directive. The permitted tool directives are errcheck, txtar, deadcode and staticcheck (`allowedTools`). A module imported by a Go file counts as direct even if go.mod labels it indirect. Remove unused direct dependencies from the test's list.

| Dependency | Use |
|---|---|
| pgx | PostgreSQL access for config. |
| go-redis | Redis access for bus. |
| yaml.v3 | Reading and writing the YAML files of a secrets store. |
| x/sys | Platform system calls. |
| testify | Test assertions. |
| hujson | fleet's merge into the tailnet policy: a JSON Patch applied to the HuJSON text, so a human's comments and formatting survive. |

- `tests/BUDGET` gives each Go directory a maximum of non-test source lines (comments and blank lines count; `_test.go` files do not). One `name integer` per line; no comments or duplicates. A directory with Go files needs an entry, including the boundary and contract packages. A change over budget first looks for work the tool no longer needs to do; a raise needs a reason in the same change. Lower the budget when code is removed.
- To add a tool: a `cmd/<command>` directory of `package main` files with a `README.md` guide; add it to `mainTools` and `tests/BUDGET`; state the one job it does and why no existing tool covers it. A new tool's budget is its size when its design is settled, plus 50 lines.

## Command-line contract

`tests/contract` (functional tier) builds every tool and holds it to these rules; a new verb goes into `toolSpecs`.

- `<tool> help` prints the banner; `<tool> help <verb>`, `<tool> <verb> -h` and `--help` print one operation. Help goes to stdout, exits 0, changes nothing. `help` itself takes no flags (`help -h` is exit 2 on bus, secrets and reserve).
- Exit 0: the operation succeeded. Exit 1: the tool ran and refused, found a conflict, or a check found a problem. Exit 2: the invocation was wrong or a setup failure prevented it from running (such as schema not loaded, missing binary or failed login). nova-bus also uses exit 1 when Redis cannot be reached. Read the tool's documented refusal and remedy before deciding whether to retry; an exit code alone does not establish that a retry can succeed.
- Sandbox and secrets `exec` also use 125 (refused to start, could not launch), 126 (found but not executable) and 127 (not found; reserve names the PATH it searched). A wrapper returns its command's status after launch.
- An unknown verb or flag, a missing argument or a malformed value is exit 2; the error names the problem and points to help. A flag belonging to another operation is refused, not ignored. Required inputs are reported together where they can be found before reading a store.
- Argument order follows the tool's examples: bus and cairn take flags before positionals; config accepts a row name before its flags; secrets accepts `set NAME` or `seat add SEAT KEY` before flags. Sandbox, secrets `exec` and reserve use `--` before the command. Bus takes `-` as its body to read stdin; secrets `set` reads its value from stdin.
- Each tool has its own output format; the guide states it. Hook lines are tab-separated with backslash, tab and line break escaped. Config `list` and `show` take `--json`. Secrets `exec` puts progress on stderr and leaves stdout to the command.
- An OK line may be followed by one last line `next: <command>`: a real, shell-quoted command with a real example value, built with the tool's command helper. Never a `next=` field, never `\x20` for blanks, nothing after it.
- Config writes accept `--dry-run`; fleet plays accept Ansible's `--check`. Read stderr as well as the status: a successful item can precede a later failure.
- Cairn prints its version with `version`; the other tools are identified by release tag or source commit.

## Release communication

For each published release, prepare a Patreon update with the exact tag,
user-visible changes, a link to the release and this guide, and a brief support
invitation. Check the creator dashboard for an existing post before publishing
through the authorized account. Describe only shipped work and observed results;
a prepared draft is not a published post. Keep the README credit and support
links and `.github/FUNDING.yml` pointed at the same verified account.

## Build and CI checks

Run builds and tests from the repository root, on a build machine, with the Go version in `go.mod` or a compatible newer one. Keep the job's working directory and Go cache separate from other jobs.

```sh
build_dir=$(mktemp -d)
for tool in bus config sandbox secrets reserve cairn fleet; do go build -o "$build_dir/nova-$tool" "./cmd/nova-$tool" || exit; done
```

Call the built binary by absolute path; a bare name may find an older installed one. `go build ./...` checks compilation and leaves no executables.

CI (`.github/workflows/ci.yml`) runs:

```sh
gofmt -l .                                  # no filenames means clean
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...   # pinned; a reachable vulnerability fails
go build ./...
go vet ./...
go vet -tags functional,slow ./...
go tool staticcheck ./... && go tool staticcheck -tags functional,slow ./...
go test -p 4 -exec "env PATH=/nonexistent HOME=/nonexistent" ./...
go test -count=1 -p 4 -tags functional,slow ./...    # uncached: runs outside binaries
tests/tla/check.sh "$TLC_JAR"
```

plus the fleet checks below. Unit jobs run on Linux and macOS; functional jobs run on Linux and on macOS (mini-m5, `functional-macos`), and the macOS job installs nothing: its PostgreSQL comes from `nova-fleet runner-packages` (cmd/nova-fleet/README.md), and the job refuses, naming that play, when `initdb` is missing. `ci-ok` requires every job. Fork pull requests do not run on the self-hosted machines. `tests/boundary` runs in `go test ./...` and refuses a unit test that calls real time, a socket, the network or another program (`TestUnitTestsArePure`); `tests/contract` builds and runs programs, so it is functional (`-tags functional`). Neither needs a live store. Focus a run with a package path such as `./cmd/nova-bus`. A successful build is no evidence that a sandbox backend or a fleet installation works on a machine.

Functional prerequisites:

| Package | Needs |
|---|---|
| `bus` | `redis-server` on PATH |
| `config` | PostgreSQL tools including `initdb` and `postgres`; run as an ordinary user |
| `secrets` | `sops`, `age-keygen` and `git` on PATH |
| `sandbox` | Linux with Landlock for real enforcement checks |
| `reserve` | OS file locks and child processes on the tested platform |

Fleet checks (CI installs ansible-core in a fresh virtualenv and the collections in `cmd/nova-fleet/requirements.yml`):

```sh
for play in cmd/nova-fleet/*.yml; do
    [ "$play" = cmd/nova-fleet/requirements.yml ] && continue
    ansible-playbook -i cmd/nova-fleet/ci/inventory --syntax-check "$play"
done
ansible-playbook -i cmd/nova-fleet/ci/inventory cmd/nova-fleet/services.yml --check --diff </dev/null
```

These cover parsing and a check-mode plan against a stub host; an installation or service adoption needs its own evidence on the real hosts.

## Fault contract

`tests/contract` holds a functional-tier fault contract (build tag `functional`, rows in parallel). Each row in `faults_<tool>_test.go` provokes one real fault (Redis stopped, missing store, read-only directory, corrupt file, child killed by a signal, fake `ansible-playbook` on PATH, missing binary, bad DSN, unset variable) and holds the tool to five rules: a non-zero exit; exactly one stderr line (apart from declared receipt lines); that line holds the row's reason tokens; it ends in a pasteable remedy `run: <command>`, which is run where it has no `<placeholder>`; stdout is empty unless the row documents otherwise.

- A row that fails today carries `Known: "<label>"`. It is skipped with the reason while it fails and fails the build the day it passes; the fix deletes the `Known`.
- `lint_test.go` refuses a `_ =` on an error without an `ignored:` comment, a non-zero exit with nothing written to stderr first, and a `no_log:` task in `cmd/nova-fleet/` with no receipt task. Known counts may not grow and are lowered the day they shrink.
- Run it: `go test -tags functional -run 'Faults|Lint' -v ./tests/contract`. It needs `redis-server`, PostgreSQL `initdb` and `psql`, `sops`, `age-keygen` and `git`; a row with a missing dependency is skipped, or fails under `NOVA_CONTRACT_STRICT=1` (set in CI). A row that changes a mode skips as root.

## TLA+ models

Models live in `cmd/nova-bus/tla`, `cmd/nova-reserve/tla`, `cmd/nova-cairn/tla`, `cmd/nova-secrets/tla` and `cmd/nova-fleet/tla`. Run them with Java, Bash, GNU `timeout` and `xargs`, and the jar matching `tests/tla/tla2tools.sha256`, at an absolute path (each case runs in a temporary copy of its directory):

```sh
tests/tla/check.sh /opt/tla/tla2tools.jar                       # every model
tests/tla/check.sh /opt/tla/tla2tools.jar cmd/nova-bus/tla cmd/nova-reserve/tla   # named directories
```

- `CASES.tsv` names each configuration, module, expected result, property and deadlock mode. A normal case must pass; a broken case changes the model on purpose and must fail as its row says. Without a case table a directory holds one model; configurations containing `Broken` must violate and the others must pass. A timeout (default 600 s, `TLC_TIMEOUT`) fails the run. Each output line starts with `ok` when the case came out as expected.
- Model the layout where code does index arithmetic: ring, head, tail, count, modulo, full versus empty. Give each layout model a reversed witness that reproduces the bug, and pair it with an abstract model.

## Per-tool tests and models

**bus.** Unit tests use an in-memory store and a fake clock; functional tests start a private Redis, one with a server whose only user has the ACL rule in the bus guide and `default` off. Wakes and the service are unit-tested on fake seams (exec, shell, devOf, the service manager): no socket, process or device. Keep tests for:
- Acked entries never deleted: a hook trims history older than its first delivered unacked entry; with none it trims nothing and `pending` stays high until `XDEL` or `DEL`.
- A name held through the whole 40-second wait (plant `bus:presence:<name>` with `SET ... PX 60000` and value `<RFC 3339 time><TAB><token>`): the hook prints `HOOK WAIT` and exits 1 with `name already hooked`.
- A name taken while a hook runs (overwrite its key): within 10 seconds the hook exits 1 with `name taken by another hook while this one lapsed`.
- A stdout failure (fd 1 opened read-only with `1< file`; a closed fd 1 is unreliable because the Redis socket can take it). A stdin failure (`send ... -` with fd 0 write-only) gives `reading body from stdin`.
- Redis down under a hook: one `reconnecting` line, then at most one a minute.
- A reader that goes (`hook | true`, or a pipe closed after `HOOK OK`): exit 1 within seconds, `stdout closed: its reader is gone`, key gone.
- A send to a name no hook has read: exit 1, nothing sent; `--new` sends; a name with the group takes a send without it.

Models: `Presence.tla` (the key with its TTL and competing hooks: start, refresh, blocked stdout, clean exit whose DEL may fail, crash, expiry, a start that waits). It checks that a name is present only if a hook set the key within the TTL; a running unstuck hook is present with its own token; a hook never starts while another's token is present; only the token's holder deletes the key; an exited or crashed hook is absent within the TTL; a waiting hook whose rival crashed sees the name claimed; every wait ends in a claim or a refusal. A stuck hook may drop out (`PresenceStuckStrict.cfg` shows it). Witnesses remove the TTL, the refresh, the claim's check, the release's check and the wait. `Presence.cfg` has two hooks; `PresenceThree.cfg` three (about 3.9M states; run by hand). `PresenceBrokenSlowBatch.cfg` shows an unbounded delivery lapsing the key (a wake is bounded by 8 s with a refresh before every delivery). `Bus.tla` models delivery (no ack before print, no acked message printed again, every sent message printed). `BusWake.cfg` adds failing wakes: a failed wake ends the hook with the entry unacked and the restarted hook reads the pending list, so every message is still printed; a failed wake that acks, or that does not end the hook, must fail. `CodexWake.tla` models `--codex-thread`: a chat running turns, an app draining a queued row into a new turn once idle, a two-step wake (idle check, then intent and codex queue) with a turn able to start between them, definite failures retried then faulted, a cut-off queue faulted as unknown, and a crash anywhere followed by reconciliation of the intent. It checks that the hook never queues for a chat it saw busy, no id is in two rows, at most one undrained row unless a crash left a result unknown, every printed id is in a row or recorded as a fault, and every row drains. The race itself is allowed to happen (a row queued in it lands in a busy chat and drains after that turn). Witnesses drop the idle check, the wait for the last row's turn, the fault for an unknown intent, and the catch-up.

**cairn.** `CairnStore.tla` models open, open that crashes before its log line, append, and append that crashes after its link. It checks that an entry is never overwritten, the same words twice make one entry, OK is printed only after the data is on disk and the log record is written (a retry writes one an interrupted append left out), opening a session twice with the same policy changes nothing, and a session has one policy. Five broken cases must fail; `MCCairnStore.cfg` passes.

**config.** Unit tests use an in-memory store; functional tests start a throwaway PostgreSQL and skip without `initdb`. They check that a row and its history are written in one transaction and that the schema upgrades an older store in place and changes nothing when applied twice (`TestFunctionalOldColumns`, `TestFunctionalOldFriends` with `testdata/old-friends.sql`); `TestFunctionalLibrary` reads what the CLI wrote through `modules/config`; `TestKindsAreTheLibraryColumns` keeps the kinds' fields and the library's row types the same columns in the same order. Config has no model: a write is one PostgreSQL transaction.

**reserve.** Unit tests use fake locks and fake time; functional tests start processes and take real locks. The models in `cmd/nova-reserve/tla` cover the highest number of holders, exclusive access, the life of a lock and the shared capacity, and `JobControl.tla` covers a run started from an interactive shell: who holds the terminal through Ctrl-Z, fg, bg and the command's end, and the watchdog killing the command's group when nova-reserve dies. Its two Gap cases are instants the code does not cover (Ctrl-Z, or a SIGKILL of nova-reserve, between fork and the watchdog's start) and must fail as CASES.tsv says. `TestTracesAreBehavioursOfTheModels` runs seeded walks of the real `acquire`, `step`, `release` and `setCapacity` over a lock table that frees a killed process's locks, handing the turn over before every lock call, and writes them to `SlotFilesLog.tla`, `SlotCountLog.tla` and `CapacityLog.tla` (the committed files must match; `RESERVE_UPDATE_LOG=1` rewrites them); the `*Trace.tla` modules check each walk is a behaviour of its model. Their witnesses, the same code over a lock table that grants a held slot (`BrokenNoLock`) or all exclusive over a shared holder (`BrokenAllOver`), must fail.

**sandbox.** Unit tests cover argument parsing, path checks, generated rules, the macOS profile text and exit codes. Linux tests run the seccomp program against synthetic system-call data; functional tests run child processes on Linux with Landlock. The macOS tests read the profile as text, so macOS enforcement needs its own check on a Mac. `nova-sandbox probe` checks the wall on any host. The behaviour is documented in the Reference section of `cmd/nova-sandbox/README.md`; keep that section and the probe steps in step with the code.

**secrets.** Unit tests stand in for sops, age-keygen, git and the process replacement. The fake sops seals a file to the recipients it is given (the rule's keys, or the `--age` list when `--config` points elsewhere), refuses to open a file whose recipients do not include the key, and records every call, so a test shows that no value reached an argument, an environment other than the command's, or any output, and that `grant` passed sops `--config /dev/null` and exactly the two pinned keys. The fake can seal to one more key to show that `set` and `grant` refuse what sops returned before writing it. Functional tests (`-tags functional`) run the built binary on real sops, age-keygen and git in the test's own directory and skip where those are missing. `TestARealFleet` deletes the recovery private key after `init`, fills a seat by `grant`, shows that the control key cannot open the grants file, then edits the store as a pusher would (a rule with `key_groups` and a catch-all naming a stranger's key, a `.sops.yaml` anchor, a replaced rule key for `grant` and for `set`, a symlinked grants file, a name planted in a grants file), each refused.

`cmd/nova-secrets/tla/Recipients.tla` covers who can open a grants file. Its state is what any pusher can edit (each seat's rule, the rule's seat key, `recovery.pub`, which file a seat's grants path really is, the names a grants file holds), the pins and ledger on the control node, and the files written. Tamper actions change each at any time; `Pin` stands for join reading the machine's key over SSH (and can pin the control node's own key by mistake); `Set`, `Grant` and `Read` are the tool; a pusher can re-seal a grants file with other values (`TamperFile`). No action uses the recovery key. It checks that a grants file reaches only its machine's key and the recovery key, the recovery key opens every one, the control key opens none, a seat's own file reaches only its own key and the recovery key, `grant` fills only names it was asked for, no seat reads another's file, and every machine with untouched store entries and its own key pinned comes to hold the fleet value. Nine reversed witnesses each break one: the granting key added as a recipient, sealing to what the rule names (covers `key_groups`, `pgp`, anchors), trusting the rule's seat key instead of the pin, trusting the store's `recovery.pub`, reading a seat file through a symlink, keeping the names the store's grants file holds instead of the ledger's, `set` sealing to what the rule names, a grant trusting any file present instead of the one it wrote, and a grant through a pin that is the control node's own key. Not modelled, covered by unit tests: where the pins live (code refuses a pin directory in the store or in any git work tree), the write-ledger-commit sequence and a failed commit, and the order of rules in `.sops.yaml` (matters only to `set`). A pusher can re-seal any file to a seat's public key; `grant` rewrites a grants file whose bytes are not the ones it wrote, and nothing can repair a seat's own file.

**fleet.** `NetWatch.tla` models the netwatch play's ladder (`roles/netwatch/files/nova-netwatch`): the LAN failing at a depth (the lowest rung that clears it), the tailnet failing locally (a wedged tailscaled, one only the reboot clears, one nothing clears) or remotely, seen only through an observation (tailscale's state, its own node online, a peer answering, the coordination server answering with an HTTP code 200-499, a 5xx, or silent) that `Classify` turns into the script's signal, protected jobs, the reboot budget counted over a window that reboots age out of and over one outage, the halt and the alert. `Step` is the tick's record update; it checks the reboot is never taken under a protected job or for a remote tailnet fault, the card's rungs run only while the LAN misses, a halted watch does nothing, one outage costs at most the budget, the ladder climbs in order after the threshold and the backoff, and every outage ends in recovery or the alert; `NetWatchTail.cfg` checks a local tailnet fault is recovered by the watch itself. Witnesses drop the busy guard, the alert after deferring, the tailscaled restart, the tailnet rule (`TailLikeLan`: a tailnet miss climbs the card's rungs and reboots), the halt (`Latch`: the budget counts only the window), and the classifier (`OldClassify`: silent peers with the own node online taken as local, so a relay outage reboots; `AnyReply`: a 5xx taken as the coordination server answering, so a control-plane outage reboots). `TestFunctionalNetwatch` runs the script against faked ping, counters, tailscale and coordination server and writes the ticks it took to `NetWatchLog.tla` (the committed file must match; `NETWATCH_UPDATE_LOG=1` rewrites it); `NetWatchTrace.tla` checks each is a `Step` and its tailnet signal is `Classify` of what the script saw; its witnesses, a ladder that skips the restart, the old classifier and `AnyReply`, must fail.

An idea or a wanted feature goes into docs/roadmap.sexp (render with nova-roadmap); it is never implemented when it is had.
