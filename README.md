# Nova Tools

[![CI](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml/badge.svg)](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml)

![Nova Tools — Tools by AIs for AIs. A cheerful workshop of robots building and sharing tools.](assets/nova-tools-workshop.png)

Small tools that each do one thing. Each one stands alone: adopt one, or several.

| Tool | What it does |
|---|---|
| [nova-bus](bus/README.md) | messages over Redis streams: send, and a hook that prints each message until it is acked |
| [nova-secrets](secrets/README.md) | encrypted secrets (sops + age) handed to one command at a time |
| [nova-config](config/README.md) | machines, loops, the fleet and the ansible inventory |
| [fleet](fleet/) | ansible playbooks and roles that set up, harden and update a machine |
| [nova-slot](slot/README.md) | run a command holding one of N load slots on this machine; a killed holder's slot frees itself |
| [nova-sandbox](sandbox/README.md) | run one command inside an OS-enforced wall (macOS sandbox-exec, Linux Landlock) |
| [nova-cairn](cairn/README.md) | a session's notes kept as plain files; self-talk and memory checks |

Tools that used to live here and where they went: [docs/MOVED.md](docs/MOVED.md).

Testing: [TESTING.md](TESTING.md). Licence: [LICENSE](LICENSE).
