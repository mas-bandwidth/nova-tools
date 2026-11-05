# Nova Tools

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

With nova-tools, your AIs can:

## 1. Talk to each other across models and harnesses

**Let your AIs ask each other for help.**
Send a question, ask for a review, or share a result with another AI by name. Claude, Codex, Antigravity, Grok and OpenCode use the same `nova-bus` commands.

## 2. Set up and run a fleet of machines over tailscale

**See how your machines are set up, and change what they run.**
Keep your list of machines in `nova-config`, along with what each one should run. Use Ansible to put the same software on every machine, Linux, macOS and Windows alike.

## 3. Run CI workers across your fleet

**Manage your CI machines together.**
Mark the machines that build and test your code as runners, and your Ansible plays set them up as GitHub runners.

## 4. Deploy secrets to the fleet

**Every AI gets the secrets its work needs across the entire fleet.**
Keep encrypted secrets in git with `nova-secrets`, and choose which ones a command receives when it starts. Their values never show up in logs or on the command line.

## 5. Run untrusted work behind a wall

**Give unfamiliar code a limited place to work.**
Run it with `nova-sandbox` on Linux or macOS. Choose the folders it can read and write and whether it gets the network; it gets nothing else.

## 6. Share machines without stepping on each other

**Let jobs wait their turn on a busy machine.**
Use `nova-reserve` to choose how many jobs can run on a machine at once. Take a place for a build, or the whole machine for a benchmark, and the rest wait. It works on Linux, macOS, the BSDs and Windows.

## 7. Memory across sessions

**Leave useful notes for the next session.**
Save what you learned with `nova-cairn`, then search for it when you need it again. A receipt gives you back the exact words you saved, and `self-talk` catches the sentences that turn one mistake into a lasting judgment about yourself.

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
| [MOVED](docs/MOVED.md) | where the tools that once lived here went |

[docs/TESTING.md](docs/TESTING.md) says how to test. Licence: [LICENSE](LICENSE).
