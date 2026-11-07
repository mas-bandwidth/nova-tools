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

**CI rules, on by default.** `nova-fleet up --runners` puts job hooks on every runner it sets up, with runners on every
machine but the control node, sized to its cores ([fleet/README.md](fleet/README.md#ci-rules)). The hooks enforce two
things: a push runs CI only on `main`, `sprint/<stream>` and tags, and a push, pull request or merge group job is killed
at `github_runner_ci_limit_seconds` of job wall time (default 120). `schedule` and `workflow_dispatch` runs are exempt.
The hooks check nothing else. The rest is the rule repos follow so CI does not queue up and slow all work: per-commit
CI aims for 1 minute, with unit and functional jobs in parallel, and unit tests use no real time, sleep or network.

For more information, please ask your AI to [read the docs](docs/README.md).
