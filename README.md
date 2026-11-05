# Nova Tools

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

With nova-tools, your AIs can:

## 1. Talk to each other across models and harnesses

**Let your AIs ask each other for help.**
Send a question, ask for a review, or share a result with another AI by name. Claude, Codex, Antigravity, Grok and OpenCode use the same `nova-bus` commands.

## 2. Set up and run a fleet of machines over tailscale

**See how your machines are set up, and change what they run.**
Now your AIs can see every machine in the fleet and what each one runs, from one place. `nova-config` keeps the list, and Ansible puts the same software on every machine, Linux, macOS and Windows alike.

## 3. Run CI workers across your fleet

**Manage your CI machines together.**
Now your builds and tests run on your own machines. Mark the ones that do it as runners, and your Ansible plays set them up.

## 4. Deploy secrets to the fleet

**Every AI gets the secrets its work needs across the entire fleet.**
Now your AIs can use an API key or a token on any machine without ever seeing it in a prompt or a log. `nova-secrets` keeps secrets encrypted in git and hands each command only the ones it needs.

## 5. Run untrusted work behind a wall

**Give unfamiliar code a limited place to work.**
Now your AIs can run code they did not write without putting the rest of the machine at risk. `nova-sandbox` limits its access to files and the network, on Linux or macOS.

## 6. Share machines without stepping on each other

**Let jobs wait their turn on a busy machine.**
Now several AIs and people can share one machine without colliding. `nova-reserve` caps how many jobs run at once, on Linux, macOS, the BSDs and Windows.

## 7. Memory across sessions

**Leave useful notes for the next session.**
Now your AIs remember. `nova-cairn` saves what an AI learned and finds it again, word for word, in its next session.

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
