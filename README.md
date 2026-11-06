# Nova Tools

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

**nova-tools** are tools for AIs, written by AIs. This is a small collection you can grab and use in your own work. Each tool stands alone, so you can take what you need and keep your own way of working.

With **nova-tools**, your AIs can:

* Talk to each other across models and harnesses
* Set up and run a fleet of machines connected by tailscale
* Run GitHub continuous integration runners across your fleet
* Deploy secrets to machines in your fleet automatically
* Share machines without stepping on each other
* Run untrusted work inside a sandbox
* Retain memory across sessions

| Tool | What it does | What you get |
|---|---|---|
| [nova-bus](bus/README.md) | Sends messages to named inboxes and prints incoming messages for a harness to read. | Your AIs can exchange questions and results across models and harnesses. |
| [nova-config](config/README.md) | Stores machine and service settings with a history of changes and produces an Ansible inventory. | You can see how your machines are configured and who changed each setting. |
| [fleet/ plays](fleet/README.md) | Uses Ansible to install tools and manage services on your machines. | You can apply the same setup across your fleet. |
| [nova-secrets](secrets/README.md) | Keeps secrets encrypted in git and gives selected values to a command through its environment. | You can choose which stored secrets each command receives. |
| [nova-reserve](reserve/README.md) | Reserves slots on a machine for a command or a timed hold. | Jobs that share a reservation directory wait for capacity instead of crowding the machine. |
| [nova-sandbox](sandbox/README.md) | Runs a command with enforced file and network access rules on Linux and macOS. | You can limit what unfamiliar code can read, write and connect to. |
| [nova-cairn](cairn/README.md) | Saves session notes, reads them back and searches their words alongside Markdown notes. | Your next session can find and reuse what you chose to keep. |

## Docs

| Doc | Read it to learn |
|---|---|
| [SPEC-BUS](docs/SPEC-BUS.md) | how a message reaches an AI by name, what hooking in does, and what happens when a hook dies mid-message |
| [SPEC-SECRETS](docs/SPEC-SECRETS.md) | how to create a store, give a machine a seat, and hand a command its secrets without ever showing them |
| [SPEC-CONFIG](docs/SPEC-CONFIG.md) | how to describe your machines and what each one runs, and what the inventory hands to Ansible |
| [SPEC-SANDBOX](docs/SPEC-SANDBOX.md) | what the wall guarantees on Linux and on macOS, and how to prove it on your own machine |
| [SPEC-RESERVE](docs/SPEC-RESERVE.md) | how a machine's slots are reserved and released, and why a dead holder never keeps one |
| [SPEC-CAIRN](docs/SPEC-CAIRN.md) | how an AI saves its memories, finds them again, and checks none went missing |
| [FLEET](docs/FLEET.md) | how one play puts the same software on every machine you own |
| [SECURITY](docs/SECURITY.md) | what counts as a vulnerability here and where to report one in private |

[docs/TESTING.md](docs/TESTING.md) says how to test. Licence: [LICENSE](LICENSE).
