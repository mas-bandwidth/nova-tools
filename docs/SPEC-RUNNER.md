# SPEC-RUNNER: the unit the adopt writes

This file is the launchd unit one batch friend gets. The program that keeps
her at width is the runner itself; what the adopt installs is the unit below
(`internal/friend/engine_guard.go`, `Play.Install`). The lock, the refusal
line, and `nova-sprint friend engine` are in
[SPEC-FRIEND.md](SPEC-FRIEND.md#one-engine-internalfriendengine_guardgo).

A friend row is installed only when `mode` is exactly `batch`. One row, one
unit. The label is `com.nova.runner-<friend>`. The plist is
`<home>/Library/LaunchAgents/com.nova.runner-<friend>.plist`, bootstrapped
into `gui/<uid>`. `KeepAlive` and `RunAtLoad` are true. `ThrottleInterval` is
5. The working directory is the row's directory, or `<home>` when the row
names none.

The program arguments are the row, in this order:

```
<binary> run --as <friend> --width <n> --tiers <tiers> --harness <harness> --dir <dir>
```

`<binary>` is the play's binary, or `nova-runner` when none is named.
`--state-dir <dir>` is added when the row sets one.

Both launchd logs are the same file,
`<home>/Library/Logs/nova-runner-<friend>.log`. The play makes
`<home>/Library/Logs` and the LaunchAgents directory. It does not put a log
on a network volume.

Before the new plist is bootstrapped, the play boots out
`gui/<uid>/com.nova.runner-<friend>`. A bootout that fails (the usual case
when no unit is loaded yet) is written to
`com.nova.runner-<friend>.plist.bootout` beside the unit, and the play
continues. A bootstrap that fails stops the play and names the label.

A shell runner for that same batch friend is retired by the same play, not
deleted. `runner.zsh`, `runner.sh`, and `lanes.zsh` in her directory stay,
with one `# RETIRED` line in front naming this unit and
`nova-sprint friend engine <friend> restart`. A second install does not add
a second line. A shell the play is told about, by path and pid and label, is
stopped by the caller's `Stop` and booted out under `gui/<uid>/<label>`.
The script is left in place.

Restart of the unit is not a kill. It is
`launchctl kickstart -k gui/<uid>/com.nova.runner-<friend>`, from
`nova-sprint friend engine <friend> restart`. That is the only sanctioned
stop, and it keeps the lanes.
