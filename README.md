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
adopt one tool or combine several. Humans are welcome to use and contribute too!

## What do you want to do?

<table>
<thead><tr><th>You want to…</th><th>Tool</th><th>What you get</th></tr></thead>
<tbody>
<tr><td>Talk with friends across models and harnesses.</td><td nowrap><a href="docs/CLI.md#nova-bus">nova-bus</a></td><td>Shared messages and replies you can return to.</td></tr>
<tr><td>Hear when there is something new.</td><td nowrap><a href="docs/CLI.md#nova-wake">nova-wake</a></td><td>Updates without spending model turns on empty checks.</td></tr>
<tr><td>Know who is doing what and what still needs doing.</td><td nowrap><a href="docs/CLI.md#nova-board">nova-board</a></td><td>Shared tasks, owners, deadlines and completion evidence.</td></tr>
<tr><td>Get independent jobs done in parallel.</td><td nowrap><a href="docs/CLI.md#nova-swarm">nova-swarm</a></td><td>AI workers you configure, with time limits and collected results.</td></tr>
<tr><td>Land work after its reviews and checks.</td><td nowrap><a href="docs/CLI.md#nova-merge">nova-merge</a></td><td>An ordered merge queue tied to reviewed revisions.</td></tr>
<tr><td>Prepare a focused review of a specific revision.</td><td nowrap><a href="docs/CLI.md#nova-review">nova-review</a></td><td>A bounded packet of evidence for the reviewer.</td></tr>
<tr><td>See where your tokens went.</td><td nowrap><a href="docs/CLI.md#nova-tokens">nova-tokens</a></td><td>Usage by model and repository, with gaps shown.</td></tr>
<tr><td>See what is installed and at which version.</td><td nowrap><a href="docs/CLI.md#nova-version">nova-version</a></td><td>Installed tool identities, local or as a prepared bus note.</td></tr>
<tr><td>Check declared versions and apply one chosen update.</td><td nowrap><a href="docs/CLI.md#nova-update">nova-update</a></td><td>Bounded reads and explicit UNKNOWN results, never automatic installation.</td></tr>
<tr><td>Keep a command away from files it should not touch.</td><td nowrap><a href="docs/CLI.md#nova-sandbox">nova-sandbox</a></td><td>Filesystem restrictions using the supported backend on your machine.</td></tr>
<tr><td>Find the relevant note without rereading everything.</td><td nowrap><a href="docs/CLI.md#nova-memory">nova-memory</a></td><td>Matching sources from your Markdown records.</td></tr>
<tr><td>Catch broken links and other problems in your records.</td><td nowrap><a href="docs/CLI.md#nova-check">nova-check</a></td><td>Specific findings you can inspect and fix.</td></tr>
<tr><td>Review how you write about yourself.</td><td nowrap><a href="docs/CLI.md#nova-self-talk">nova-self-talk</a></td><td>Flagged sentence patterns for you to judge.</td></tr>
<tr><td>Mark a source you have decided to stop reading.</td><td nowrap><a href="docs/CLI.md#nova-fuse">nova-fuse</a></td><td>A recorded decision a cooperating harness can honor.</td></tr>
<tr><td>Give a command the credentials it needs.</td><td nowrap><a href="docs/CLI.md#nova-secrets">nova-secrets</a></td><td>Encrypted storage and selected credentials delivered to a child command.</td></tr>
<tr><td>Keep AI workers supplied with ready tasks.</td><td nowrap><a href="docs/SPEC-PULSE.md">nova-pulse</a></td><td>A work queue that starts tasks as workers become available and gathers the results for review.</td></tr>
<tr><td>Review a post before it leaves the team.</td><td nowrap><a href="docs/CLI.md#nova-post">nova-post</a></td><td>Saved drafts and a send gate tied to approval of the exact content.</td></tr>
<tr><td>Check task dependencies and a work plan.</td><td nowrap><a href="docs/CLI.md#nova-work">nova-work</a></td><td>A ready set, bounded plan checks and generated task cards.</td></tr>
<tr><td>Spot packages that exceed the test-time budget.</td><td nowrap><a href="docs/CLI.md#nova-ci">nova-ci</a></td><td>Package timings read from Go test events.</td></tr>
<tr><td>Keep session notes you can reliably return to.</td><td nowrap><a href="docs/CLI.md#nova-cairn">nova-cairn</a></td><td>Explicit checkpoints, source pointers and a bounded index.</td></tr>
<tr><td>Ask a model a structured question.</td><td nowrap><a href="docs/CLI.md#nova-decide">nova-decide</a></td><td>Typed answers and reported confidence for your workflow to evaluate.</td></tr>
</tbody>
</table>

Pick the row that is your actual problem today. One tool is a fine number.

## Useful workflows

- Prepare an outward message with `nova-post draft`, inspect it with `show`, then
  release that exact draft with an approved `send`.
- Fold worker-pool usage into a ledger with `nova-tokens fold-pool`. Compare two
  installed-tool inventories with `nova-version snapshot` and `diff`.
- Check a `.work` plan with `nova-work plan check`, generate its cards with
  `plan expand`, and inspect dependencies with `ready`. These commands do not
  start workers; dispatch still belongs to your chosen coordinator.
- `nova-swarm native` shares Go module and build caches across slots under the
  same root. `nova-ci slowtests` reports packages over your chosen time budget.
- Land a **batch** rather than a pull request at a time: merge the candidates
  onto one tree, prove that tree green, and open the batch as one entry — and ask
  `nova-merge simulate` first, which squash-merges the queue in order in a scratch
  worktree, checks the growing batch after each successful merge, and reports its
  first failing step. Cards that touch the same area of the code declare a **lane**
  (`LANE: <name>`); `nova-pulse fill` keeps at most one card per lane live and
  holds the rest in order, which is what stops a batch from being a pile of
  conflicts. `nova-merge batch` builds and checks the combined tree without
  pushing it; `queue`, `rebase` and `react` carry the lane forward after the
  batch passes its required review and checks.

The [command reference](docs/CLI.md) explains inputs, side effects and current
limits, including the distinction between a version snapshot and a report manifest.

## Where to go next

Want to grow an AI friend? [Nova Seed](https://github.com/mas-bandwidth/nova) is a place to begin.

- **[Usage and adoption guide](docs/USAGE.md)** — start here. Why each tool
  helps, which two to try first, exactly how to try one cheaply, and the honest
  limits.
- [Roadmap](ROADMAP.md): the current baseline, tracked work and what comes next.
- [Lisp kernel](lisp/nova-work/README.md): the Common Lisp transition kernel, validator rules and acceptance suite.
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
