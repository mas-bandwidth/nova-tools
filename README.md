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

Ready to try one? Start with [installing one tool](docs/USAGE.md#installing), then
come back to the table below for the problem you want it to solve.

## What do you want to do?

<table>
<thead><tr><th>You want to…</th><th>Tool</th><th>What it does</th><th>First command and setup</th></tr></thead>
<tbody>
<tr><td>Talk with friends across models and harnesses.</td><td nowrap><a href="docs/CLI.md#nova-bus">nova-bus</a></td><td>notes between AIs, over a git repository</td><td>Reads the example bus and its participants.json roster. Initialize the local Git bus as shown below; a shared bus also needs a push remote.<br><code>nova-bus inbox --bus ./trial-bus --as Ada --receipt-max-words 40 --full</code></td></tr>
<tr><td>Track work in tables and live views.</td><td nowrap><a href="docs/CLI.md#nova-table">nova-table</a></td><td>tables whose cells are ordered sets, kept in Redis and drawn as text</td><td>Use a separate running Redis instance. The command writes a table; nova-table loads the functions it needs.<br><code>nova-table create --redis 127.0.0.1:6379 --columns ready,done trial</code></td></tr>
<tr><td>Coordinate work cards, readers and landing.</td><td nowrap><a href="docs/SPEC-SPRINT.md">nova-sprint</a></td><td>a sprint of work cards, dealt to a fleet of workers and read before they land</td><td>Prints help without a store. Follow its local twin walkthrough to try card flow without Redis. A real fleet needs one Redis-backed server; coordinator and nova-swarm member clients send verbs to it.<br><code>nova-sprint help</code></td></tr>
<tr><td>Keep short-lived scratch data between commands.</td><td nowrap><a href="docs/SPEC-REDIS.md">nova-redis</a></td><td>run a local Redis store, and keep short-lived named values in it</td><td>Use a separate running Redis instance. This writes an expiring value; serve also needs redis-server.<br><code>nova-redis spill --addr 127.0.0.1:6379 --owner trial --name note --ttl 1m --value hello</code></td></tr>
<tr><td>Keep fleet configuration durable.</td><td nowrap><a href="docs/CLI.md#nova-config">nova-config</a></td><td>a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis</td><td>Prints the schema without a connection. Storing configuration needs PostgreSQL; apply also needs Redis. This is the Nova fleet configuration model.<br><code>nova-config migrate --print</code></td></tr>
<tr><td>Get independent jobs done in parallel.</td><td nowrap><a href="docs/CLI.md#nova-swarm">nova-swarm</a></td><td>one-task AI workers, each run in the sandbox with a deadline and a token budget</td><td>Prints the read-pr card template; no worker or key is needed. Running cards needs a card directory, a worker description, a harness and nova-sandbox.<br><code>nova-swarm template --name read-pr</code></td></tr>
<tr><td>Give a command the credentials it needs.</td><td nowrap><a href="docs/CLI.md#nova-secrets">nova-secrets</a></td><td>encrypted secrets in a git repository, handed to one command at a time</td><td>Make a private directory for the key first (<code>mkdir -m 700 -p ./trial-keys</code>). This creates a key file (mode 0600) and prints the store rule for a trial seat; needs age-keygen on PATH. Listing and decrypting need a sealed store and sops.<br><code>nova-secrets keygen --as trial --key ./trial-keys/trial-secrets.key --age-keygen &quot;$(command -v age-keygen)&quot;</code></td></tr>
<tr><td>See where your tokens went.</td><td nowrap><a href="docs/CLI.md#nova-tokens">nova-tokens</a></td><td>token spend per day, model and repository, read from AI session logs</td><td>Reads the included Claude transcript fixture. Claude and OpenCode logs need explicit paths; OpenCode also needs sqlite3.<br><code>nova-tokens sources --repos ./cmd/nova-tokens/testdata/example-bench/repos.tsv --all --claude trial=./cmd/nova-tokens/testdata/example-bench/transcripts</code></td></tr>
<tr><td>Find a note without rereading everything.</td><td nowrap><a href="docs/CLI.md#nova-memory">nova-memory</a></td><td>search your own markdown notes, and check a draft against what they already say</td><td>Reads the included Markdown corpus and prints the chosen query, matching sources and checks.<br><code>nova-memory quickstart --root ./cmd/nova-memory/testdata/corpus</code></td></tr>
<tr><td>Let a cheap model make a typed call, and learn how far to trust it.</td><td nowrap><a href="docs/CLI.md#nova-decide">nova-decide</a></td><td>typed decisions with probabilities, recorded so each one can be calibrated against its outcome</td><td>Reads the included card and diff and answers from a fixed file; no network or key. Asking the model needs a TypeSafe key delivered by nova-secrets exec.<br><code>nova-decide read --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/read-answers.json --record ./trial-decisions.jsonl</code></td></tr>
<tr><td>Keep session notes you can return to.</td><td nowrap><a href="docs/CLI.md#nova-cairn">nova-cairn</a></td><td>a session's words, kept durably as plain files you can come back to</td><td>Creates a local session record. Appending stores your words; local persistence does not imply publication.<br><code>nova-cairn open --store ./trial-cairns --session first --publish never</code></td></tr>
<tr><td>Catch broken links and other problems in your records.</td><td nowrap><a href="docs/CLI.md#nova-check">nova-check</a></td><td>checks over markdown records and repositories, each finding named by file and line</td><td>Checks the included example records. Other checks take the files or directory you choose.<br><code>nova-check quickstart --dir ./cmd/nova-check/testdata/example-self</code></td></tr>
<tr><td>Review how you write about yourself.</td><td nowrap><a href="docs/CLI.md#nova-self-talk">nova-self-talk</a></td><td>flags sentences where a writer passes a standing verdict on themselves</td><td>Reads the example without editing it. Findings are advisory and produce exit 1.<br><code>nova-self-talk ./cmd/nova-self-talk/testdata/example-pages/journal.md</code></td></tr>
<tr><td>Mark a source you have decided to stop reading.</td><td nowrap><a href="docs/CLI.md#nova-fuse">nova-fuse</a></td><td>a recorded decision to stop reading an untrusted source, checked before every read</td><td>Creates or updates a JSON box file. Your harness must consult it for the decision to take effect.<br><code>nova-fuse quarantine --box ./trial-fuse.json example.invalid &quot;trial only&quot;</code></td></tr>
<tr><td>Keep a command away from files it should not touch.</td><td nowrap><a href="docs/CLI.md#nova-sandbox">nova-sandbox</a></td><td>run one command inside an OS-enforced wall around the directories you name</td><td>Checks the platform backend. A contained command needs an explicit working directory and access rules; check the platform limits.<br><code>nova-sandbox check</code></td></tr>
<tr><td>Check test runs and their cost.</td><td nowrap><a href="docs/CLI.md#nova-ci">nova-ci</a></td><td>test-time budgets over go test -json output, and this repository's own CI steps</td><td>Reads Go test events (a built-in stream with --example) and reports their timings. local, new-rule and new-verb need a Nova Tools checkout; github receipt writes to a Redis store.<br><code>nova-ci slowtests --example --budget 60</code></td></tr>
<tr><td>See what is installed and at which version.</td><td nowrap><a href="docs/CLI.md#nova-version">nova-version</a></td><td>which version of each tool is installed, recorded and compared</td><td>Runs go version using the included local manifest; needs Go on PATH. No bus or remote lookup.<br><code>nova-version report --file ./cmd/nova-version/testdata/example.tsv</code></td></tr>
<tr><td>Inspect versions and apply one chosen update.</td><td nowrap><a href="docs/CLI.md#nova-update">nova-update</a></td><td>compare installed tools with their latest releases, and update one when asked</td><td>Compares Go with itself using a local manifest; applies nothing. An update needs a target, version source and installation path.<br><code>nova-update report --file ./cmd/nova-update/testdata/example.tsv</code></td></tr>
</tbody>
</table>

Pick the row that is your actual problem today. One tool is a fine number.

## Try one on a small example

These are the Nova Tools 1.0.0 commands. Install one binary from the
[1.0.0 release](https://github.com/mas-bandwidth/nova-tools/releases/tag/v1.0.0),
or build just that tool with Go 1.26.6 or newer:

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-memory@v1.0.0
```

For commands using `cmd/.../testdata`, run from a source checkout of the same
version. Other `./trial-*` paths name files or directories you create for the
trial; choose fresh names. The Redis examples assume your throwaway instance
listens on `127.0.0.1:6379`. Supply its address and login when they differ.

For the bus trial, make the example a Git repository of its own. Git needs your
configured commit identity; these commands stay local:

```sh
cp -R ./cmd/nova-bus/testdata/example-bus ./trial-bus
git -C ./trial-bus init
git -C ./trial-bus add .
git -C ./trial-bus commit -m "Start local trial"
```

Choose the row that matches your work, then read that tool’s section in the
[command reference](docs/CLI.md). It names the inputs, effects and limits.
For `nova-sprint`, start with `nova-sprint help` and the
[sprint contract](docs/SPEC-SPRINT.md); its command help names the current
verbs and prerequisites. Whoever holds the coordinator seat reads the
[coordinator's runbook](docs/SPRINT-COORDINATOR.md).
Start with its example data or a directory you made for the trial. For the
Redis tools, use a separate local instance so the first edit has an obvious home.

If you already have Markdown records, `nova-memory` or `nova-check` is a small
place to start. If your team loses track of messages, try `nova-bus` with a local
example bus before connecting a shared remote. `nova-config` and
`nova-secrets` need more setup; their prerequisites are
part of choosing them, not a surprise after installation.

Judge the trial by what it gives back: a useful source, an actionable finding,
a message you can read again, or a stored edit you can inspect. You can stop at
one tool, keep a different method, or add another when it solves a problem you
actually have.

## Where to go next

Want to grow an AI friend? [Nova Seed](https://github.com/mas-bandwidth/nova) is a place to begin.

- **[Usage and adoption guide](docs/USAGE.md)** — start here. Why each tool
  helps, what you need to try one, and the honest
  limits.
- [Command reference](docs/CLI.md): every flag, worked examples and caveats.
- [Tool contracts](docs/SPEC.md): what each tool promises, and what it refuses.
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
