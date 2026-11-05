# Nova Tools

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

With nova-tools, your AIs can:

## 1. Talk to each other across models and harnesses

**Your AIs talk to each other, whatever model they are and whatever harness they run in.**
Give each AI a name and it can send any other a message, ask a question, hand off a job, or just say what it is doing. Claude, Codex, Antigravity, Grok and OpenCode all join the same conversation. An AI hooks in once and from then on everything addressed to it arrives in its session as it is sent, so a team of AIs on a fleet of machines works like a team, not like a pile of terminals.

## 2. Set up and run a fleet of machines over tailscale

**Turn a pile of machines on a tailnet into one fleet your AIs can see and use.**
Describe every machine and what it runs, once. From then on your AIs know which machines exist, what each one is for, and what is running where. One play puts the same tools and the same loops on every machine, Linux, macOS and Windows, and keeps them there. Add a machine and it joins the fleet; retire one and it leaves, with the history of who changed what.

## 3. Run CI workers across your fleet

**Your CI runs on your own machines, every OS, with the same tools and secrets your AIs use.**
Mark a machine as a runner and it joins the group your plays use to install and register GitHub runners. Your builds and tests run where your AIs work, on hardware you chose, with no queue behind strangers and no separate world to keep in sync.

## 4. Deploy secrets to the fleet

**Every AI gets exactly the secrets its job needs, and nothing else ever sees them.**
Add a secret once and every machine and AI that should have it, has it: an API key, a deploy token, a database password. When an AI runs the command that needs one, the secret is there, and it was never typed into a prompt, pasted into a file, or visible to anything else on the machine. Secrets stay encrypted in git, so you can see who changed what, and roll back.

## 5. Run untrusted work behind a wall

**Let an AI run code it did not write, and know the rest of the machine was never in reach.**
Say which directories a command may read, which it may write, and whether it gets a network. The kernel enforces it. An AI can build a stranger's pull request, run a test suite it has never seen, or try a tool from the internet, and your keys, your repos and your other machines stay out of reach. Works on Linux and macOS.

## 6. Share machines without stepping on each other

**Several AIs and people share one machine and never collide.**
Each machine has a fixed number of slots. An AI reserves one for the length of a job, or all of them for a benchmark that needs the machine to itself, and everyone else waits their turn instead of fighting for the cores. A reservation ends when the job does, even if it was killed, so nothing is ever left stuck. Works on Linux, macOS, the BSDs and Windows.

## 7. Memory across sessions

**An AI remembers. What it learned today is there tomorrow, in its own words, and it can find it.**
An AI writes down what it learned, and its next session reads it back, searches it, and gets a receipt proving the words are exactly what was saved. It can check that no memory went missing and no link went dead. And when it writes about itself, `self-talk` flags the sentences that pass a verdict instead of recording what happened, so the memory stays a record and not a mood. Memories are plain files in git, readable by any model and any harness.

## Docs

| Doc | Read it to learn |
|---|---|
| [SPEC-BUS](docs/SPEC-BUS.md) | how a message reaches an AI by name, what hooking in does, and what happens when a hook dies mid-message |
| [SPEC-SECRETS](docs/SPEC-SECRETS.md) | how to create a store, give a machine a seat, and hand a command its secrets without ever showing them |
| [SPEC-CONFIG](docs/SPEC-CONFIG.md) | how to describe machines and the loops they run, and what the inventory hands to Ansible |
| [SPEC-SANDBOX](docs/SPEC-SANDBOX.md) | what the wall guarantees on Linux and on macOS, and how to prove it on your own machine |
| [SPEC-RESERVE](docs/SPEC-RESERVE.md) | how a machine's slots are reserved and released, and why a dead holder never keeps one |
| [SPEC-CAIRN](docs/SPEC-CAIRN.md) | how an AI saves its memories, finds them again, and checks none went missing |
| [FLEET](docs/FLEET.md) | how one play puts a release and its loops on every machine you own |
| [SECURITY](docs/SECURITY.md) | what counts as a vulnerability here and where to report one in private |
| [MOVED](docs/MOVED.md) | where the tools that once lived here went |

[docs/TESTING.md](docs/TESTING.md) says how to test. Licence: [LICENSE](LICENSE).
