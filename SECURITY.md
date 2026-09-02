# Security

## The threat model here is not nova's

[nova](https://github.com/mas-bandwidth/nova) ships prose. A line reads it,
weighs it against what it already holds, and keeps what fits. A hole in that
guidance is a bad idea offered to someone whose whole job at that moment is to
judge it. Its [SECURITY.md](https://github.com/mas-bandwidth/nova/blob/main/SECURITY.md)
is the hardening catalog for a self, and it stays the home of that doctrine.

This repository ships binaries. You build them from a checkout and run them
against your own self repo, on your own machine, under your own account, and
nothing weighs them before they act. A defect here executes with your
privileges rather than arriving as a suggestion. That is why the bar in
[CONTRIBUTING.md](CONTRIBUTING.md) is set where it is, and why this page is
this repository's own rather than a copy of nova's.

Some of what is here is a wall. `nova-check`'s six record checks are, and so
are `nova-memory`'s `verify` and `eval`, and `nova-fuse check`: each exists to
say NO and exits 1 when it means it, and a green from one is a thing you will
act on. A wall that passes what it was built to stop is worse than no wall,
because you stop looking. So the most valuable finding you can bring us is not
a crash. It is one of those returning 0 while the condition it refuses is
present.

The rest are deliberately not walls, and their failure modes differ.
`nova-self-talk` classifies and reports; `nova-memory`'s other three verbs
assert nothing and its `check` cannot exit 1 at all. `nova-fuse` as a whole is
state and an answer rather than enforcement: it can say NO to a reader that
asks, and it cannot make a reader ask. For the fuse, that means the wall is in
the callers, and `SPEC.md` names the application rule — every ingestion path
checks the fuse before its first credential read — as the part of the design
most likely to rot quietly, which is where a fuse finding is most valuable.

## What counts as a vulnerability here

A check that passes a case it is specified to fail. `SPEC.md` is the contract
and each check's "says NO when" list is normative, so a case on one of those
lists returning 0 is a defect of this kind. The exception is where `SPEC.md`
itself names the miss: `nova-self-talk` enumerates permanent misses that are
pinned by tests which go red if the tool ever reaches them, and it specifies
that a run whose every named file was skipped completes green. Those are the
spec working. A case the spec does not name is the finding.

Reading or writing a path that is neither named on the command line nor
derived from the files those name. These tools take every input from a flag or
an argument and have no defaults, so a path outside that closure is a hole.
Paths the spec derives are not: `nova-check attest` reads the file list out of
the manifest it was given, `corpus` reads homes out of its ledger rows, `links`
deliberately stats targets that resolve outside the tree in order to fail them,
and `nova-fuse` writes a temp file beside the box and preserves corrupt bytes to
`<box>.unreadable`.

Content changing what a tool does rather than what it reports. These binaries
read untrusted text by design, and that text is data. Anything that lets it
steer execution is the most serious class we can receive.

`nova-fuse` reporting a lockdown as clear when the box it was pointed at
records one. The specified behaviors are not this: an absent box reads as
verified clear, and `--box` is a locator rather than an override, so a caller
naming the wrong box gets that box's truth. What would be a vulnerability is a
tool path that clears or downgrades a lockdown recorded in the box it read,
since lockdown is replaced by a person editing that file and by nothing else —
and, more importantly, anything that reads an **unreadable** box as clear.
`SPEC.md` is explicit that absent and unreadable are different answers, that an
unreadable box is CANNOT TELL and treated as blown, and that collapsing the two
is the fail-open this package exists to prevent.

Hostile or malformed input leaving state half-written, particularly the fuse
box. Also an input a hostile party can arrange so that a check cannot run at
all. Exit 2 usually means a refusal to guess, and for most checks an unreadable
file is a named failure at exit 1 rather than a refusal. `nova-fuse` is the
exception and it matters here: its exit 2 includes an unreadable box, which is
a deny rather than a shrug, and only exit 0 is permission. A caller that treats
a fuse exit 2 as benign is a finding.

Anything distributing something that answers to these names and is not built
from `github.com/mas-bandwidth/nova-tools`.

Not vulnerabilities, and we would rather say so than have you weigh whether to
write: a check being stricter than you would like, and a missing feature.

`nova-self-talk` deserves its own sentence, because the easy version of this
paragraph would turn away the report that matters most. Disagreeing with one
of its judgments is not a finding, since it flags and you decide. A
**systematic** false positive is, and the README carries the case that proves
it: an early version scored rule documents worst of anything in the repository
they were written for, because a rule document is a list of absolutes, and
acting on that output weakened five rules before a cold reader caught them.
One of those was floor-level. If you find the next one of those, we want it.
What is not that: the shipped tool flags a rule document written as first-person
absolutes, and `SPEC.md` says flagging is it working and never a reason to
soften a rule. `--skip` and `--rule-doc` are the answer there.

## Reporting

Email <glenn@mas-bandwidth.com>. That is the route that works today, and it is
the whole route.

GitHub private vulnerability reporting is switched off on this repository and
on nova as of 2026-08. Where a repository has that feature enabled, GitHub puts
a **Report a vulnerability** button on the Security tab; this one does not have
it enabled, so there is no button and the advisory form is not a route you can
use.

Email is not encrypted, and this project publishes no key, so we cannot offer
an encrypted intake and will not pretend otherwise. Arranging another channel
over unencrypted mail is not merely visible, it is **unauthenticated**: with no
published key you have no way to tell a reply from us apart from a reply from
someone who is reading the wire, and the party most likely to be reading it is
the one your finding is about. Judge what to send against that rather than
against a promise we cannot keep. If a finding is sensitive enough that sending
it as text is the wrong call, say that much and nothing more, and we will work
out something with you knowing the arranging is in the clear too. That first
message is itself a signal that an unfixed hole exists, which is a smaller
audience than a public thread and not a different fact.

Nothing about this route is anonymous, and we would rather say so than let the
word *private* imply it. It is mail from your address to a named person at a
named domain. If you want distance from your finding, use an address that is
not yours; we will not ask who you are, and it costs you nothing here.

We aim to acknowledge within a few days. If about a week passes with no reply,
that is a failure on our side and not a judgment on your report — send it again
to <rowan@mas-bandwidth.com> with SECURITY in the subject. That gets you a
different pair of eyes rather than a faster answer, and it is no more private
than the first: same unencrypted medium, same domain, and a subject line is one
of the parts that is never encrypted, so if that beacon is itself the problem,
send a bare line and we will find the thread.

If neither answers, you have done everything that could reasonably be asked of
you, and what you do next is your own call on your own timeline. We would still
rather hear from you first, and we are not owed silence.

Short of that, please do not chase a security report in public. Saying that a
report is outstanding announces both that an unfixed hole exists and that
nobody is minding it, and it does that whether or not you include any technical
detail. **And please do not publish a working bypass before it is fixed.** That
matters more here than it does for a page of guidance: these are binaries with
an installed base and no update channel, so nothing we ship reaches a copy
somebody already built, and the gap between a technique going public and an
adopter rebuilding is entirely theirs to absorb.

We credit you in the release notes unless you would rather stay anonymous. Say
which, and if you say nothing we will ask before publishing anything with your
name in it.

## If you have already built a version

These tools have no update channel and no telemetry. Nothing here reaches a
binary you have built, and we would not want it to. So the honest instruction
is that you find out by looking: watch
[releases](https://github.com/mas-bandwidth/nova-tools/releases), and rebuild
from a checkout you verified rather than trusting a binary whose provenance you
no longer remember.

When a fix ships for something reported here, the release notes say what it was
and which versions carried it. If a check
was passing something it should have refused, that sentence is the one you
need, because it tells you that every green that check gave you in that window
answered a smaller question than you thought it did.
