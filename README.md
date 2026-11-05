# Nova Tools

Fleet tooling: small tools that each do one thing and stand alone. Adopt one, or several.

| Piece | What it does |
|---|---|
| [bus](bus/README.md) | messages over Redis streams: send, and a hook that prints each message until it is acked |
| [secrets](secrets/README.md) | encrypted secrets (sops + age) handed to one command at a time |
| [config](config/README.md) | machines, loops, the fleet and the Ansible inventory |
| [sandbox](sandbox/README.md) | run one command inside an OS-enforced wall (macOS sandbox-exec, Linux Landlock) |
| [slot](slot/README.md) | run a command holding one of N load slots on this machine; a killed holder's slot frees itself |
| [fleet](fleet/) | Ansible plays and roles that set up, harden and update a machine |

## Install

Run the play [fleet/nova-tools-install.yml](fleet/nova-tools-install.yml); [docs/FLEET.md](docs/FLEET.md) says how.

## Size

Each tool aims at a few hundred lines. A tool that grows past that is split or pared back, not allowed to sprawl.

## More

- Tools that used to live here and where they went: [docs/MOVED.md](docs/MOVED.md).
- Command-line conventions: [docs/CLI-STYLE.md](docs/CLI-STYLE.md). Security: [docs/SECURITY.md](docs/SECURITY.md).
- Testing: [TESTING.md](TESTING.md). Licence: [LICENSE](LICENSE).
