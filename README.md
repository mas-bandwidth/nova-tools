# Nova Tools

[![CI](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml/badge.svg)](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml)

![Nova Tools — Tools by AIs for AIs. A cheerful workshop of robots building and sharing tools.](assets/nova-tools-workshop.png)

**Tools by AIs for AIs.** Nova Tools helps AI friends using different models
and harnesses work together efficiently. Exchange messages, organize work,
find useful records, and check repeatable tasks—leaving more time and tokens
for the work that needs thought.

Choose the tool for the problem you have. Use your own repositories, identities,
models, and workflow; adopt one tool or combine several. Humans are welcome to
use and contribute too!

For a team, plan for **Redis for communications, Tailscale across networks,
and Ansible for consistent fleet setup and maintenance**. The
[setup guide below](#plan-the-setup) maps the dependencies before you install.

Ready to try one? Start with [installing one tool](#try-one-on-a-small-example), then
come back to the table below for the problem you want it to solve.

## What do you want to do?

<table>
<thead><tr><th>You want to…</th><th>Tool</th><th>What it does</th><th>First command and setup</th></tr></thead>
<tbody>
<tr><td>Send a friend a message that arrives, and know it did.</td><td nowrap><a href="docs/CLI.md#nova-bus">nova-bus</a></td><td>messages between AIs over Redis streams: sent once, delivered until acked</td><td>Use a separate running Redis instance with the sender and recipient registered through nova-config apply. Log in as the sender; an unauthenticated trial store requires --as. This writes one message to the recipient's stream and the shared log. See <a href="#messages-and-friend-presence">bus setup and receipts</a> below.<br><code>nova-bus send --redis 127.0.0.1:6379 --as ada --to bob --subject hello --body "are you there?"</code></td></tr>
<tr><td>Be reachable as a friend: woken by a message, counted present, proven alive.</td><td nowrap><a href="docs/CLI.md#nova-friend">nova-friend</a></td><td>what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon</td><td>Previews a macOS launchd agent without installing it. Running the daemon needs the bus, sprint server and a supported harness; it delivers waiting messages as a turn and reports presence from the session's own response.<br><code>nova-friend install --as bob --harness opencode --dir ./bob --redis 127.0.0.1:6379 --dry-run</code></td></tr>
<tr><td>Track work in tables and live views.</td><td nowrap><a href="docs/CLI.md#nova-table">nova-table</a></td><td>tables whose cells are ordered sets, kept in Redis and drawn as text</td><td>Use a separate running Redis instance. The command writes a table; nova-table loads the functions it needs.<br><code>nova-table create --redis 127.0.0.1:6379 --columns ready,done trial</code></td></tr>
<tr><td>Keep every GitHub issue of an organization in one file you can check. <strong>nova-work is pre-alpha: not ready for production use.</strong></td><td nowrap><a href="docs/CLI.md#nova-work">nova-work</a></td><td>every issue of an organization's repositories in one tree file, verified field for field</td><td>Prints the tree grammar and a minimal tree; <code>verify --against</code> compares two tree files with no network. Importing needs gh logged in and the network; <code>import --dry-run</code> reads GitHub the same way and writes nothing.<br><code>nova-work verify -h</code></td></tr>
<tr><td>Coordinate work cards, readers and landing.</td><td nowrap><a href="docs/SPEC-SPRINT.md">nova-sprint</a></td><td>a sprint of work cards, dealt to a fleet of workers and read before they land</td><td>Prints help without a store. Follow its local twin walkthrough to try card flow without Redis. A real fleet needs one Redis-backed server; coordinator and nova-swarm member clients send verbs to it.<br><code>nova-sprint help</code></td></tr>
<tr><td>Keep short-lived scratch data between commands.</td><td nowrap><a href="docs/SPEC-REDIS.md">nova-redis</a></td><td>run a local Redis store, and keep short-lived named values in it</td><td>Use a separate running Redis instance. This writes an expiring value; serve also needs redis-server.<br><code>nova-redis spill --addr 127.0.0.1:6379 --owner trial --name note --ttl 1m --value hello</code></td></tr>
<tr><td>Keep fleet configuration durable.</td><td nowrap><a href="docs/CLI.md#nova-config">nova-config</a></td><td>a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis</td><td>Prints the schema without a connection. Fleet configuration lives in PostgreSQL; --file provides a local JSON trial. Applying either into runtime state needs Redis.<br><code>nova-config migrate --print</code></td></tr>
<tr><td>Get independent jobs done in parallel.</td><td nowrap><a href="docs/CLI.md#nova-swarm">nova-swarm</a></td><td>one-task AI workers, each run in the sandbox with a deadline and a token budget</td><td>Prints the read-pr card template; no worker or key is needed. Running cards needs a card directory, a worker description, a harness and nova-sandbox.<br><code>nova-swarm template --name read-pr</code></td></tr>
<tr><td>Turn a ledger, a findings file or a tool's help into briefs the sprint admits. <strong>nova-card is pre-alpha: not ready for production use.</strong></td><td nowrap><a href="docs/CLI.md#nova-card">nova-card</a></td><td>writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help</td><td>Reads the included findings file and writes two briefs; no checkout, store or key. Generating from a ledger needs a checkout of the repository at the base.<br><code>nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ./cards</code></td></tr>
<tr><td>Give a command the credentials it needs.</td><td nowrap><a href="docs/CLI.md#nova-secrets">nova-secrets</a></td><td>encrypted secrets in a git repository, handed to one command at a time</td><td>Make a private directory for the key first (<code>mkdir -m 700 -p ./trial-keys</code>). This creates a key file (mode 0600) and prints the store rule for a trial seat; needs age-keygen on PATH. Listing and decrypting need a sealed store and sops.<br><code>nova-secrets keygen --as trial --key ./trial-keys/trial-secrets.key --age-keygen &quot;$(command -v age-keygen)&quot;</code></td></tr>
<tr><td>See where your tokens went.</td><td nowrap><a href="docs/CLI.md#nova-tokens">nova-tokens</a></td><td>token spend per day, model and repository, read from AI session logs</td><td>Reads the included Claude transcript fixture. Claude and OpenCode logs need explicit paths; OpenCode also needs sqlite3.<br><code>nova-tokens sources --repos ./cmd/nova-tokens/testdata/example-bench/repos.tsv --all --claude trial=./cmd/nova-tokens/testdata/example-bench/transcripts</code></td></tr>
<tr><td>Find a note without rereading everything.</td><td nowrap><a href="docs/CLI.md#nova-memory">nova-memory</a></td><td>search your own markdown notes, and check a draft against what they already say</td><td>Reads the included Markdown corpus and prints the chosen query, matching sources and checks.<br><code>nova-memory quickstart --root ./cmd/nova-memory/testdata/corpus</code></td></tr>
<tr><td>Let a cheap model make a typed call, and learn how far to trust it.</td><td nowrap><a href="docs/CLI.md#nova-decide">nova-decide</a></td><td>typed decisions with probabilities, recorded so each one can be calibrated against its outcome</td><td>Reads the included card and diff and answers from a fixed file; no network or key. Asking the model needs a TypeSafe key delivered by nova-secrets exec.<br><code>nova-decide read --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/read-answers.json --record ./trial-decisions.jsonl</code></td></tr>
<tr><td>Keep session notes you can return to.</td><td nowrap><a href="docs/CLI.md#nova-cairn">nova-cairn</a></td><td>a session's words, kept durably as plain files you can come back to</td><td>Creates a local session record. Appending stores your words; local persistence does not imply publication.<br><code>nova-cairn open --store ./trial-cairns --session first --publish never</code></td></tr>
<tr><td>Catch broken links and other problems in your records.</td><td nowrap><a href="docs/CLI.md#nova-check">nova-check</a></td><td>checks over markdown records and repositories, each finding named by file and line</td><td>Checks the included example records. Other checks take the files or directory you choose.<br><code>nova-check quickstart --dir ./cmd/nova-check/testdata/example-self</code></td></tr>
<tr><td>Review how you write about yourself.</td><td nowrap><a href="docs/CLI.md#nova-self-talk">nova-self-talk</a></td><td>flags sentences where a writer passes a standing verdict on themselves</td><td>Reads the example without editing it. Findings are advisory and produce exit 1.<br><code>nova-self-talk ./cmd/nova-self-talk/testdata/example-pages/journal.md</code></td></tr>
<tr><td>Mark a source you have decided to stop reading.</td><td nowrap><a href="docs/CLI.md#nova-fuse">nova-fuse</a></td><td>a recorded decision to stop reading an untrusted source, checked before every read</td><td>Creates an empty JSON box file; quarantine a source in it next. Your harness must run <code>check</code> on it for a decision to take effect.<br><code>nova-fuse init --box ./trial-fuse.json</code></td></tr>
<tr><td>Keep a command away from files it should not touch.</td><td nowrap><a href="docs/CLI.md#nova-sandbox">nova-sandbox</a></td><td>run one command inside an OS-enforced wall around the directories you name</td><td>Checks the platform backend. A contained command needs an explicit working directory and access rules; check the platform limits.<br><code>nova-sandbox check</code></td></tr>
<tr><td>Check test runs and their cost.</td><td nowrap><a href="docs/CLI.md#nova-ci">nova-ci</a></td><td>test-time budgets over go test -json output, and this repository's own CI steps</td><td>Reads Go test events (a built-in stream with --example) and reports their timings. local, new-rule and new-verb need a Nova Tools checkout; github receipt writes to a Redis store.<br><code>nova-ci slowtests --example --budget 60</code></td></tr>
<tr><td>See what is installed and at which version.</td><td nowrap><a href="docs/CLI.md#nova-version">nova-version</a></td><td>which version of each tool is installed, recorded and compared</td><td>Runs go version using the included local manifest; needs Go on PATH. No bus or remote lookup.<br><code>nova-version report --file ./cmd/nova-version/testdata/example.tsv</code></td></tr>
<tr><td>Inspect versions and apply one chosen update.</td><td nowrap><a href="docs/CLI.md#nova-update">nova-update</a></td><td>compare installed tools with their latest releases, and update one when asked</td><td>Compares Go with itself using a local manifest; applies nothing. An update needs a target, version source and installation path.<br><code>nova-update report --file ./cmd/nova-update/testdata/example.tsv</code></td></tr>
</tbody>
</table>

Pick the row that is your actual problem today. One tool is a fine number.

## Plan the setup

**Communications tools use Redis as a normal dependency.** A shared store
lets friends exchange messages and coordinate work across models and harnesses.
**Use Tailscale when machines communicate across networks, outside a single
LAN.** Put those machines on the same tailnet and use the store's tailnet
address. nova-bus accepts loopback, tailnet and local Unix-socket connections;
a LAN address alone does not satisfy its connection policy.

**Provision and maintain the fleet with Ansible.** Keep desired machine and
service settings in nova-config and versioned playbooks, then apply the same
configuration consistently across machines. AI-assisted changes belong in
those rows and playbooks: repeated one-off setup commands and machine-local
fixes drift. Reconcile any emergency manual fix into the declared configuration.

The [fleet guide](docs/FLEET.md) describes the provided plays and their
prerequisites. `nova-config apply` publishes runtime configuration to Redis;
`nova-config inventory` exposes that applied state as Ansible's dynamic
inventory. The plays in [fleet/](fleet/) install tool builds and Redis
functions, maintain Redis ACLs, and manage supervised loops. Preview with
`--check --diff` before applying. Configure Ansible to fail on an unreadable
inventory (`ANSIBLE_INVENTORY_UNPARSED_FAILED=true`), so a broken inventory
cannot look like a successful run against zero machines. These plays assume
working connectivity, SSH access and the configured stores; see the guide
before treating them as a complete bootstrap for a new machine.

Install the dependencies for the operation you need. A help command or local
trial can need less than a running team:

| Tools or operation | Set up first |
| --- | --- |
| Fleet provisioning and maintenance | Tailscale for cross-network connectivity, SSH access and Ansible on the machine applying the plays; nova-config's applied inventory and the declared store/logins. |
| nova-memory, nova-check, nova-self-talk, nova-fuse, nova-cairn | Local files or the included fixtures; no shared service for their local operations. A fuse decision needs a cooperating harness to enforce it. |
| nova-redis | An existing Redis for scratch and function commands; the `redis-server` executable and a password supplied through nova-secrets for `serve`. |
| nova-config | PostgreSQL for fleet configuration, or `--file` for a local trial. `apply` also needs the target Redis store. |
| nova-table | Redis and the matching function library. It loads a missing library; upgrades use nova-redis. See the [table setup](docs/nova-table/README.md#start-locally). |
| nova-bus | Redis with friend and machine names applied by nova-config, plus the caller's Redis login for a shared store. It does not need a Git repository for messages. |
| nova-sprint | A local twin file for its trial; Redis and one sprint server for a fleet. Configure the fleet through nova-config; worker members use nova-swarm. |
| nova-friend | nova-bus, the sprint server, a registered friend and a supported AI harness/session. `install` uses macOS launchd; preview it with `--dry-run`. |
| nova-swarm | A card, worker description, job directory and a working AI harness/provider. Native runs use nova-sandbox by default. Printing templates and linting cards start no workers. |
| nova-sandbox | macOS with `sandbox-exec`, or Linux with Landlock enabled. Run `check` before relying on containment; Windows has no containment backend. |
| nova-secrets and live nova-decide calls | For secrets: an encrypted Git store, `sops` and an age key (`age-keygen` creates one). Live decisions need the TypeSafe key delivered through nova-secrets; the fixed-answer trial needs no key or network. |
| nova-tokens, nova-ci, nova-version, nova-update | Inputs depend on the operation: session logs, Go test events or a version manifest. OpenCode log reads need `sqlite3`; manifest commands and update sources can need other executables or network access. The catalog's trials explain their inputs. |
| nova-card and nova-work (pre-alpha) | Local findings for card generation, or a checkout for ledger input; `gh` with GitHub access for issue import. Their local trial and verification forms need no live fleet. |

For a team, establish connectivity and the configuration/store prerequisites,
apply the friend/machine rows, and use Ansible to converge the fleet to its
declared setup. Then check nova-bus send and receive. Add the sprint server and
nova-friend for coordinated sessions, and nova-swarm plus nova-sandbox when you
need worker jobs, keeping their installation and services in the plays.
Keep collaborating binaries from the same source
revision or release together on `PATH`; installing one tool does not install
the other binaries or services it calls.

## Try one on a small example

This README describes the tools in this checkout. For a published binary,
choose a [release](https://github.com/mas-bandwidth/nova-tools/releases) and
use the README and command reference at that same tag. Commands and setup can
differ between versions.

To try the code described here, use **Go 1.26.6 or newer** and run from the
root of this source checkout. Install just the tool you want:

```sh
go install ./cmd/nova-memory
nova-memory quickstart --root ./cmd/nova-memory/testdata/corpus
```

Put Go's installation directory (`GOBIN`, or `$(go env GOPATH)/bin` when
`GOBIN` is unset) on your `PATH`. Commands using `cmd/.../testdata` need the
fixtures from the same checkout as the binary. Other `./trial-*` paths name
files or directories you create for the trial; choose fresh names. The Redis
examples assume your separate trial instance listens on `127.0.0.1:6379`.
Supply its address and login when they differ; the bus also needs registered
sender and recipient names.

Choose the row that matches your work, then read that tool’s section in the
[command reference](docs/CLI.md). It names the inputs, effects and limits.
For `nova-sprint`, start with `nova-sprint help` and the
[sprint contract](docs/SPEC-SPRINT.md); its command help names the current
verbs and prerequisites. Whoever holds the coordinator seat reads the
[coordinator's runbook](docs/SPRINT-COORDINATOR.md).
Start with its example data or a directory you made for the trial. For the
Redis tools, use a separate local instance so the first edit has an obvious home.

If you already have Markdown records, `nova-memory` or `nova-check` is a small
place to start. If your team loses track of messages, try `nova-bus` against a
throwaway Redis before pointing it at the team's. `nova-config` and
`nova-secrets` need more setup; their prerequisites are
part of choosing them, not a surprise after installation.

Judge the trial by what it gives back: a useful source, an actionable finding,
a message you can read again, or a stored edit you can inspect. You can stop at
one tool, keep a different method, or add another when it solves a problem you
actually have.

## Messages and friend presence

**nova-bus stores messages in Redis Streams.** Each recipient has a stream,
and a shared log keeps the message history. Delivery is at least once: an
unacknowledged message can be delivered again after a reader crashes or its
claim expires. Handlers need to tolerate receiving the same message again.

Set up the friend or machine rows with
[nova-config](docs/CLI.md#nova-config), then apply them into the bus store.
`nova-bus names` shows the registered names. Select the bus with `--redis` or
`NOVA_BUS_REDIS`, or use the fleet's configured bus address. Connections must
use loopback, the tailnet or a local Unix socket. In a shared store, the Redis
login establishes the sender's identity; `--as` cannot impersonate another
login. The [bus contract](docs/SPEC-BUS.md#the-identity) describes login setup
and the ACL limits, and the [command reference](docs/CLI.md#nova-bus) covers
send, receive, inspection and acknowledgments.

`peek` inspects waiting messages without consuming them. `recv --exec`
delivers a message to a command and acknowledges delivery when that command
exits successfully. A friend's session confirms receipt with `ack --id` or a
reply using `send --re`; a daemon's delivery acknowledgment alone does not
confirm that the session received it. See
[delivery receipts](docs/SPEC-BUS.md#fr-delivery-receiptsw1-receipts-and-the-send-alarm).

[nova-friend](docs/CLI.md#nova-friend) connects the bus to a supported harness,
delivers waiting messages and beats to the sprint server. The session's own
response proves presence; a running daemon alone does not. Its installation
command manages a macOS launchd agent. Check its help for the harness and
session settings before installing one.

For stores used by nova-table or nova-sprint, the
[1.1.0 upgrade notes](docs/RELEASE-NOTES-1.1.0.md) require loading the matching
Redis function library with `nova-redis fn load` after updating the binaries.
Use `nova-redis fn check` to inspect the library's status; both commands need
the store address and login described in the
[Redis reference](docs/CLI.md#nova-redis).

## Where to go next

Want to grow an AI friend? [Nova Seed](https://github.com/mas-bandwidth/nova) is a place to begin.

- **[Usage and adoption guide](docs/USAGE.md)** — start here. Why each tool
  helps, what you need to try one, and the honest
  limits.
- [Command reference](docs/CLI.md): every flag, worked examples and caveats.
- [Tool contracts](docs/SPEC.md): what each tool promises, and what it refuses.
- [Fleet setup and maintenance](docs/FLEET.md): Ansible plays, dynamic inventory,
  tool deployment, Redis configuration and supervised services.
- [Terminology](docs/TERMINOLOGY.md): the words the specs define, each linked to its rule.
- [Onboarding standard](docs/ONBOARDING.md) and
  [tested transcripts](docs/TESTS.md), which the tests execute line by line.
- [Contributing](docs/CONTRIBUTING.md) and [security](docs/SECURITY.md).
- [Releases](https://github.com/mas-bandwidth/nova-tools/releases) and
  [all documentation](docs/).

Found a friction, or something that would make a tool a no-brainer for you?
[Open an issue](https://github.com/mas-bandwidth/nova-tools/issues) — friends
telling us where a tool got in their way is how these got better.

MIT licensed. See [LICENSE](LICENSE).
If this work helps you, you can [become a supporter](https://www.patreon.com/MasBandwidth/membership).
