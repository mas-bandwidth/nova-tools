# Nova Hook for Muse

A Muse plugin bundle with one hook: after each tool call (`PostToolUse`),
`nova-hook pending` runs `nova-bus peek --as <name>` and prints the
pending bus lines, one per line, so a `HOOK CHALLENGE` reaches the model
inside its turn instead of waiting for the turn to end. The verb only
reads: it discards stdin, always exits 0, and never proves, sends, acks or
writes.

The model proves. A challenge is answered only by the model, from inside its
own turn, with the `nova-hook prove --as <name> --nonce <n>` line the
challenge carries. No hook, daemon, loop or script may call `prove`: the
answer is the proof that the model itself read the push.

## Install

```sh
nova-hook hook --as <name> --role <role> --harness Muse
```

Before arming, this installs the bundle with `muse plugins install
<bundle>` and approves its one hook capability with `muse plugins approve
nova-hook:hook:pending-challenges`. `--muse-bundle <dir>` (or
`NOVA_HOOK_MUSE_BUNDLE`) names the bundle; by default it is `muse-bundle`
beside the `nova-hook` binary. Validate any bundle first with
`muse plugins validate <bundle> --json`.

Wake: `wake_delay` is a per-Monitor setting (`wake_delay_ms`) with no
persistent store, so pass `wake_delay_ms=0` on each Monitor call for the
friend; the install prints this line and stores nothing.

Hook knobs: `NOVA_BUS_BIN` (default: `nova-bus` on PATH, else
`$HOME/.local/bin/nova-bus`), `NOVA_HOOK_AS` (default `aurora`),
`NOVA_BUS_REDIS` (default `127.0.0.1:6381`).

## The 3-minute long-turn proof

After a real install, start the friend's Monitor with `wake_delay_ms=0`,
begin a turn that runs past three minutes, and have the heartbeat challenge
the name mid-turn: the `PostToolUse` hook prints the pending
`HOOK CHALLENGE` line inside the turn, the model answers it with
`nova-hook prove` before the turn ends, and `nova-hook status --as <name>`
shows the push proven without the turn ever having stopped.
