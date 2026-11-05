# Nova Tools

nova-tools is the small set of tools that lets a group of AI agents work as a fleet: talk to each other, share machines, hold secrets and run safely. Each tool is a few hundred lines and does one thing.

Now, with nova-tools, your AIs can:

## Talk to each other across models and harnesses

**Any process that can reach Redis can send and receive messages by name.**
`nova-bus` keeps one inbox per name. A model hooks in with `nova-bus hook`, which prints one line for each message addressed to its name, so the harness shows it as input. It works the same for Claude, GPT, Gemini, Grok, a shell script or a daemon: they all just run the command.

```sh
export NOVA_BUS_REDIS=redis-host:6379          # NOVA_BUS_PASSWORD too, if the Redis has one
nova-bus hook --as bob                         # bob's session: one line per message, until killed
nova-bus send --as alice --to bob "review" "PR 12 is ready"
nova-bus who                                   # names whose hook is running
nova-bus pending --as bob --list               # what bob has not handled yet
```

## Set up and run a fleet of machines over tailscale

**Describe your machines and what each one runs once, and get an Ansible inventory from it.**
`nova-config` keeps the machines and the loops each runs as rows in PostgreSQL, with a history row for every write. `nova-config inventory` prints them as an Ansible inventory, and the plays in `fleet/` install a release and the loops on every machine.

```sh
export NOVA_PG_DSN=postgres://nova@db-host:5432/nova   # the password comes from NOVA_PG_PASSWORD
nova-config machine add bench-a --user nova --roles bench --actor ops
nova-config loop add reader --machine bench-a --argv '["nova-bus","hook","--as","reader"]' --keepalive true --actor ops
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > nova-inventory && chmod +x nova-inventory
```

Not yet in the repo: the roles for ssh hardening, joining tailscale and installing CI runners (docs/FLEET.md, "Not yet here").

## Run CI workers across the fleet

**Mark which machines run CI jobs.**
A machine with the `runner` role is listed in the inventory's runners group, so a play can target it. The role that installs and registers GitHub runners is the next piece and is not here yet.

## Deploy secrets to the fleet without ever exposing them

**A machine gets only the secret names it is given, handed straight to one command.**
`nova-secrets` keeps secrets in git, encrypted with sops and age, one file per seat. `exec` decrypts the named values into the child process's environment: never an argument, a log line or something `ps` shows.

```sh
export NOVA_SECRETS_STORE=~/secrets NOVA_SECRETS_KEY=~/.config/nova/bench-a.key
printf '%s\n' "$TOKEN" | nova-secrets set GITHUB_TOKEN        # encrypted and committed
nova-secrets exec --only GITHUB_TOKEN -- gh pr list           # only this command sees it
nova-secrets seat add bench-b age1...                         # a new machine's key; then set its names
```

## Run untrusted work behind a wall

**Declare the paths and network a command may touch; the kernel refuses the rest.**
`nova-sandbox` uses Landlock on Linux and sandbox-exec on macOS. Only the directories you name are reachable, and `--net-deny` closes the network or the wall refuses to start.

```sh
nova-sandbox probe                                            # proves a write outside, a read outside and a connect are refused
nova-sandbox --read /opt/tools --write /srv/work --net-deny -- /opt/tools/run-agent
```

## Share machines without stepping on each other

**Cap how many heavy jobs run at once on a machine.**
`nova-slot` holds one of N slots while your command runs. The slots are OS file locks, so a killed holder's slot frees itself. It works on Linux, macOS, the BSDs and Windows.

```sh
nova-slot --slots 2 -- go test ./...      # waits for a free slot, runs, frees it
nova-slot --exclusive -- ./benchmark      # runs alone
nova-slot status                          # each slot: free, or who holds it
```

## Install

Install a release on a machine with the fleet play, from a checkout with Ansible and the inventory script above:

```sh
ansible-playbook -i ./nova-inventory fleet/nova-tools-install.yml -e nova_tools_version=v1.6.29 -l bench-a
```

Or build any one tool: each directory is a Go `package main`, so `go build ./bus` (or `./config`, `./sandbox`, `./secrets`, `./slot`) makes its binary.

## Size

Each tool aims at a few hundred lines. A tool that grows past that is split or pared back.

## Docs

| Doc | What it is |
|---|---|
| [SPEC-BUS](docs/SPEC-BUS.md) | the bus: messages, inboxes, delivery |
| [SPEC-SECRETS](docs/SPEC-SECRETS.md) | the secrets store, seats and exec |
| [SPEC-CONFIG](docs/SPEC-CONFIG.md) | machine, loop and fleet rows, and the inventory |
| [SPEC-SANDBOX](docs/SPEC-SANDBOX.md) | the wall and what it promises |
| [FLEET](docs/FLEET.md) | the Ansible plays and what is not here yet |
| [SECURITY](docs/SECURITY.md) | what counts as a vulnerability, and how to report one |
| [MOVED](docs/MOVED.md) | tools that used to live here, and where they went |

[TESTING.md](TESTING.md) says how to test. Licence: [LICENSE](LICENSE).
