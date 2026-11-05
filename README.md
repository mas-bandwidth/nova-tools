# Nova Tools

nova-tools is the small set of tools that lets a group of AI agents work as a fleet: talk to each other, share machines, hold secrets and run safely. Each tool is a few hundred lines and does just one thing.

Now, with nova-tools, your AIs can:

## 1. Talk to each other across models and harnesses

**Any process that can reach Redis can send and receive messages by name.**
`nova-bus` keeps one inbox per name. A model hooks in with `nova-bus hook`, which prints one line for each message addressed to its name, so the harness shows it as input. It works the same for Claude, GPT, Gemini, Grok, a shell script or a daemon: they all just run the command.

## 2. Set up and run a fleet of machines over tailscale

**Describe your machines and what each one runs once, and get an Ansible inventory from it.**
`nova-config` keeps the machines and the loops each runs as rows in PostgreSQL, with a history row for every write. `nova-config inventory` prints them as an Ansible inventory, and the plays in `fleet/` install a release and the loops on every machine.

## 3. Run CI workers across your fleet

(this text sucked, rewrite it please)

## 4. Deploy secrets to the fleet without ever exposing them

`nova-secrets` keeps secrets in git, encrypted with sops and age, one file per seat. `exec` decrypts the named values into the child process's environment.

Secrets are never exposed to command line arguments, logs or something `ps` shows.

## 5. Run untrusted work behind a wall

**Declare the paths and network a command may touch; the kernel refuses the rest.**

`nova-sandbox` uses Landlock on Linux and sandbox-exec on macOS. Only the directories you name are reachable, and `--net-deny` closes the network or the wall refuses to start.

## 6. Share machines without stepping on each other

**Cap how many heavy jobs run at once on a machine.**
`nova-slot` holds one of N slots while your command runs. The slots are OS file locks, so a killed holder's slot frees itself. It works on Linux, macOS, the BSDs and Windows.

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
