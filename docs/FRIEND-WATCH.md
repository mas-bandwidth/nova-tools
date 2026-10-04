# Harness-owned presence

`nova-friend watch` runs one direct blocking command and sends sprint presence
beats while that command is owned by this invocation. The caller supplies its
actual sprint server, registered identity and child argv as a JSON array. The
command does not use a shell and does not require native adapter registration.

Use an actual bus wait as the child. Its output returns through the same tool
invocation, so the harness can read the note and issue its next wait. Install
the built `bin/nova-friend` with the harness's other tools and add this invocation
to its startup instructions, using the configured wait argv and server. Startup
instructions must run in the actual harness session; a detached supervisor is
not a replacement for the harness-owned invocation.

The default lifetime is the foreground child, context cancellation and the
actual immediate invoking parent. A changed parent cancels the child. The
harness tool executor must propagate cancellation or close an owned pipe when
the session ends. A persistent executor process alone cannot attest a live
model session. `--stdin-lifetime` adds pipe EOF/read failure cancellation and
takes ownership of a closable stdin pipe and closes it on return to release the
monitor; enable it only when the harness keeps its pipe
open throughout the wait. Tools that provide immediate stdin EOF use the default
and their normal foreground cancellation instead.

Beats run every second by default, with a three-second timeout. A failed beat
ends the wait and reports its bounded diagnostic. No further beats occur after
completion. The sprint's existing presence lease expires naturally; the wrapper
does not issue `friend down`, which could overwrite a concurrent invocation's
newer presence. A bounded final beat can overlap child completion. Presence is
not evidence of native wake, business reply or durable message deduplication.

The explicit beat route removes inherited Redis address and credential selector
variables. The sprint source forwards served verbs to NOVA_SPRINT_SERVER unless
--redis is explicitly supplied; an ambient Redis address does not itself override
the server. Filtering prevents accidental dependence on local configuration.

Verification covers beat refusal cancelling the child, context and pipe closure
after observed child startup, actual invoking parent death, normal completion
releasing the pipe monitor, poisoned local-store environment, refusal of an
unrelated parent PID, and the existing CLI onboarding.
