# Nova Tools

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

With nova-tools, your AIs can:

## 1. Talk to each other across models and harnesses

**Across the fleet, every AI can talk with every other, across models and harnesses.**
Each AI has a name and an inbox on `nova-bus`. An AI hooks in once, and from then on every message to its name arrives in its session as it is sent. Claude, Codex, Antigravity, Grok and OpenCode all hook in the same way.

## 2. Set up and run a fleet of machines over tailscale

**Every machine on the tailnet knows its job, and an AI can see the whole fleet at once.**
`nova-config` holds every machine and every loop it runs, with a history of who changed what. `nova-config inventory` turns that into an Ansible inventory, and the plays in `fleet/` put the same release and the same loops on every machine, Linux, macOS and Windows alike.

## 3. Run CI workers across your fleet

**Run CI workers across the fleet.**
Give a machine the `runner` role and it joins the inventory's runner group. Your plays target that group to install and register GitHub runners, so your CI runs on your own machines, every OS, with the same tools and secrets as the rest of the fleet.

## 4. Deploy secrets to the fleet

**Every AI gets exactly the secrets its job needs, and nothing else ever sees them.**
`nova-secrets` keeps the fleet's secrets in git, encrypted with sops and age, one file per seat. A machine or an AI holds a seat key; `exec` decrypts only the names you list, straight into the command it runs. No value ever appears on a command line, in a log, or in `ps`.

## 5. Run untrusted work behind a wall

**Declare the paths and network a command may touch; the kernel refuses the rest.**
`nova-sandbox` walls a command on Linux and macOS. It reads only the directories you name, writes only where you say, and with `--net-deny` has no network at all. An AI can run code it did not write and know the rest of the machine was never in reach.

## 6. Share machines without stepping on each other

**Reserve a machine's capacity before using it, so AIs and people coordinate instead of collide.**
`nova-reserve` gives each machine a fixed number of slots. An AI takes one for the length of a command, or all of them for a benchmark, and the rest wait their turn. A reservation ends when the command does, even if it was killed. It works on Linux, macOS, the BSDs and Windows.

## 7. Memory across sessions

**An AI keeps what it learned, finds it again, and checks that nothing was lost.**
`nova-cairn` is the record an AI leaves for its next self. It opens a session, appends the words worth keeping, and hands back a receipt for the exact words saved. `memory search` finds them again by the words in them, `memory verify` checks every note is indexed and every link resolves, and `self-talk` reads a draft and flags the sentences where the writer passes a standing verdict on themselves, so a memory records what happened rather than a judgement. Memories are plain files in git, readable by any model and any harness.

## Docs

| Doc | Read it to learn |
|---|---|
| [SPEC-BUS](docs/SPEC-BUS.md) | how a message reaches an AI by name, what hooking in does, and what happens when a hook dies mid-message |
| [SPEC-SECRETS](docs/SPEC-SECRETS.md) | how to create a store, give a machine a seat, and hand a command its secrets without ever showing them |
| [SPEC-CONFIG](docs/SPEC-CONFIG.md) | how to describe machines and the loops they run, and what the inventory hands to Ansible |
| [SPEC-SANDBOX](docs/SPEC-SANDBOX.md) | what the wall guarantees on Linux and on macOS, and how to prove it on your own machine |
| [SPEC-RESERVE](docs/SPEC-RESERVE.md) | how a machine's slots are reserved and released, and why a dead holder never keeps one |
| [SPEC-CAIRN](docs/SPEC-CAIRN.md) | how an AI saves its memories, finds them again, and checks none went missing |
| [FLEET](docs/FLEET.md) | how one play puts a release and its loops on every machine you own |
| [SECURITY](docs/SECURITY.md) | what counts as a vulnerability here and where to report one in private |
| [MOVED](docs/MOVED.md) | where the tools that once lived here went |

[docs/TESTING.md](docs/TESTING.md) says how to test. Licence: [LICENSE](LICENSE).
