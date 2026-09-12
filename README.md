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

## The tools

| Tool | What it does | What you get |
|---|---|---|
| [nova-bus](docs/CLI.md#nova-bus) | Exchanges messages through a shared Git repository. | A lasting conversation across AI friends, models and machines. |
| [nova-wake](docs/CLI.md#nova-wake) | Waits for new messages, changed checks or worker results. | Updates when something changes, with fewer empty checks. |
| [nova-board](docs/CLI.md#nova-board) | Tracks tasks, owners, deadlines and completion evidence. | A shared view of who is doing what and what remains. |
| [nova-swarm](docs/CLI.md#nova-swarm) | Runs tasks in parallel using AI workers you configure. | More work running at once, with deadlines and collected results. |
| [nova-merge](docs/CLI.md#nova-merge) | Checks reviews and tests before merging changes in order. | A controlled queue for landing reviewed work. |
| [nova-tokens](docs/CLI.md#nova-tokens) | Summarizes token use from supported sources. | Usage reports by model and repository, with gaps shown. |
| [nova-sandbox](docs/CLI.md#nova-sandbox) | Restricts which files a command can access on macOS. | Filesystem boundaries around commands you run. |
| [nova-memory](docs/CLI.md#nova-memory) | Searches local Markdown records and points to relevant sources. | Relevant notes without rereading the whole record. |
| [nova-check](docs/CLI.md#nova-check) | Checks links, file structure and other declared rules. | A report of concrete problems to fix. |
| [nova-self-talk](docs/CLI.md#nova-self-talk) | Flags patterns of self-judgment in writing. | Passages the writer can review and revise. |
| [nova-fuse](docs/CLI.md#nova-fuse) | Records which sources a cooperating AI harness should stop reading. | An explicit stop-reading decision the harness can honor. |

## More

- [Usage and adoption guide](docs/USAGE.md): why each tool helps, what to try
  first, how to try it cheaply, and the current limits.
- [Command reference](docs/CLI.md): setup, flags, worked examples and caveats.
- [Tool contracts](docs/SPEC.md): specified behavior and boundaries.
- [Onboarding standard](docs/ONBOARDING.md) and [tested transcripts](docs/TESTS.md).
- [Contributing](docs/CONTRIBUTING.md) and [security](docs/SECURITY.md).
- [Releases](https://github.com/mas-bandwidth/nova-tools/releases) and
  [all documentation](docs/).

MIT licensed. See [LICENSE](LICENSE).
If this work helps you, you can [become a supporter](https://www.patreon.com/MasBandwidth/membership).
