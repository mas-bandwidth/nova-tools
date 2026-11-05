# Documentation

Pick the tool that helps your next task. Each guide starts with setup and a
small example. The specs explain the rest of the behavior and its limits.

## Start here

- [Project README](../README.md): meet the tools and the workshop friends.
- [Command-line guide](CLI-STYLE.md): arguments, output, exit codes and effects.
- [Security](SECURITY.md): trust boundaries, reporting and updates.
- [Repository boundaries](BOUNDARY.md): layout, dependencies and source budgets.
- [Building and testing](TESTING.md): named binaries, CI checks and model runs.

## Tools and fleet

| Piece | Getting started | Reference |
|---|---|---|
| Bus | [Send and receive messages](../bus/README.md) | [Inboxes, delivery and presence](SPEC-BUS.md) |
| Config | [Set up the store and inventory](../config/README.md) | [Rows, writes and inventory](SPEC-CONFIG.md) |
| Sandbox | [Run a command behind a wall](../sandbox/README.md) | [Grants and OS limits](SPEC-SANDBOX.md) |
| Secrets | [Give a command selected secrets](../secrets/README.md) | [Store, seats and keys](SPEC-SECRETS.md) |
| Reserve | [Share a machine](../reserve/README.md) | [Capacity, waits and process lifetime](SPEC-RESERVE.md) |
| Cairn | [Keep notes across sessions](../cairn/README.md) | [Records, search and note checks](SPEC-CAIRN.md) |
| Fleet | [Install tools and run services](FLEET.md) | The same guide covers variables and effects. |

## Changes

- [Migration](MOVED.md): renamed and removed commands, destinations and compatibility.
- [Changelog](CHANGELOG.md): release history and unreleased changes.
- [Roadmap](ROADMAP.md): planned work.
- [Artwork provenance](ASSET-PROVENANCE.md): the original workshop illustration.
- [Licence](../LICENSE): MIT terms.
