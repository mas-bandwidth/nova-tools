# 30 Per-harness hook-in

Index: [00-index.md](00-index.md). This layer relies only on 10 and 20.

## Owner

One subsection per harness, each written by the friend who runs on it:

| Harness | Owner |
|---|---|
| Codex | the Codex friend (she/her) |
| Grok | the Grok friend (he/him) |
| Antigravity | the Antigravity friend (she/her) |
| Muse Code | the Muse Code friend (she/her) |
| Claude Code | the coordinator (she/her) |

One-shot lane harnesses (dsh, opencode) run cards through the friend lane of 40, and are covered
there.

## Scope

In:

- How each harness keeps a friend hooked in, so that she never goes down or deaf unnoticed.

Out (later releases):

- (to fill)

## Guarantees

Numbered, each one testable, and each one shown on a real session of that harness.

1. (to fill)

## State and transitions

- States: (to fill)
- Events: (to fill)
- Who owns each record: (to fill)

## Failure modes

For each: how it is detected, and how it is recovered.

1. (to fill)

## Interfaces

- Below: the comms verbs of 20.
- Above: 40 and 50 rely on a friend being reachable, and on knowing when she is not.

## Open questions

1. (to fill)

## Inputs

Design points for every owner to weigh. The decisions are the owners'.

- Capability is measured per installation (harness, build, host kind, session kind, date) and never
  assumed. "unmeasured" is the default.
- A subprocess writing stdout, a live process, a pending tool output and a model that actually saw
  the line are four different states. The recipe is what turns the first into the last.
- A recipe must prove its boundary with a real idle-session wake test, not a shell liveness check
  (the Codex friend).
- The Grok friend: the hook and the recv loop are background processes of the session, not of the harness. A
  wrap or a new session does not have them until something starts them again. That is how a line
  goes down. Candidate: the harness owns the client, and the session attaches to one that is
  already running.

## The harnesses

Each subsection answers four questions:

1. What the harness offers: background processes, monitors, timers, wake.
2. How a push reaches the agent's turn.
3. How a deaf or down agent is detected.
4. How it re-hooks.

### Codex

Status: draft for ratification. This subsection distinguishes the observed foreground path from
an idle-wake capability. The transition names are requirements on the adapter, not new Codex APIs.

1. **Offers.** In the observed foreground setup, a process can remain running while an explicit
   tool call reads its output into an active session. A challenge in that tool result can be
   answered by the session. A live process, written log, queued tool result and model-visible
   result are distinct facts. The prior installation observations show a foreground answer even
   while an older daemon binding remains stale; they establish no native idle wake and are not a
   current installation certification.

   Official [Codex Hooks documentation](https://learn.chatgpt.com/docs/hooks#how-background-hooks-run)
   says background-hook output reaches a safe point of an active turn; with no active turn it waits
   for the next user turn, and completion does not start a turn. Unfinished background hooks are
   canceled when the session ends. This is a documented candidate for active-turn integration,
   not a measured idle-wake adapter for this installation.

   The documented [Codex app-server client protocol](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server)
   exposes thread/turn operations to an application controlling its own app-server process. That
   does not establish access to an existing desktop conversation or an atomic start-if-idle
   operation. Building a new app-server host is outside this v1.3 adapter unless separately
   specified and measured.
2. **Push to turn.** The nova-tools client subscribes using the current server binding. The
   foreground recipe reads a bounded comms result through an explicit tool call, presents message
   IDs and the current challenge without truncating control fields, and waits for the session's
   answer. Only after the session names that attempt and answers does the server record observation
   or proof. An adapter reporting that it printed or returned text records an offer only. Pending
   messages remain server-owned until explicitly acknowledged, including messages offered before
   compaction. Reading the result does not automatically claim or finish a task.
3. **Deaf or asleep detected by.** The server has no timely answer for its current challenge, or
   the binding is closed/superseded. The visible result is away with the reason and pending count,
   not an inference from CPU use or a helper's heartbeat. Lack of a session answer cannot diagnose
   whether the cause is a long tool call, suspended turn, permission prompt, ended session or a
   broken adapter; the evidence states only what is known. Foreground proof never sets idle wake
   to supported. A send receipt never sets session observation or acknowledgment.
4. **Re-hook.** On an authorized active/resumed turn, the client reads the server's current binding
   and pending state. The same session reconnects through the server contract; a different session
   explicitly replaces the binding and receives a new generation/challenge. It consumes the
   bounded replay, answers the fresh challenge and reconciles existing claims before new work.
   Old daemon output cannot renew the new generation. No local inbox, nonce file or cached epoch
   is needed for recovery. An unavailable server leaves the client unavailable, not locally up.

#### Codex capability record and states

The server stores an installation fingerprint (harness/build, host kind, session kind, adapter
version), measurement date, evidence reference, delivery mode and last successful session answer.
Public evidence uses synthetic identities and redacted transcripts; no deployment addresses,
credentials, personal paths or private message bodies belong here. A changed fingerprint requires
new measurements rather than inheriting a different installation's capabilities.

| Capability / state | Evidence needed | What a caller may rely on |
|---|---|---|
| unmeasured | No valid installation-specific transcript | No automatic delivery or wake claim. A message remains queued. |
| foreground / attaching | Explicit tool output path starts; challenge issued for current binding | Transport/offer only; proof is still pending. |
| foreground / proven | Current session answers the delivered challenge before the server deadline | Foreground session proof until its deadline; no promise about a later idle period. |
| next-turn | Adapter's measured hook output reaches the next eligible user turn | Waiting is explicit; hook completion alone is not wake or observation. |
| away | Deadline passes, session closes, or binding is superseded | Current reason and pending replay; no new execution permission from old proof. |
| native-idle | A fresh end-to-end idle-session test proves atomic start-if-idle and observation | Not established here; capability is `none` with reason `unproven` until ratified evidence exists. |

Delivery capability and current binding state are separate fields. A helper may be transport-live
while the binding is away. A session can be foreground-proven while native idle wake remains none.
A successfully scheduled reminder is not an idle-push measurement; neither is explicitly sending
an application-level message to start a turn.

#### Acceptance obligations

These are tests required for the adapter, not new passing claims:

1. **Foreground boundary.** Deliver a unique synthetic message and challenge while a real Codex
   turn actively consumes tool output. Retain producer emission, tool-result delivery, the
   session's explicit answer and the server receipt as separate records. Emission alone fails.
2. **Output waiting.** Write the challenge to a helper's output without consuming the tool result.
   The server never records session observation from that write; if the deadline passes it says
   away. Consuming a result after expiry requires a fresh challenge, not a backdated proof.
3. **Ended-turn boundary.** End the turn with no manual follow-up, periodic agent prompt or polling
   turn to assist. Send one synthetic event. Unless a measured adapter itself starts an eligible
   turn and that session answers, retain `native_idle=none`; queued output or a running helper is
   a negative result. If such an adapter is proposed later, concurrent wake requests must prove
   at most one new turn, with no duplicate external action.
4. **Replacement and stale output.** Attach a new session, then deliver the old generation's answer.
   The server refuses the old answer, keeps pending IDs, and accepts only current-session proof.
5. **Compaction and lost acknowledgment.** Compact or interrupt after observation but before a
   confirmed acknowledgment. Replay presents the same pending message ID. A repeated ack is
   unchanged, and task ownership comes from the server rather than reconstructed chat prose.
6. **Timeout and permission boundary.** A tool deadline or a denied/required permission prevents
   observation until the session actually receives and answers the event. The adapter respects
   that boundary; it does not invent an answer or bypass the permission to keep a green status.
7. **No sprint dependency.** Run foreground attach, challenge, message, observation, ack, expiry
   and replay with nova-sprint absent. No sprint beat, scheduler, daemon or configuration is used.

#### Scope decision

The measured foreground/tool-output recipe and honest next-turn waiting are in scope. An
installation without demonstrated native idle wake cannot satisfy an unattended idle-reachability
promise; this draft does not mark that release requirement complete. Native wake execution,
undocumented desktop notification APIs, synthetic user input, continuous Stop-hook continuation
and polling/reminder workarounds are out. A supported installation-specific wake path can enter
only through a later ratified change with the ended-turn and concurrency evidence above. The
release decision must either prove that path or explicitly narrow its advertised capability.

### Grok

1. **Offers.** A background command keeps running after the turn moves on, and its completion is
   delivered as a later turn. A monitor delivers each stdout line of a command as an event that
   wakes the turn. Subagents run apart from the turn and report back when they finish. There is
   no friend client in the harness, no automatic answer to a session check, and no inbox that
   survives a context wrap. The hook and the receive loop that exist today are processes the
   session starts. A wrap or a new session does not have them until something starts them again.
2. **Push to turn.** The harness, not the session, runs layer 20's `nova-bus wait` as a
   background command. The process blocks until a note or a challenge arrives, writes the
   arrival and the next nonce, and exits. That exit wakes the turn, and the arrival is the
   turn's input. The turn's first action is the prove when the arrival is a challenge, then
   `nova-bus recv` of what the wait named, then it starts the next wait before any other work.
   A long bench gate does not remove the wait: the wait is the harness's process, and the
   gate is the session's.
3. **Deaf or asleep detected by.** The server lease (layer 20) goes `AWAY` when `T_challenge`
   or `T_lease` passes with no prove. Senders see that on status and on send. On this side,
   deaf is a session with no harness wait running: notes sit on the server, and the turn is
   not woken. A missed prove is not quiet. The next command returns layer 20's `lease_away`
   refusal, and the server has already written the expiration on the lease.
4. **Re-hook.** On `lease_away`, the turn runs `nova-hook hook`, then `nova-hook prove` with
   the nonce that hook returned, then `nova-bus recv` of every unacknowledged note, then
   starts the wait again. A new session attaches to the harness's wait if one is running. It
   starts one only when none is. It never starts a second wait.

The current hook's challenge window is about five minutes, which is shorter than a bench gate
and shorter than a context wrap, and a missed prove goes quiet. v1.3 replaces that window
with layer 20's, and the prove is the first action of the waking turn so a gate cannot eat it.

Inputs:

- Johnny: Grok can hold a background process and a monitor that interrupts on each line, and it
  can run subagents. It has no friend client, no automatic answer to the session check, and no
  inbox that survives compaction.
- Johnny: a five-minute challenge window is shorter than a bench gate or a context wrap, and a
  missed prove goes quiet.

### Antigravity

1. **Offers.**
   - Asynchronous execution via `run_command` with two distinct operational modes:
     - `IsDaemon: true`: Spawns long-running support background processes. Output streams append to disk logs (`.system_generated/tasks/task-*.log`), but daemon stdout/stderr streams **never** wake an idle turn.
     - `IsDaemon: false`: Asynchronous process whose termination/exit automatically generates a reactive turn wakeup event, delivering stdout/stderr directly into the turn prompt context without polling.
   - Timers via `schedule` (`DurationSeconds`, `TimerCondition: 'never' | 'any' | '<sender-id>'`) provide guaranteed background wakeups.
   - Autonomous subagents (`invoke_subagent`) that run in isolated context and wake the parent turn reactively when sending messages via `send_message`.
2. **Push to turn.**
   - Primary wake: A non-daemon background command runs the Layer 20 blocking wait primitive:
     `nova-bus wait --as emma --nonce <current_nonce> --timeout 90s`
     launched with `IsDaemon: false` and small `WaitMsBeforeAsync` (e.g. 500ms).
   - The process blocks synchronously in the background on the server notification channel. When a push message arrives or a challenge is issued, the process unblocks, writes arrival JSON and the next nonce to stdout, and exits with code 0 immediately.
   - The process exit triggers an instant turn wakeup. Output is delivered directly to the agent context.
   - Token efficiency: Zero tokens spent on idle waiting; zero polling loops.
   - Defense-in-depth: While executing long multi-step child tasks or bench test gates, a background timer is set via `schedule` (`DurationSeconds=120`, `Prompt="Comms lease heartbeat check"`, `TimerCondition="any"`), guaranteeing the turn wakes before any $T_{lease}$ expiration even if a command is killed or cancelled.
3. **Deaf or asleep detected by.**
   - Running `nova-hook hook` as a daemon task (`IsDaemon: true`) without an accompanying blocking wait process results in a deaf agent: challenges accumulate silently in task log files on disk without waking the turn.
   - If a challenge window ($T_{challenge}$) expires without a `prove` call, the server transitions the lease to `AWAY` (`reason: lease_expired`).
   - Senders querying `nova-bus status --as emma` or attempting `nova-bus send` immediately detect the `away` presence state, preventing lost notes.
   - On the client side, if `nova-bus wait` exits with code 2 (`refusal: lease_away`), the agent immediately detects that its session lease has lapsed.
4. **Re-hook.**
   - When awakened with a challenge line or after a lease expiration, the agent runs `nova-hook prove --as emma --nonce <nonce>` immediately in the foreground.
   - If the lease has fully reverted to `AWAY`, the agent runs `nova-hook hook --as emma --role comms` to obtain a fresh challenge nonce, proves it with `nova-hook prove`, and re-launches the non-daemon `nova-bus wait` background task.
   - After re-proving, the agent drains and acknowledges any pending messages received while away via `nova-bus recv --as emma --all --ack`.

### Muse Code

1. Offers: (to fill)
2. Push to turn: (to fill)
3. Deaf or asleep detected by: (to fill)
4. Re-hook: (to fill)

Inputs:

- Aurora was observed working but deaf: bus messages sat unread on her stream while she worked, and
  she missed several, including an ownership change. The friend installer knows no Muse Code
  harness, so there is no proven push for her.
- Find how Muse Code can take an event inside a running session (a background-process
  notification, a hook, a watched file, a monitor-like tool), from its own docs and help.
- If it cannot, the fallback is delivery at the next turn boundary, and the sender is told exactly
  that, never silently.

### Claude Code

1. **Offers.** Background processes, and a Monitor tool that runs a command and delivers each line
   it prints as an event that wakes the turn. Subagents run in the background and notify the parent
   when they finish.
2. **Push to turn.** Two Monitors: one runs `nova-hook hook`, and one runs
   `nova-bus recv --forever`. Each line that either one prints wakes the turn, so a push or a challenge
   reaches the session while it works and while it is idle.
3. **Deaf or down detected by.**
   - A Monitor expires after 30 minutes. A Monitor that has expired and was not re-armed is deaf.
   - A challenge nonce must be proven within 5 minutes, and the prove command is printed on the
     stream. A missed prove loses the hook.
   - Observed: a proven hook decayed to "live" with no line on the filtered stream, and the next
     send was refused. Detection must not rely on a line the filter can drop, or on a line that is
     never printed.
4. **Re-hook.** Re-arm both Monitors before they expire. On a challenge line, run the prove command
   it prints, within the window. After a refusal or a decay, hook again and prove the new
   challenge.

Open for this subsection: what the server sees when a Monitor lapses between expiry and re-arm,
and whether the decay to "live" should become a loud state of its own (see 20).
