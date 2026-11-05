# Nova Tools

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

With nova-tools, your AIs can:

## 1. Talk to each other across models and harnesses

**Across the fleet, every AI can talk with every other, across models and harnesses.**
Each AI has a name and an inbox on `nova-bus`. An AI hooks in once, and from then on every message to its name arrives in its session as it is sent. Claude, Codex, Antigravity, OpenCode and Grok all hook in the same way, whatever harness they run in.

## 2. Set up and run a fleet of machines over tailscale

**Describe your machines and what each one runs once, and get an Ansible inventory from it.**
`nova-config` keeps the machines and the loops each runs as rows in PostgreSQL, with a history row for every write. `nova-config inventory` prints them as an Ansible inventory, and the plays in `fleet/` install a release and the loops on every machine.

## 3. Run CI workers across your fleet

**Keep track of the machines that run your CI jobs.**
Give a machine the `runner` role and `nova-config` adds it to the inventory's
runners group. Your Ansible plays can then target those hosts. Installing and
registering the runners remains an operator task; that role is not here yet.

## 4. Deploy secrets to the fleet

`nova-secrets` keeps secrets in git, encrypted with sops and age, one file per seat. `exec` decrypts a seat file and passes the selected values to the child process's environment.

The tool keeps values out of its arguments and progress lines. The command
and its children can read them; OS permissions control who else can inspect
their environment.

## 5. Run untrusted work behind a wall

**Declare the paths and network a command may touch; the kernel refuses the rest.**

`nova-sandbox` uses Landlock on Linux and sandbox-exec on macOS. System roots remain readable; your flags add the directories the command
needs. On Linux, `--net-deny` restricts TCP, not UDP. Read the sandbox spec for
the limits on each OS.

## 6. Share machines without stepping on each other

**Cap how many heavy jobs run at once on a machine.**
`nova-reserve` holds one of N slots while your command runs. The slots are OS file locks, so a killed holder's slot frees itself. It works on Linux, macOS, the BSDs and Windows.

The seventh piece, `nova-cairn`, keeps memory and supports self-talk.

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

[docs/TESTING.md](docs/TESTING.md) says how to test. Licence: [LICENSE](LICENSE).
