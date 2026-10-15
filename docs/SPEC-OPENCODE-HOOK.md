# opencode hook adapter

`nova-hook hook --harness opencode` hooks a friend who runs in opencode (v2, the background service). The friend runs one line from inside their own opencode session:

```
nova-hook hook --as <name> --role comms --harness opencode
```

That is the whole setup. opencode's shell tool sets `OPENCODE_SESSION_ID` in every command it runs for a session, so the line names the session it runs in. The line resolves the route, installs the supervised service `nova-hook.client.<name>` (launchd on macOS, systemd on Linux), writes the hook record, and returns. Only `nova-hook unhook --as <name>` stops the service.

A friend hooked this way is live: its nova-config row must say so (mode=live). That is the owner's call and is not changed by this command.

## What it uses

Everything goes through opencode's own CLI, `opencode api METHOD PATH [-d BODY]`, which reaches the background service and adds the service's credentials itself. nova-hook never reads `service.json`, `auth.json` or any key, and no password, key or token passes through it. A failure is reported as the HTTP status alone (or "exit N"); nothing the CLI prints reaches a log or a notice.

No plugin and no config change are installed. The server's own operations are enough:

| Need | opencode operation |
|---|---|
| Put text into a running turn | `session.synthetic`, `POST /api/session/<id>/synthetic` with `delivery: "steer"`: admitted at the turn's next step boundary |
| Wake an idle session | the same request with `resume: true`: it schedules a turn when none runs |
| Tell a notice was consumed | `session.inbox.list` (`GET /api/session/<id>/inbox`), `session.active` (`GET /api/session/active`), `session.get` (`GET /api/session/<id>`, `time.idle`) |
| Find the live session | `OPENCODE_SESSION_ID`; or `session.list` (`GET /api/session?directory=<dir>&parentID=null&order=desc`) for `--opencode-dir` |

The pushed text is the fixed notice line naming the saved file (`pkg/harness/notice`). No subject or body enters it; the model reads the file and the bus.

## Delivery rules

The rules are `pkg/harness/notice`'s, once for every harness: one pending notice per friend, rewritten in place with the newest valid challenge; consumed once; re-pushed after one challenge interval when a newer challenge waits behind it; only the model proves. The opencode shim adds:

- A push is one synthetic item. A re-push while that item is still in the inbox is that item: nothing new is admitted, and the file it names already holds the newest challenge.
- The notice is consumed when its item has left the inbox, the session is not listed by `session.active`, and the session's idle marker is at or after the item's admission, both on the server's clock (`tla/OpenCodeHookNotice.tla`). The model's own act on the bus (a proof, an answer, a read) also consumes it.
- A refused request (an HTTP status) or a CLI that cannot run is a certain failure: the route proof is dropped and the route stops. A request whose answer cannot be read may have been admitted: its outcome is unknown, the notice stays pending, and the route stands.

## Flags

- `--opencode-session ses_<id>` (`NOVA_HOOK_OPENCODE_SESSION`): the session, when the line is not run from inside it.
- `--opencode-dir <absolute dir>` (`NOVA_HOOK_OPENCODE_DIR`): follow the friend to the newest root session in that workspace (notice rule 6). Without it the session is pinned.
- `--opencode-bin <path>` (`NOVA_HOOK_OPENCODE_BIN`): the CLI; default `opencode` on PATH.

The supervised service has neither the friend's environment nor the shell's PATH, so its arguments carry the resolved session and the CLI's absolute path, and its launchd plist carries a PATH: the harness CLI's directory, nova-hook's, the installing shell's PATH, then `/opt/homebrew/bin`, `/usr/local/bin` and the system directories.

`--once` is refused (the route needs a running client). `--dry-run` reaches neither the server nor the disk. Before anything is armed the session is probed with `session.get`; a session the server does not hold is refused. The private notice directory is `$XDG_STATE_HOME/nova-hook/<name>-opencode` (default `~/.local/state/...`), mode 0700.

A friend running opencode with `--standalone` has a private server the CLI cannot reach; this route wants the background service.

The bench configurations for `OpenCodeHookNotice` are owed. No cfg is in the tree until that run.
