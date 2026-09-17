## Handoff

The manager shift is the coordinator's turn on a queue, and it ends by handoff and begins again
by takeover. `handoff --to <name>` ends the shift: it writes the `SHIFT END` line, stops the
loop, releases the `OWNER` lock (name, host, pid, since), writes a `HANDOFF` record (last
`WIDTH`, in-flight cards by bench, pending, escalations open, benches and state) and posts
one bus note to the successor carrying the record. It refuses mid-harvest — it finishes the
harvest first — and refuses when the successor is asleep by `nova-wake awake`, and then
prints `HANDOFF OK`. `takeover --as <name>` refuses when `OWNER` names a live process on a
reachable host (`TAKEOVER REFUSED owner=<name> pid=<n> host=<h>`); a stale lock is taken with
one `NOTE` line, then the loop and a manager shift start on the same queue and it prints
`TAKEOVER OK`. When nova-work is open, `handoff` also moves the coordinator ownership record
in the tree — generation, token, fencing, the `:handoff` event SPEC-WORK names.

The `OWNER` lock and the `HANDOFF` record live under `<root>/queue/` as files, both
tab-separated. `OWNER` is `name`, `host`, `pid`, `since`. `HANDOFF` is `to`, `from`,
`width` (the last `PULSE WIDTH` line), `in-flight` (cards by bench), `pending`, `escalations`,
`benches` and `state`.

The verbs are shipped (#509): `handoff` checks the successor awake by `nova-wake
awake --bus <clone>` and posts the record with one `nova-bus send`; the loop's state is
`<queue>/LOOP`, `stopped` by handoff and `running` by takeover; `--work <dir>` moves the
tree's ownership record beside the queue's, bumping its generation with a fresh token and
appending the `:handoff` event. Replays 26-31 walk every refusal and every count.
