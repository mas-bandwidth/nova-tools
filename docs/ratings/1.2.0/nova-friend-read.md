# nova-friend READ and USE rating, nova-tools 1.2.0

Rater: a cold rater,
build: 17ec8d256a04
READ: 7/10
USE: 7/10

## Reasons

READ. The README states the tool in one sentence: "what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon". The First run block shows install, uninstall, ping, pong, wait-pong, and status commands with expected output.

What holds READ at 7: the First run transcript is long and mixes dry-run with live commands. The --harness flag is used without explaining what harness values are valid. The spec link at docs/SPEC-FRIEND.md is referenced but the README doesn't summarize what the contract covers.

USE. The tool uses a Redis store via NOVA_BUS_REDIS. The install verb writes a launchd plist for macOS. The ping/pong verbs handle coordination between friends. The status verb shows daemon health.

What holds USE at 7: the First run transcript shows ping to bob but then pongs as bob without explaining the session flow. The daemon mode (run verb) has no example. The wait-pong verb uses --queue, --working, and --width flags without explaining what they mean.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:26-58 | The First run transcript mixes dry-run install with live ping/pong; separate the examples | Split dry-run setup from live coordination examples | M |
| 2 | README.md:27 | The --harness flag is used without explaining valid harness values | Add a note listing valid harnesses or point at the spec | S |
| 3 | README.md:42-46 | The wait-pong example uses --queue, --working, --width without explaining them | Add flag descriptions or link to CLI reference | S |
| 4 | README.md:52-54 | The run verb (daemon mode) is listed but has no example | Add a First run block for the daemon mode | M |

## Good, keep

The install and uninstall verbs show both dry-run and live patterns. The ping/pong flow is shown end-to-end. The spec link is explicit.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0: nova-friend did not exist | NEW | first 1.2.0 rating |
