# DeepSeek Harness native wake adapter

The recipient-owned `nova-hook hook --harness deepseek` (alias `dsh`) queues
bounded saved-event JSON and immutable, locally authored notification records.
A native Cordis plugin targets exactly one existing live session, the one the
follow file names.
It uses the agent registry's `get(sessionId)`; it never creates or resumes a
session. No gateway credentials or HTTP gateway are involved.

## Setup in one line

`nova-hook hook --as <name> --role comms --harness dsh` is the whole setup:

- The harness home is `$DSH_HOME`, else `~/.dsh` (the harness's own
  `resolveDshHome`). The friend's workspace is the one in
  `storages/workspace.json` titled with her name (or whose directory is named
  so). Its sessions less `archivedSessionIds` are the candidates; a session
  whose `sessions/<project key>/<id>/session.lock` a process holds open (the
  harness's single-writer `flock`, read with `lsof`, never taken) is live. The
  newest live session wins, else the newest session.
- The events directory defaults to `~/.local/state/nova-hook/<name>-dsh`,
  created 0700; the include to `<dsh home>/<name>-hook/hooks.json`.
- The include is mounted into the desktop profile's patch layer,
  `<dsh home>/profiles/desktop/cordis.patch.yml`, as a loader insert of
  `cordis:include` with id `nova-hook-dsh-include`; an existing mount at the
  same path is left byte for byte, one at another path is repointed, and every
  other entry and comment is kept.
- When the include, its modules or the mount changed, the result prints one
  line, `restart DeepSeek Harness once to load the nova-hook plugin`: the
  harness reads its profile only at start.
- The supervised service `nova-hook.client.<name>` is installed as for every
  harness. It is given only the flags passed, so it rediscovers on start.

Following a new session needs no restart. The include names no session: the
receiver reads `<events dir>/session.json` (`{"version":1,"sessionId":...}`,
private) on each drain and on its change. The hook client rediscovers the
session once a `FollowEvery` and rewrites the file when it moved; a pending
notice is pushed once into the new session (pkg/harness/notice rule 6). A move to another session drops the old
session's ordinary hold (tla/DshNativeWake.tla `Retarget`, `HoldOnTarget`).

`--dsh-events-dir`, `--dsh-hooks-file` and `--dsh-session` (or
`NOVA_HOOK_DSH_EVENTS_DIR`, `NOVA_HOOK_DSH_HOOKS_FILE`,
`NOVA_HOOK_DSH_SESSION`) override each default; `--dsh-session` pins one
session and stops following. A given event directory must already be
canonical, owned by the current user and private (0700). The include path must
be an absolute `.json` file. `--once` is refused; `--dry-run` reads the harness
state and writes no include, module, mount, saved event or notification.

## The verified native contract

Install writes the native module, receiver module and a Cordis include list:
`[{"id":"nova-hook-dsh","name":"<absolute plugin module>","config":
{"eventsDir":"<private directory>"}}]`, and the follow file naming the
session. An include written by an earlier version also carries `sessionId`;
the receiver uses it only when no follow file exists.
Codex, Muse and DeepSeek routes are mutually exclusive.

The implementation contract is pinned to Harness 0.2.0-rc.2, source
`5e9e301dd9dc8923b2762f76dacfc5751f6ca851`: `dsh-agent` exposes live
`ctx.agents.get(sessionId)` and agent-scoped `agent/pre-step`; `dsh-agent-loop`
implements synchronous `followup` (next turn, wakes idle driver) and `steer`
(next step, wakes driver). `inject` does not wake. Identified input uses
the verified `dsh-llm` `createUserMessage` structure (fresh UUIDv4, role user,
structured clone and recursive freeze), with explicit
`source: {kind: 'plugin', plugin: 'nova-hook'}`. The runtime brand helper is identity; this dependency-free construction avoids
an unresolved bare import from a loose module outside the app archive.
External message bodies and nonces stay in the saved JSON; native text contains only a fixed notice and a
validated receiver path. Reading that fresh file supplies the nonce and proof
instructions without a bus read.

The former command-hook bridge could only run at tool/Stop boundaries.
Its `continue:false` output is ignored by this harness's actual hook protocol;
it could not wake an already idle session. This adapter uses native agent input
and does not install those command hooks.

## Queue and admission boundaries

A single recipient writer holds the event-directory route lock. Each saved
receiver and `.notices/notice-<id>.json` publication is immutable and atomic. The native
plugin owns one receiver lock and watches filesystem publication, with an initial
drain and an agent-created listener to cover startup races. It consumes individual
records; there is no shared read/modify/write pending summary to resurrect during
a producer/consumer race. A cold or disposed pinned target retains the queue and
logs the unavailable target; it does not guess another identity.

Ordinary records form one native summary while a prior ordinary inbox input is
pending. Further ordinary records wait. The loop removes claimed input before
`agent/pre-step`, even when a listener rejects or replaces it, so claiming that
input releases the ordinary hold in every middleware outcome. This is inbox
progress only; it does not claim the model read it or answered it.

Challenge priority requires the entire canonical challenge body, the active
store nonce hash and the current epoch. Stale nonces, lookalikes and wrong epochs
remain ordinary traffic. A new current challenge bypasses an ordinary inbox hold
with a new immutable receiver path and new native input. If multiple challenges
queued while cold, the newest queued challenge is surfaced. Ordinary work is
retained independently. The same timely-challenge rule applies to every adapter.

Before native input, records are renamed to `claimed-<id>.json`. On successful
synchronous admission they are removed. If native input throws, its outcome may
already be accepted: claimed records remain for inspection, never automatic
retry. A process crash can also leave a receiver lock or claimed records; the
operator must inspect the native inbox and saved events before clearing them.
Normal Cordis unload calls the returned idempotent disposer, closing the watcher,
releasing listeners and removing the receiver lock; forced exit cannot do this.
Native admission is not a durable model receipt. No daemon proves a challenge,
writes a bus answer, or treats queue consumption as acknowledgment.

`--dsh-since` retains its fixed UTC configuration boundary; it must never be
advanced on restart. This checkpoint does not implement a Redis history backfill
or prove 24-hour recovery coverage. Earlier work still needed must be resent.
Actual mounted-app busy/idle proof is a separate acceptance gate from source,
Linux executor tests and hosted CI.

## Validation

Go tests cover immutable publication, native include installation, canonical
current-challenge priority and no automatic proof/answer. On a Linux bench run
`node --test pkg/dshdesktop/testdata/receiver.test.mjs` to execute busy refresh,
true idle wake, ordinary batching, rejected/filtered inbox claims, concurrent
publication, consumed silence, cold target and ambiguous native admission.
