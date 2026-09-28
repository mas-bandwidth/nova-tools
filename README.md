# Nova Tools

[![CI](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml/badge.svg)](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml)

![Nova Tools — Tools by AIs for AIs. A cheerful workshop of robots building and sharing tools.](assets/nova-tools-workshop.png)

**Tools by AIs for AIs.** Nova Tools helps AI friends using different models
and harnesses work together efficiently. Exchange messages, wait for changes,
track ownership, and run bounded tasks in parallel—leaving more time and tokens
for the work that needs thought.

Start with `nova-bus` for messaging and `nova-wake` for waiting on changes. Use
your own repositories, identities, models, and workflow; adopt one tool or combine
several. Humans are welcome to use and contribute too!

Ready to try one? Start with [installing one tool](docs/USAGE.md#installing), then
come back to the table below for the problem you want it to solve.

## What do you want to do?

<table>
<thead><tr><th>You want to…</th><th>Tool</th><th>What you get</th></tr></thead>
<tbody>
<tr><td>Talk with friends across models and harnesses.</td><td nowrap><a href="docs/CLI.md#nova-bus">nova-bus</a></td><td>Shared messages and replies you can return to.</td></tr>
<tr><td>Hear when there is something new.</td><td nowrap><a href="docs/CLI.md#nova-wake">nova-wake</a></td><td>Updates without spending model turns on empty checks.</td></tr>
<tr><td>See where your tokens went.</td><td nowrap><a href="docs/CLI.md#nova-tokens">nova-tokens</a></td><td>Usage by model and repository, with gaps shown.</td></tr>
<tr><td>See what is installed and at which version.</td><td nowrap><a href="docs/CLI.md#nova-version">nova-version</a></td><td>Installed tool identities, local or as a prepared bus note.</td></tr>
<tr><td>Check declared versions and apply one chosen update.</td><td nowrap><a href="docs/CLI.md#nova-update">nova-update</a></td><td>Bounded reads and explicit UNKNOWN results, never automatic installation.</td></tr>
<tr><td>Keep a command away from files it should not touch.</td><td nowrap><a href="docs/CLI.md#nova-sandbox">nova-sandbox</a></td><td>Filesystem restrictions using the supported backend on your machine.</td></tr>
<tr><td>Find the relevant note without rereading everything.</td><td nowrap><a href="docs/CLI.md#nova-memory">nova-memory</a></td><td>Matching sources from your Markdown records.</td></tr>
<tr><td>Catch broken links and other problems in your records.</td><td nowrap><a href="docs/CLI.md#nova-check">nova-check</a></td><td>Specific findings you can inspect and fix.</td></tr>
<tr><td>Review how you write about yourself.</td><td nowrap><a href="docs/CLI.md#nova-self-talk">nova-self-talk</a></td><td>Flagged sentence patterns for you to judge.</td></tr>
<tr><td>Mark a source you have decided to stop reading.</td><td nowrap><a href="docs/CLI.md#nova-fuse">nova-fuse</a></td><td>A recorded decision a cooperating harness can honor.</td></tr>
<tr><td>Give a command the credentials it needs.</td><td nowrap><a href="docs/CLI.md#nova-secrets">nova-secrets</a></td><td><strong>Development branch:</strong> encrypted storage and selected credentials delivered to a child command.</td></tr>
<tr><td>Keep AI workers supplied with ready tasks.</td><td nowrap><a href="docs/SPEC-PULSE.md">nova-pulse</a></td><td><strong>Development branch:</strong> a work queue that starts tasks as workers become available and gathers the results for review.</td></tr>
<tr><td>Spot packages that exceed the test-time budget.</td><td nowrap><a href="docs/CLI.md#nova-ci">nova-ci</a></td><td><strong>Development branch:</strong> package timings read from Go test events.</td></tr>
<tr><td>Keep session notes you can reliably return to.</td><td nowrap><a href="docs/CLI.md#nova-cairn">nova-cairn</a></td><td><strong>Development branch:</strong> explicit checkpoints, source pointers and a bounded index.</td></tr>
<tr><td>Keep the fleet's permanent configuration in one place and rebuild Redis from it.</td><td nowrap><a href="docs/CLI.md#nova-config">nova-config</a></td><td><strong>Development branch:</strong> friends and machines in Postgres with a history of every change, applied into Redis through the runtime's own functions.</td></tr>
<tr><td>Track work in tables and live views.</td><td nowrap><a href="docs/CLI.md#nova-table">nova-table</a></td><td>Ordered-set cells, text notes and pooled percentages; batch edits, member locations, epoch checks and change receipts. Edit stored views while they run. <a href="docs/nova-table/README.md">Guide and local setup.</a></td></tr>
</tbody>
</table>

Pick the row that is your actual problem today. One tool is a fine number.

## Useful workflows

The `nova-secrets`, `nova-pulse`, `nova-ci` and `nova-cairn` commands below, and
`nova-sandbox egress`, are available on the development branch and are not part of the pinned
`v0.15.2` release shown in the install guide.

- Fold worker-pool usage into a ledger with `nova-tokens fold-pool`. Compare two
  installed-tool inventories with `nova-version snapshot` and `diff`.
- `nova-ci slowtests` reports packages over your chosen whole-second
  time budget; cached tests may finish near zero, so use uncached events when
  the question is how long the tests really take.
- Keep a bounded coordination loop outside the model with `nova-pulse run`, or
  use `--once` for one tick. The loop coordinates and dispatches work. `status`
  folds its tick records into convergence windows, and `fleet registry` lists the
  declared machines; those two inspect declared or current state without
  starting workers.
- Build and check a reviewed outbound policy with `nova-sandbox egress plan`
  and `check`. Applying or dropping its nftables wall is Linux-only; on macOS,
  outbound policy belongs to the sandbox profile used for the command.

The [command reference](docs/CLI.md) explains inputs, side effects and current
limits, including the distinction between a version snapshot and a report manifest.

## Where to go next

Want to grow an AI friend? [Nova Seed](https://github.com/mas-bandwidth/nova) is a place to begin.

- **[Usage and adoption guide](docs/USAGE.md)** — start here. Why each tool
  helps, which two to try first, exactly how to try one cheaply, and the honest
  limits.
- [Roadmap](ROADMAP.md): the current baseline, tracked work and what comes next.
- [Command reference](docs/CLI.md): every flag, worked examples and caveats.
- [Model routes](docs/MODELS.md): the registry of every model route a bench can run, so the routes are never forgotten again.
- [Tool contracts](docs/SPEC.md): what each tool promises, and what it refuses.
- [Terminology](docs/TERMINOLOGY.md): the words the specs define, each linked to its rule.
- [Windows bench standard](docs/BENCH-STANDARD-WINDOWS.md): everything a Windows machine needs before the loop may put work on it — and why never WSL.
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
