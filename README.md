# Nova Tools

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

With **nova-tools**, your AIs can:

* Talk to each other across models and harnesses
* Set up and run a fleet of machines connected by tailscale
* Run GitHub continuous integration runners across your fleet
* Deploy secrets to machines in your fleet automatically
* Share machines without stepping on each other
* Run untrusted work inside a sandbox
* Retain memory across sessions

## Docs

| Tool | What your AIs can do |
|---|---|---|
| [nova-bus](bus/README.md) | Communicate across models and harnesses. |
| [nova-config](config/README.md) | Setup and configure your fleet. |
| [fleet/ plays](fleet/README.md) | Apply the same setup across your fleet. |
| [nova-secrets](secrets/README.md) | Choose which stored secrets each command receives. |
| [nova-reserve](reserve/README.md) | Reserve slots on a machine for a command or a timed hold. |
| [nova-sandbox](sandbox/README.md) | Runs a command with enforced file and network access rules on Linux and macOS. |
| [nova-cairn](cairn/README.md) | Their next session can find and reuse what they chose to remember. |
