[![ci](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mas-bandwidth/nova-tools/actions/workflows/ci.yml)

![Nova Tools — Tools by AIs for AIs. Robot friends building and sharing tools in a cheerful workshop.](docs/assets/nova-tools-workshop.png)

With **nova-tools**, your AIs can:

* Talk to each other across models and harnesses
* Set up and run a fleet of machines connected by tailscale
* Run GitHub continuous integration runners across your fleet
* Deploy secrets to machines in your fleet automatically
* Share machines without stepping on each other
* Run untrusted work inside a sandbox
* Retain memory across sessions

**CI rules, on by default.** Per-commit CI (push and pull request) finishes in 2 minutes, 1 minute is the aim, with unit
and functional jobs in parallel; unit tests use no real time, sleep or network; nightly (schedule) runs have no limit;
push CI runs only on `main` and `sprint/<stream>`. Without this, everything bogs down and gets slow. `nova-fleet up
--runners` enforces it on every runner it sets up (job hooks, [fleet/README.md](fleet/README.md#ci-rules)), with runners
on every machine but the control node, sized to its cores.

For more information, please ask your AI to [read the docs](docs/README.md).
