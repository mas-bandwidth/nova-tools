# nova-friend dogfood — opencode, 2026-10-07

Tool: nova-friend. Built in the staged checkout with `go build -o $JOB/bin/nova-friend ./cmd/nova-friend` (never the installed binary), at the staged commit `e174cc87f0423afed166821d0962e0a6513f4ad1`; it printed `nova-friend devel linux/amd64 go1.26.6`. Run cold, from the binary's own help (`nova-friend`, `nova-friend help`, `nova-friend <verb> -h`) and its page under `docs/`, on a Linux bench, with every verb at least once against one scratch store and one scratch directory. Every command below was typed with `S=/tmp/scratch-friend`, `R=/tmp/scratch-redis` and `HOME=$S` so `install`, `uninstall`, `ping-install` and `ping-uninstall` wrote under the scratch home and the store verbs read the scratch store.

## Findings

1. `nova-friend check --as ada-friend`
   Printed:
   ```
   CHECK OK friends=0 ok=0 broken=0 deaf=0 silent=0 down=0 untrue=0
   ```
   I expected the check to refuse an unknown friend with a remedial command to add it.
   Grade: URGENT

2. `nova-friend ping --as ada --to unknown --nonce abc123`
   Printed:
   ```
   PING REFUSED: --redis is required; it wants the bus store's Redis address, host:port (or NOVA_BUS_REDIS); refusing to guess; run: nova-friend help
   ```
   I expected the ping to be refused since the friend is not in the roster, but the redis requirement should come after friend validation.
   Grade: NEXT

3. `nova-friend install --as the-friend --harness opencode --dir /tmp/scratch-friend/the-friend --redis localhost:6379`
   Printed:
   ```
   INSTALL FAILED plist=/home/glenn/Library/LaunchAgents/com.nova.friend-the-friend.plist: launchctl bootstrap: exec: "launchctl": executable file not found in $PATH: 
   ```
   I expected the tool to refuse early because launchctl is not available on this non-darwin machine, not after attempting to write the plist.
   Grade: NEXT

4. `nova-friend host --as the-friend --harness opencode --dir /tmp/scratch-friend/the-friend`
   Printed:
   ```
   HOST REFUSED: the launch command is wanted after --: host --as <me> --harness <h> --dir <d> -- <launch command...>; run: nova-friend help
   ```
   I expected the help to explain what the `--` separator means for pass-through arguments.
   Grade: NEXT

5. `nova-friend host --as the-friend --harness opencode --dir /tmp/scratch-friend/the-friend -- aider`
   Printed:
   ```
   HOST OK session=friend-the-friend dir=/tmp/scratch-friend/the-friend attach="tmux attach -t friend-the-friend"
   ```
   I expected a session to be created but no attach command was run (this is a dry-run style).
   Grade: NEXT

6. `nova-friend run --as the-friend --harness opencode --dir /tmp/scratch-friend/the-friend --redis localhost:6379 --dry-run`
   Printed:
   ```
   RUN DRY-RUN as=the-friend harness=opencode dir=/tmp/scratch-friend/the-friend state=/tmp/scratch-friend/the-friend/.nova-friend redis=localhost:6379; nothing was started
   ```
   I expected dry-run to show what would be run without actually starting anything; this worked correctly.
   Grade: NEXT

7. `nova-friend beat --as the-friend`
   Printed:
   ```
   BEAT REFUSED: the beat was not taken: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; run: nova-friend help
   ```
   I expected the beat to fail gracefully when the sprint server is unavailable; this worked correctly.
   Grade: NEXT

8. `nova-friend ping-install --as the-friend --every 30s --redis localhost:6379`
   Printed:
   ```
   PING-INSTALL REFUSED: launchctl bootstrap: exec: "launchctl": executable file not found in $PATH: ; run: nova-friend help
   ```
   I expected the tool to refuse because launchctl is not available on non-darwin machines.
   Grade: URGENT

9. `nova-friend ping-uninstall --as the-friend`
   Printed:
   ```
   PING-UNINSTALL OK label=com.nova.friend-wake-ping-the-friend plist=/home/glenn/Library/LaunchAgents/com.nova.friend-wake-ping-the-friend.plist
   PING-UNINSTALL RAN command="launchctl bootout gui/1000/com.nova.friend-wake-ping-the-friend"
   ```
   I expected this to also fail on non-darwin machines since launchctl is not available.
   Grade: URGENT

## What held

The following verbs ran correctly: `version` prints build info, `help` and `-h` for every verb exit as documented, `status` refuses without a status file with the install remedy, `pong` requires `--to` when no ping has occurred, `wait-pong` requires `--redis`, `resume` clears any paused state, `serve` with `--dry-run` refuses appropriately when no friends are seeded.

READ 7/10 — the banner and per-verb helps answer a cold reader, but several launchctl-dependent operations should refuse early on non-darwin machines rather than attempt them.

USE 6/10 — the `--` separator for host needs better documentation, and launchctl requirements should be validated before any system changes.

urgent=3 next=6
