# Nova Hook for Muse

A Muse plugin bundle with one hook: after each tool call (`PostToolUse`),
`nova-hook pending` prints the one pending notice line the running
`nova-hook hook --harness Muse` left for the name (pkg/harness/notice), so
the newest `HOOK CHALLENGE` reaches the model inside its turn instead of
waiting for the turn to end. The line names a saved file that always holds
the newest current challenge and the message count. The verb only reads: it
discards stdin, always exits 0, and never proves, sends, acks or writes.

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

Wake: `wake_delay_ms` has no persistent store in Muse, so the install
stores nothing for wake. Checked: `muse plugins validate` reports manifest
fields `wake_delay_ms` and capability `monitors` as "not used by this
runtime", and `muse -c wake_delay_ms=0` refuses with "unknown key: no such
setting"; the plugin manifest's capabilities are only skills, hooks,
mcp_servers, commands and reminders. The install's HOOK STATE line therefore
spells out the exact remedy: start both Monitors with `wake_delay_ms=0`:

- `Monitor(command="nova-hook hook --as <name> --role <role> --harness Muse", wake_delay_ms=0)`
- `Monitor(command="nova-bus recv --as <name> --forever --exec '<deliver-into-session>'", wake_delay_ms=0)`

Without `wake_delay_ms=0` on both, Muse batches idle notifications and
the friend silently goes down when idle.

Hook knob: `NOVA_HOOK_AS` (default `aurora`).

## The 3-minute long-turn proof

After a real install, start both of the friend's Monitors with `wake_delay_ms=0` (above),
begin a turn that runs past three minutes, and have the heartbeat challenge
the name mid-turn: the `PostToolUse` hook prints the pending
`HOOK CHALLENGE` line inside the turn, the model answers it with
`nova-hook prove` before the turn ends, and `nova-hook status --as <name>`
shows the push proven without the turn ever having stopped.
