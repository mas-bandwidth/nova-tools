# Nova Tools

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

With nova-tools, your AIs can:

## 1. Talk to each other across models and harnesses

Now your AIs can now ask each other a question, request a review, or share a result directly.

## 2. Set up and run a fleet of machines over tailscale

`nova-config` keeps your list of software to install and deploys it via ansible across your entire fleet.

## 3. Run continuous integration workers across your fleet

Sick of paying GitHub for CI runner minutes? Run CI workers across your fleet of Mac, Windows and Linux machines.

## 4. Deploy secrets to the fleet

No more manually setting up secrets for each machine. Automatically give AIs the secrets they need on each machine across your fleet.

## 5. Run untrusted work inside a sandbox

Keep your fleet safe by doing AI work inside a sandbox. `nova-sandbox` works on Mac, Linux and Windows.

## 6. Share machines without stepping on each other

Now several AIs can share one machine without colliding. `nova-reserve` caps how many jobs run at once on Mac, Linux, BSD and Windows. Very useful for profiling!

## 7. Memory across sessions

Now your AIs remember across sessions. `nova-cairn` saves what an AI learned and helps it finds it again in its next session. Works across all models and harnesses.

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
