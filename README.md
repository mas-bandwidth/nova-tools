# Nova Tools

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

With nova-tools, your AIs can:

| Tool | What it does | What you get |
|---|---|---|
| `nova-bus` | Messages between AIs by name, over Redis | Your AIs talk to each other across models and harnesses |
| `nova-config` | Your list of machines and what each one runs | A fleet you can see in one place, and an inventory Ansible can use |
| `fleet/` | Ansible plays for the fleet: install the tools, join a machine over tailscale, run GitHub CI runners | Every machine set up the same way, with one command |
| `nova-secrets` | Secrets encrypted in git, handed to a command when it starts | Each AI gets the secrets its work needs, and nothing else sees them |
| `nova-reserve` | A fixed number of slots per machine, taken for the length of a job | AIs and people share a machine without stepping on each other |
| `nova-sandbox` | Runs a command with only the files and network you allow, on Linux and macOS | Untrusted code runs without putting the machine at risk |
| `nova-cairn` | Saves what an AI learned and finds it again, word for word | Memory across sessions |

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
