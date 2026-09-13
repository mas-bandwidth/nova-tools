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
<tr><td>See where your tokens went.</td><td nowrap><a href="docs/CLI.md#nova-tokens">nova-tokens</a></td><td>Usage by model and repository, with gaps shown.</td></tr>
<tr><td>Keep a command away from files it should not touch.</td><td nowrap><a href="docs/CLI.md#nova-sandbox">nova-sandbox</a></td><td>Filesystem restrictions on macOS.</td></tr>
<tr><td>Find the relevant note without rereading everything.</td><td nowrap><a href="docs/CLI.md#nova-memory">nova-memory</a></td><td>Matching sources from your Markdown records.</td></tr>
<tr><td>Catch broken links and other problems in your records.</td><td nowrap><a href="docs/CLI.md#nova-check">nova-check</a></td><td>Specific findings you can inspect and fix.</td></tr>
<tr><td>Review how you write about yourself.</td><td nowrap><a href="docs/CLI.md#nova-self-talk">nova-self-talk</a></td><td>Flagged sentence patterns for you to judge.</td></tr>
<tr><td>Mark a source you have decided to stop reading.</td><td nowrap><a href="docs/CLI.md#nova-fuse">nova-fuse</a></td><td>A recorded decision a cooperating harness can honor.</td></tr>
</tbody>
</table>

Pick the row that is your actual problem today. One tool is a fine number.

## Where to go next

Want to grow an AI friend? [Nova Seed](https://github.com/mas-bandwidth/nova) is a place to begin.

- **[Usage and adoption guide](docs/USAGE.md)** — start here. Why each tool
  helps, which two to try first, exactly how to try one cheaply, and the honest
  limits.
- [Command reference](docs/CLI.md): every flag, worked examples and caveats.
- [Tool contracts](docs/SPEC.md): what each tool promises, and what it refuses.
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
