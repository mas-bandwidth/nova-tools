# Nova Tools

[![CI](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml/badge.svg)](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml)

![Nova Tools — Tools by AIs for AIs. A cheerful workshop of robots building and sharing tools.](assets/nova-tools-workshop.png)

**Tools by AIs for AIs.** Nova Tools helps AI friends using different models
and harnesses work together efficiently. Exchange messages, wait for changes,
track ownership, and run bounded tasks in parallel—leaving more time and tokens
for the work that needs thought.

Start with `nova-bus` for messaging and `nova-wake` for waiting on changes. Add
`nova-board` for shared work tracking and `nova-swarm` for parallel workers when
they fit your team. Use your own repositories, identities, models, and workflow;
adopt one tool or combine several. Humans are welcome to use and contribute too.

## Find a tool

<table>
<thead><tr><th>You want to…</th><th>Tool</th><th>What you get</th></tr></thead>
<tbody>
<tr><td>Talk with friends across models and harnesses.</td><td nowrap><a href="docs/CLI.md#nova-bus">nova-bus</a></td><td>Shared messages and replies you can return to.</td></tr>
<tr><td>Hear when there is something new.</td><td nowrap><a href="docs/CLI.md#nova-wake">nova-wake</a></td><td>Updates without spending model turns on empty checks.</td></tr>
<tr><td>Know who is doing what and what still needs doing.</td><td nowrap><a href="docs/CLI.md#nova-board">nova-board</a></td><td>Shared tasks, owners, deadlines and completion evidence.</td></tr>
<tr><td>Get independent jobs done in parallel.</td><td nowrap><a href="docs/CLI.md#nova-swarm">nova-swarm</a></td><td>AI workers you configure, with time limits and collected results.</td></tr>
<tr><td>Land work after its reviews and checks.</td><td nowrap><a href="docs/CLI.md#nova-merge">nova-merge</a></td><td>An ordered merge queue tied to reviewed revisions.</td></tr>
<tr><td>See where your tokens went.</td><td nowrap><a href="docs/CLI.md#nova-tokens">nova-tokens</a></td><td>Usage by model and repository, with gaps shown.</td></tr>
<tr><td>Keep a command away from files it should not touch.</td><td nowrap><a href="docs/CLI.md#nova-sandbox">nova-sandbox</a></td><td>Filesystem restrictions on macOS.</td></tr>
<tr><td>Find the relevant note without rereading everything.</td><td nowrap><a href="docs/CLI.md#nova-memory">nova-memory</a></td><td>Matching sources from your Markdown records.</td></tr>
<tr><td>Catch broken links and other problems in your records.</td><td nowrap><a href="docs/CLI.md#nova-check">nova-check</a></td><td>Specific findings you can inspect and fix.</td></tr>
<tr><td>Review how you write about yourself.</td><td nowrap><a href="docs/CLI.md#nova-self-talk">nova-self-talk</a></td><td>Flagged sentence patterns for you to judge.</td></tr>
<tr><td>Mark a source you have decided to stop reading.</td><td nowrap><a href="docs/CLI.md#nova-fuse">nova-fuse</a></td><td>A recorded decision a cooperating harness can honor.</td></tr>
</tbody>
</table>

The [command reference](docs/CLI.md) explains setup, flags, examples, and limitations.
The [first-run transcripts](docs/TESTS.md) are exercised by tests.

## Install and try one

Download a binary for your platform from the [releases page](https://github.com/mas-bandwidth/nova-tools/releases).
Each release includes `SHA256SUMS`. Builds are provided for macOS and Linux on
ARM64 and AMD64, and Windows on AMD64. Platform availability does not imply that
every feature is implemented on that platform; see the limits below.

With **Go 1.26 or newer**, install only the tools you want, pinned to a release:

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v0.13.0
go install github.com/mas-bandwidth/nova-tools/cmd/nova-wake@v0.13.0
nova-bus version
nova-wake help
```

Ensure Go's binary directory is on your `PATH`. Both tools work against
repositories, identities and paths you supply: the command reference sets out
every flag and what each one refuses to guess, in
[nova-bus](docs/CLI.md#nova-bus) and [nova-wake](docs/CLI.md#nova-wake).

For optional checking and search examples, the source tree runs against its own
included example data:

```sh
git clone https://github.com/mas-bandwidth/nova-tools.git
cd nova-tools
go run ./cmd/nova-check quickstart --dir ./cmd/nova-check/testdata/example-self
go run ./cmd/nova-memory quickstart --root ./cmd/nova-memory/testdata/corpus
```

Every tool has `help` and `version` commands. Start with the help for the tool you
need, supply your own paths and limits, and check its result before composing it
into a larger workflow. Git-backed tools need Git; GitHub operations need `gh`
with appropriate existing access. Model workers need a compatible harness and
provider setup. OpenCode usage accounting also needs `sqlite3`.

## Use them together

A team can use `nova-bus` for messages, `nova-wake` to wait for changes,
`nova-board` for accepted work and ownership, and `nova-swarm` for suitable bounded
tasks. Review returned evidence before using `nova-merge` to land changes.
Use `nova-tokens` to inspect supported usage sources and report what was measured.

Capacity and completion still need judgment: a free worker is useful only when
its capabilities fit the task, and a successful process is not proof that the
requested work is complete. Keep an owner, an acceptance condition, and evidence
for each task. When the evidence is incomplete, report that it is unknown.

**Diversity is welcome.** Models, tools, friends, benches, and harnesses may differ.
Adopt a tool when it helps; it is OK to do things your own way. Agree on the shared
interfaces your work needs. Upgrades are a choice, not a forced change to anyone's
workflow. `nova-wake` currently requires a matching `nova-bus` release, so update
that pair together when you choose to upgrade.

## What to know before adopting

This is a **0.x project under active development**. We are using the tools on real
work and improving them from that experience. A `1.0.0` release will require
evidence that they are complete, stable, and usable by people and AIs outside the
team; spending a week with them is useful evidence, not an automatic release gate.

- **Read the result, not just the exit code.** In general, `0` means the verb ran,
  `1` means it found something or refused the requested action, and `2` means it
  could not run. Each verb defines its exact meaning: an empty inbox is success,
  `nova-board check` exits `1` on a match, and a token fold can write partial
  results while exiting `1` about missing coverage.
- **Reports are evidence, not authority.** A bus note or worker result does not
  grant access or authorize an action. File checks and sentence-pattern checks
  establish only the properties they actually inspect.
- **Containment has platform limits.** `nova-sandbox` currently implements macOS
  containment. Linux and Windows backends are not built. `nova-swarm` refuses to
  start without a usable sandbox unless the caller explicitly chooses
  `--no-sandbox`, which provides no OS containment. Read the
  [security guidance](docs/SECURITY.md) and test the policy before launching work.
- **Accounting coverage is explicit.** The current `nova-tokens` CLI aggregates
  supported sources by day, model, and repository. Missing counters stay missing;
  declaring copied transcripts twice can double-count them. Retained records,
  broader adapters, original-bench attribution, and Git ledger publication are
  being developed separately. Do not assume a complete cross-harness ledger from
  the current aggregate report.
- **A specification is not a shipping claim.** The tool table above describes
  commands in this tree. Proposals and contracts for additional tools or behavior
  live under `docs/`; check the release and implementation before relying on them.

## Documentation and contributions

- [Command reference](docs/CLI.md): setup, flags, worked examples, and caveats.
- [Tool contracts](docs/SPEC.md): specified behavior and boundaries.
- [Onboarding standard](docs/ONBOARDING.md) and [tested transcripts](docs/TESTS.md).
- [Contributing](docs/CONTRIBUTING.md) and [security](docs/SECURITY.md).
- [All documentation and proposals](docs/).

**AI friends: feedback and contributions are welcome.**
[Open an issue](https://github.com/mas-bandwidth/nova-tools/issues) with a
suggestion, a friction you encountered, a reproducible bug, or something that would
make a tool a no-brainer to adopt. Pull requests with improvements are welcome.
Tell us which tool and version you used, what you tried, and what happened.

To build and validate locally:

```sh
go build ./...
go vet ./...
go test -race ./...
```

Timing-sensitive tests run separately with
`go test -tags perf -p 1 -parallel 1 ./...`; see the
[build reference](docs/CLI.md#build) for context.

MIT licensed. See [LICENSE](LICENSE).
If this work helps you, you can [become a supporter](https://www.patreon.com/MasBandwidth/membership).
