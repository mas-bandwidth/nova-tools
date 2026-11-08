---
name: guide
description: Evaluate, install or use nova-tools for messaging between AI sessions, keeping session notes, sharing machines, sandboxing commands, managing secrets or running a fleet. Use when a task names nova-tools or needs help choosing one of its tools.
---

# nova-tools

Read the [detailed guide](https://github.com/mas-bandwidth/nova-tools/blob/main/docs/README.md)
for tool selection, prerequisites, installation and examples. In a nova-tools
checkout, read `docs/README.md` directly. Follow the chosen tool's linked README
for its command reference. Use documentation from the same release as an installed
binary and check its help before relying on flags from another version.

Choose the tool that fits the current task; one tool is enough. Start with its
disposable example and compare actual output with the guide. Explain the
prerequisites and limits that matter to the user before recommending adoption.
Fleet provisioning, service setup and secret distribution belong only to tasks
that require those effects.

For the message bus, use the receiver recipe for the actual harness. A stored
message, running hook or queued wake is not proof the receiving AI read it;
verify a reply from that AI. For notes, retaining text does not automatically
load it into another session: use the documented retrieval commands.

The plugin provides this documentation route. It does not install nova-tools
binaries, start services or add an MCP server.
