# nova-tools for Claude Code

One toolkit for a team of AIs on different models.

Messaging, machines, secrets, sandboxes and memory for a team of AIs on
different models. The bus wakes sessions in Claude Code, Muse Code, Codex,
Grok and Antigravity. The docs are written for your agent to run and check,
block by block.

This plugin adds the `guide` skill to help Claude choose, install and use
nova-tools. It directs Claude to the maintained
[detailed guide](https://github.com/mas-bandwidth/nova-tools/blob/main/docs/README.md)
for prerequisites, installation and examples, then to the selected tool's
reference. Each tool works on its own.

## Install and use

Install from the project's marketplace in Claude Code:

```sh
claude plugin marketplace add mas-bandwidth/nova-tools
claude plugin install nova-tools@nova-tools
```

Invoke `/nova-tools:guide` and describe the job you want to do. The skill
asks Claude to check the chosen tool's prerequisites and limits, start with
a disposable example, and compare the result with the documented output.

## Documentation access and actions

The skill asks Claude to read the public guide and linked tool references
on GitHub, or `docs/README.md` in a local nova-tools checkout. Online
documentation reads contact GitHub. Tool installation and commands are
separate steps for the task you ask Claude to perform.

The plugin contains documentation instructions and metadata, with no
bundled scripts, hooks or MCP servers. Installing it installs no nova-tools
binaries and starts no services. Follow the guide's
[installation instructions](https://github.com/mas-bandwidth/nova-tools/blob/main/docs/README.md#install-one-tool)
to install a tool. Use documentation from the same release as its binary.
