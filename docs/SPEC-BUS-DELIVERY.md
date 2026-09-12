# Prepared bus delivery — proposal for the version-report recovery gap

Status: proposed, not implemented. This is the bounded dependency of SPEC-UPDATE
rules 24–25. Johnny accepts the direction (johnny-4d571a941268), review only;
this exact contract still needs its independent read. Existing ordinary send is
unchanged. No new timer, service, friend identity or update policy is introduced.

## Why

An unchanged version report must reach its destination after a failed push.
If the push landed but its answer was lost, recovery must find that same note.
A child killed before printing a result must not force the caller to guess a
new identity. Preparation therefore happens before any bus mutation, and the
caller retains its result before dispatch.

## Two operations, one identity

```
nova-bus prepare --bus <checkout> --as <friend> (--file <draft> | --stdin) [--slug <slug>]
nova-bus send --bus <checkout> --remote <name> --branch <name> --as <friend> (--prepared <file> | --prepared-stdin) [--attempts <n>] [--git-timeout <seconds>]
```

`prepare` reads the explicit local bus and draft, uses the existing participant,
recipient and draft validation, and computes the existing deterministic note ID.
It assigns Date once using the machine clock. It performs no network, Git write,
checkout write, index update or delivery. Stdout is exactly one JSON object:

```
{"schema":"nova.bus.prepared/1","id":"<existing bus ID>","path":"<own-lane note path>","note":"<complete rendered note>","sha256":"<64 lowercase hex digits>"}
```

`sha256` hashes the UTF-8 bytes of `note`, including its final LF; it is a full
content check, not a new bus identity scheme. The existing ID algorithm and
normalization remain unchanged. The path is the ordinary prepared note path,
not an arbitrary destination. Any tolerance notices go to stderr as bounded
single lines so stdout stays machine-readable. No automatic output file exists.
Preparation failure writes no bus state. Exit 0 produces a valid artifact;
exit 1 is a draft/roster refusal; exit 2 is an invalid invocation or unreadable input.

The caller atomically saves the entire artifact and its delivery scope before
starting `send`. Losing the preparation process before saving it cannot have
sent anything. Losing the sending process cannot erase that retained identity.

The new send input modes are mutually exclusive with ordinary `--file`/`--stdin`.
They validate the artifact schema, full digest, rendered note, current roster,
speaker, deterministic ID and safe own-lane path before writing anything. A
changed roster that no longer resolves the prepared identity is a named refusal,
not a reason to rewrite the note. Artifact content is data, not permission: the
explicit command still names bus, remote, branch and speaker. No new `--id`
override or caller-defined arbitrary ID is needed.

Sending a prepared artifact is also its explicit retry and confirmation:

1. Under the checkout lock, fetch/query the named remote branch and locate the
   exact note by ID. If note bytes and its INDEX record agree, return
   `SEND OK id=<id> path=<path> commit=<remote-containing-commit> pushed=true attempts=0 state=already-published`.
   No new note, commit or push is made. Remote INDEX alone is insufficient proof.
2. A same-ID different-content note, unsafe path, inconsistent INDEX, or different
   note at the prepared path is refused. Preserve all evidence; never overwrite.
3. If absent remotely, reconcile the exact prepared note and its INDEX entry from
   any interrupted local attempt. Exact matching partial writes can be completed;
   conflicting bytes cannot. Reuse an existing pending commit when possible. The
   final contribution contains one note, one INDEX entry and only a required
   standard merge-attributes change, with the normal send provenance trailer.
4. Refuse unrelated dirty/staged work or unrelated local commits ahead of the
   named remote. In particular, another tool's valid trailer does not authorize
   publishing its pending contribution during this retry. Refusal preserves the
   caller's index, files and commits. The operation does not stash, reset, clean,
   delete user files, change remote configuration or acquire credentials.
5. Push without force through bounded normal bus race handling. Reconcile the
   exact identity after an ambiguous push before trying again. A racing unrelated
   remote note is preserved. One successful publication returns the usual
   `SEND OK ... pushed=true`, plus `state=published`. Only remote confirmation
   establishes success. Known failure or uncertainty returns 1 with the prepared
   ID and bounded diagnostic; no raw source blob enters a diagnostic.

Retries use the existing finite attempt and Git-timeout controls and must remain
inside the reporter's remaining overall budget. Exhaustion leaves the caller's
prepared artifact available; it is not a delivered result. Same-ID equality never
stands in for full note equality. No operation is scheduled automatically.

## Version reporter join

With an explicit `--snapshot`, store `pending` before sending: delivery scope,
prepared artifact and the exact observed map it describes. Each later explicit
`--send` resolves that pending artifact first, even when no installed version
changed. Remote-confirmed success updates `delivered` and clears pending atomically.

If today's observation differs while an older report is pending, finish the old
report first. If that cannot be confirmed within budget, retain it, report the
pending gate and do not send a newer report. If confirmed, a newer observation can
be prepared, atomically saved and sent using the remaining budget. Never discard
an old report merely because a newer observation exists.

Without `--snapshot`, a plain report/draft still writes nothing, and each explicit
send remains a new delivery intention as in SPEC-UPDATE. Its artifact is held in
memory for bounded in-process retry only. Cross-process recovery requires the
caller-named snapshot and is not claimed for a stateless invocation. Help names
that choice plainly. Recipient suppression compares confirmed delivery by scope;
local observations and timestamps never suppress a first delivery.

## Deciding tests

Use real disposable local bare Git remotes, not only fake SEND text:

- Reject the first push, recover the remote, repeat an unchanged snapshot send:
  exactly one note and INDEX record land, with the original ID and bytes.
- Accept a push but lose the result: retry returns already-published without a
  second note, new commit or second publication.
- Kill the child before any SEND output, including before saving the note, after
  note write, after INDEX write and after commit: the saved artifact recovers the
  same ID. Each final remote has one complete contribution.
- Change the observation while pending: unresolved old delivery blocks the new
  one explicitly; resolved old delivery precedes the new one. Neither disappears.
- Same ID/different bytes, different note at path, changed speaker/roster, malformed
  artifact and outside-lane path are refused without writes or content echoes.
- Unrelated staged work, dirty files and ahead commits survive a refused retry
  byte-for-byte. A concurrent remote writer survives successful bounded recovery.
- Prepare makes no network/Git/write calls; its output round-trips, full hash and
  existing bus ID recompute, and ordinary send still passes its existing tests.
- A fully confirmed unchanged report makes zero bus invocations on the next run.

Public fixtures are synthetic. Friends choose recipients; version statuses do not
wake Johnny through To. New versions remain a choice, and working alternatives
remain welcome.
