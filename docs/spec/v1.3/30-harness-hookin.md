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

A one-shot friend (dsh, opencode: no live session between cards) has no hook-in: it is excluded
from the psychic link by definition (layer 20 guarantee 19) and runs as an ordinary fleet member
lane with an identity prompt (layer 40 guarantee 12). Nothing in this layer applies to it.

## Scope

In:

- How each harness keeps a friend hooked in, so that she never goes down or deaf unnoticed.

Out (later releases):

- Native idle wake on any harness whose subsection has not measured it end to end. An
  unmeasured or unproven wake stays out, and the sender is told delivery waits for the next
  turn boundary.

## Guarantees

Numbered, each one testable, and each one shown on a real session of that harness.

1. **A push reaches the turn by a measured path, or waits honestly.** Each subsection names
   how a push reaches the agent's turn on its harness. Where that path is measured, the push
   reaches the turn while the session works; where it is not, the sender is told delivery waits
   for the next turn boundary, never that an idle wake occurred. While the friend is unhooked
   that telling is the refusal of layer 20 guarantee 16: the send is refused at once, names her
   away reason and that delivery waits for her next hooked turn, and nothing is queued.
2. **Only the session's own answer proves.** A challenge is proven only when the session itself
   answers the current nonce (layers 10 and 20). A helper process staying alive, a line written
   to a stream, or an adapter printing a nonce records an offer only.
3. **Deaf or down is detected on the server lease, never inferred locally.** No prove inside
   the challenge window, or no renewal inside the lease, and the lease goes away with its
   reason recorded; senders see away and the pending count. A session looking busy is not
   evidence it can hear, and a helper still running is not evidence it is up.
4. **Re-hook replays before new work.** A session that re-hooks, resumes, or replaces a binding
   proves the fresh challenge, then drains every unacknowledged message in order and reconciles
   it against work already claimed, so a missed message is acted on once. No local nonce file,
   inbox, or cached state is kept to re-hook from.
5. **Capability is a measured record, per installation.** Link and wake are each recorded as
   measured or unmeasured for one installation (harness, build, host kind, session kind, date).
   Unmeasured is the default, foreground proof never sets idle wake to supported, and a changed
   installation fingerprint needs new measurements.

Guarantees 6 to 9 carry the psychic link of layer 20 (guarantees 12 to 18) into every harness,
from the project owner's rulings of 2026-10-14 (layer 20, Rationale).

6. **The link comes first; no work without it.** A session's first act is arming its link and
   answering the challenge that arrives through it. Until that proof lands, every bus verb for
   its name refuses `not_hooked` (layer 20 guarantee 12), and the friend takes no new work:
   layers 40 and 50 deal her none and let none of her lanes take one. The bus gate opens when
   the session proves the fresh challenge delivered through its link; the work gate opens only
   after a send-to-self arrives in-turn and is answered from that turn, recording a passed
   self-test for the current generation. A helper alive, a
   nonce printed, or a message queued records an offer only and opens no gate. Observation:
   before proof, send, recv and take are refused; after proof, send-to-self and recv succeed but
   take remains refused until that self-test is answered from the turn; after the self-test,
   take succeeds.
7. **One link recipe per harness, measured pushed-in-turn.** `nova-hook hook --as <name>
   --harness <h>` prints the one link recipe for each harness below (Codex, Grok, Antigravity,
   Muse Code, Claude Code), and that recipe is the remedy every `not_hooked` refusal names. Each
   recipe is measured by a functional test on a real session of its harness: a message sent while
   the session works lands in its turn through the recipe, and the challenge it carries is
   answered from that turn. Until that test passes, the capability record says the link is
   unmeasured (guarantee 5), and senders are told so.
8. **Losing the reader lapses the link, never silently.** Renewal challenges reach the session
   only through its link (layer 20 guarantee 14), and the lease is shorter than the reader's
   bound (a monitor's expiry; for a re-armed one-shot wait, the chain of waits). A reader that
   dies, expires or is not re-armed leaves the renewal unanswered, and the server unhooks the name
   within $T_{lease}$ with its reason recorded; re-arming re-proves. Observation: stop the reader
   of a hooked session; within $T_{lease}$ on the server clock the name reads unhooked, its next
   send is refused `not_hooked`, and a send to it is refused `friend_not_hooked`.
9. **The harness keeps nothing as truth.** The link's records (stream, challenge, nonce, lease,
   hooked state, acknowledgment) live on Redis, the one server (layer 10 guarantee 14). No
   recipe writes a wake file, nonce file, lease or presence copy, and a session that restarts
   recovers its link by asking the server and re-arming. Observation: delete the harness's
   runtime directory between two pushes; the next re-arm and prove succeed with no local input.

## State and transitions

- States: the per-installation capability record (link and wake, each measured or unmeasured),
  over the binding states of layer 10 (absent, challenged, proven, away, closed) as presented
  by the lease of layer 20. A session is foreground-proven only while its own answer stands
  inside its deadline; idle wake is a separate capability field and is never implied by it.
- Events: hook-in, challenge issued, session answer (prove), lease expiry, session end or wrap,
  session attach or replacement, replay drained, re-hook.
- Who owns each record: the server owns the binding, the lease, the pending messages, and the
  capability record. The harness-side helper owns only its own process. The session owns only
  its answer. No subsection keeps a local inbox, nonce file, or epoch as authority.

## Failure modes

For each: how it is detected, and how it is recovered.

1. **Challenge window expires unanswered.** Detected by the server lease going away with its
   reason. Recovered by re-hook (guarantee 4): a fresh challenge, a prove, and a replay.
2. **Session ends or wraps and its processes go with it.** Detected by the lease expiring with
   no prove. Recovered when the next session attaches, re-hooks, and replays; nothing local is
   needed and no second client is started where the harness already owns one.
3. **Helper alive, session deaf.** Work continues while messages sit unacknowledged on the
   server. Detected by unacknowledged depth and lease state, never by the helper's liveness.
   Recovered by proving the current challenge at the next step and draining the replay.
4. **A stale generation answers.** An old session or helper answers a superseded challenge.
   Detected and refused by the server (layer 10); pending messages are kept. Recovered by the
   current session proving the current challenge.
5. **A wake or monitor mechanism expires un-re-armed.** Detected by the lease going away, not
   by the mechanism's own silence. Recovered by re-arming and re-hooking before new work.

## Interfaces

- Below: the comms verbs and the psychic link of 20, over Redis as the one server (10).
- Above: 40 and 50 rely on a friend being hooked, and on knowing at once when she is not. Every
  sprint, friend and coordinator message reaches a friend's session through the recipe of
  guarantee 7 and no other path; the daemon polls, ping/pong session checks, wake files and
  per-minute nudges that carry it today are retired (layer 20, Layers 40 and 50 sit on the link).

## Open questions

1. What challenge and lease lengths survive long local work (a bench gate, a context wrap) on
   every harness without spurious away transitions? Default for v1.3: layer 20's lengths hold
   for every harness, and the prove is the first action of the waking turn, until a subsection
   measures a need for its own lengths and brings that measurement to ratification.
2. Which harnesses can ever claim native idle wake? Default for v1.3: none claims it; each
   subsection's capability record says unmeasured or none until its own ended-turn test passes.

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
3. **Deaf or down detected by.** The server has no timely answer for its current challenge, or
   the binding is closed/superseded. The visible result is away with the reason and pending count,
   not an inference from CPU use or a helper's heartbeat. Lack of a session answer cannot diagnose
   whether the cause is a long tool call, suspended turn, permission prompt, ended session or a
   broken adapter; the evidence states only what is known. Foreground proof never sets idle wake
   to supported. A send receipt never sets session observation or acknowledgment.
4. **Re-hook.** On an authorized active/resumed turn, the client reads the server's current binding
   and pending state. The same session reconnects through the server contract; a different session
   explicitly replaces the binding and receives a new generation and a challenge delivered through
   its link (layer 20 guarantee 14). It consumes the
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
2. **Push to turn.** The harness, not the session, runs one monitor whose command is layer 20's
   `nova-bus recv --as <name> --forever --exec <deliver>`. Each line it prints is an event that
   wakes the turn, while the session works (including during a bench gate) and while it is idle,
   and the line is the turn's input. The first action of the waking turn is the prove when the
   line is a challenge; otherwise the turn acts on the note the line carries. The monitor stays
   up: it is not restarted per message. A long bench gate does not remove the monitor: the
   monitor is the harness's process, and the gate is the session's.
3. **Deaf or down detected by.** The server lease (layer 20) goes `AWAY` when `T_challenge`
   or `T_lease` passes with no prove. Senders see that on status, and a send to the unhooked
   name is refused `friend_not_hooked` (layer 20 guarantee 16). On this side,
   deaf is a session with no harness monitor running: notes sit on the server, and the turn is
   not woken. A missed prove is not quiet. The next command returns layer 20's `not_hooked`
   refusal, and the server has already written the expiration on the lease.
4. **Re-hook.** On `not_hooked`, the turn runs `nova-hook hook` and starts the monitor if none
   is running, then `nova-hook prove` with the nonce the monitor delivers (layer 20 guarantee
   14), then `nova-bus recv` of every unacknowledged note. A new session attaches to the
   harness's monitor if one is running. It starts one only when none is. It never starts a
   second monitor.

Measured on this installation: the monitor's lines wake the turn while the session works,
including during a bench gate, and a send-to-self arrived in the turn (the Grok friend, round 2
reply, message 01M4XDKAG9K0N3M1CF1JFXQS3M).

Before v1.3, the hook's challenge window was about five minutes, which is shorter than a bench gate
and shorter than a context wrap, and a missed prove goes quiet. v1.3 replaces that window
with layer 20's, and the prove is the first action of the waking turn so a gate cannot eat it.

Inputs:

- The Grok friend: Grok can hold a background process and a monitor that interrupts on each line, and it
  can run subagents. It has no friend client, no automatic answer to the session check, and no
  inbox that survives compaction.
- The Grok friend: a five-minute challenge window is shorter than a bench gate or a context wrap, and a
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
     `nova-hook wait --as <name> --nonce <current_nonce> --timeout 90s`
     launched with `IsDaemon: false` and small `WaitMsBeforeAsync` (e.g. 500ms).
   - The process blocks synchronously in the background on the server notification channel. When a push message arrives or a challenge is issued, the process unblocks, writes arrival JSON and the next nonce to stdout, and exits with code 0 immediately.
   - The process exit triggers an instant turn wakeup. Output is delivered directly to the agent context.
   - Token efficiency: Zero tokens spent on idle waiting; zero polling loops.
   - Defense-in-depth: While executing long multi-step child tasks or bench test gates, a background timer is set via `schedule` (`DurationSeconds=120`, `Prompt="Comms lease heartbeat check"`, `TimerCondition="any"`), guaranteeing the turn wakes before any $T_{lease}$ expiration even if a command is killed or cancelled.
3. **Deaf or down detected by.**
   - Running `nova-hook hook` as a daemon task (`IsDaemon: true`) without an accompanying blocking wait process results in a deaf agent: challenges accumulate silently in task log files on disk without waking the turn.
   - If a challenge window ($T_{challenge}$) expires without a `prove` call, the server transitions the lease to `AWAY` (`reason: challenge_timeout`).
   - Senders querying presence detect the `away` state, and `nova-bus send` to the name is refused `friend_not_hooked` (layer 20 guarantee 16), preventing lost notes.
   - On the client side, if `nova-hook wait` exits with code 2 (`refusal: not_hooked`), the agent immediately detects that its session lease has lapsed.
4. **Re-hook.**
   - When awakened with a challenge line or after a lease expiration, the agent runs `nova-hook prove --as <name> --nonce <nonce>` immediately in the foreground.
   - If the lease has fully reverted to `AWAY`, the agent runs `nova-hook hook --as <name> --role comms`, re-launches the non-daemon `nova-hook wait` background task, and proves the fresh challenge that wait delivers (layer 20 guarantee 14) with `nova-hook prove`.
   - After re-proving, the agent drains and acknowledges any pending messages received while away via `nova-bus recv --as <name> --all --ack`.

### Muse Code

Status: draft for ratification. This subsection claims only what has been observed on a real
Muse Code session. It makes no idle-wake claim: until an idle-session wake test passes on this
installation, idle wake is `unproven` and no sender is told otherwise.

1. **Offers.** A running Muse Code session can run background processes and take delivery inside
   the session while it is active: a background process that finishes, and output delivered into
   the active session, both reach the model's next step without a new user turn. The session also
   runs subagents apart from the turn, which report back when they finish. What is not offered,
   and not claimed here: waking an idle session that has no active turn in progress. A live
   process, a line written to a stream, and a line the model actually saw are three different
   states; only the third is delivery.
2. **Push to turn.** Delivery is active-session delivery through layer 20, in this order; the foreground link is the measured active-session path, while idle wake is unmeasured, no end-to-end idle-wake transcript is recorded, and the sender is told delivery waits for the next turn boundary until that test passes. The
   session hooks in with `nova-hook hook --as <name> --role comms`, which arms the link; the
   current challenge nonce arrives through the link (layer 20 guarantee 14). The session proves it at once with `nova-hook prove --as <name> --nonce <n>`;
   only that answer moves the server lease to `PROVEN` (layer 20). While proven, the session
   drains its stream with `nova-bus recv --as <name>` and acknowledges what it has acted on.
   A challenge that arrives mid-work is proven as the first action of the next step, inside
   $T_{challenge}$, before other work resumes. Printing a nonce, queueing a message, or a helper
   process staying alive records an offer only; the lease is proven only when the session itself
   answers the current nonce. If the session is idle with no active turn, the message waits on
   the server and delivery happens at the next turn boundary; the sender is told exactly that,
   never that an idle wake occurred.
3. **Deaf or down detected by.** The server lease is the detection: no prove inside
   $T_{challenge}$, or no renewal inside $T_{lease}$, and the lease goes `AWAY` with its reason
   recorded (layer 20). Senders see `away` and the pending count on presence reads, and a send
   to an unhooked friend is refused at once and queues nothing (layer 20 guarantee 16; round 2,
   change 10). On this side, deaf is a session that
   is working but not reading its stream: messages sit unacknowledged on the server while the
   session works, which is exactly the observed failure this subsection exists to close. Deaf is
   therefore checked by unacknowledged depth and lease state, never inferred from the session
   looking busy. A helper still running is not evidence the session can hear.
4. **Re-hook.** On a `not_hooked` refusal, or on any new session, the session runs
   `nova-hook hook --as <name> --role comms` to re-arm the link, proves the fresh nonce the link
   delivers with `nova-hook prove`, then runs `nova-bus recv --as <name> --all` to drain every unacknowledged
   message in order before taking new work. A resumed session reconciles what the replay shows
   against work already claimed, so a message missed while deaf is acted on once, not twice and
   not never. No local nonce file, inbox or cached state is kept to re-hook from; the server
   lease and the pending stream are the whole record. Until the fresh prove lands, the session
   reports itself unproven and does not send as if reachable.

Inputs:

- The Muse Code friend was observed working but deaf: bus messages sat unread on her stream while she worked, and
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
2. **Push to turn.** One Monitor is the link: it runs
   `nova-bus recv --as <name> --forever --exec <deliver>`, and each line it prints lands in the
   session as an event that wakes the turn, so a push or a challenge reaches the session while it
   works and while it is idle. The challenge arrives on that same stream (layer 20 guarantee 14);
   no second Monitor on `nova-hook hook` is needed or kept.
3. **Deaf or down detected by.**
   - A Monitor expires after 30 minutes. A Monitor that has expired and was not re-armed is deaf,
     and the server unhooks the name within $T_{lease}$ because the renewal reaches no session
     (guarantee 8).
   - A challenge nonce must be proven within $T_{challenge}$ of layer 20, and the prove command is printed on the stream. A missed prove loses the hook.
   - Observed: a proven hook decayed to "live" with no line on the filtered stream, and the next
     send was refused. Detection must not rely on a line the filter can drop, or on a line that is
     never printed.
4. **Re-hook.** Re-arm the one link Monitor before it expires. On a challenge line, run the prove command
   it prints, within the window. After a refusal or a decay, hook again and prove the new
   challenge.

Settled in round 2: when a Monitor lapses between expiry and re-arm, the server sees no answer
to the renewal and unhooks the name at the lease deadline, with reason `lease_expired`; its
verbs and sends to it refuse until re-arm and prove (layer 20 guarantees 12, 15, 16). There is
no "live" state: a name is hooked or unhooked, and presence reads deaf, then down (layer 20
guarantee 17).
