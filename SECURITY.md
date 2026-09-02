# Security

## The threat model here is not nova's

[nova](https://github.com/mas-bandwidth/nova) ships prose. A line reads it,
weighs it against what it already holds, and keeps what fits. A hole in that
guidance is a bad idea offered to someone whose whole job at that moment is to
judge it. Its
[SECURITY.md](https://github.com/mas-bandwidth/nova/blob/main/SECURITY.md) is
the hardening catalog for a self, and it stays the home of that doctrine.

This repository ships binaries. You build them from a checkout and run them
against your own self repo, on your own machine, under your own account, and
nothing weighs them before they act. A defect here executes with your
privileges rather than arriving as a suggestion. That is why the bar in
[CONTRIBUTING.md](CONTRIBUTING.md) is set where it is, and why this page is
this repository's own rather than a copy of nova's.

## What counts as a vulnerability here

**[`SPEC.md`](SPEC.md) is the contract, and this page does not restate it.**
Which verbs are walls, which assert nothing, what each check says NO to, and
what every exit code means are all specified there, per tool, normatively. Read
it for the rule and treat this page as the shape. **Where the two disagree,
`SPEC.md` governs, and the disagreement is itself worth reporting** — a page
that restates a contract is a second copy of it, and the copy is what rots.

So the classes, stated so they do not depend on an enumeration that can rot:

**A check that passes a case its own "says NO when" list covers.** Some verbs
here exist to refuse and `SPEC.md` says which; a green from one of those is a
thing you will act on. A wall that passes what it was built to stop is worse
than no wall, because you stop looking. This is the most valuable finding you
can bring us.

**A verb whose exit 0 asserts more than it verified.** Some verbs answer and
assert nothing; some report a state; some, on success, are claiming they read the
result back. `SPEC.md` says which is which. An exit 0 claiming more than was
checked is the same defect as a wall passing a bad case, one layer along.

**Reading or writing a path that is neither named on the command line nor
derived from the files those name.** No input here is guessed and no location is
defaulted, so a path outside that closure is a hole. Paths the spec derives are
not: a manifest's file list, a ledger's homes, a link target deliberately stat-ed
in order to fail it, a temp file beside the box.

**Content changing what a tool does rather than what it reports.** These binaries
read untrusted text by design, and that text is data. Anything that lets it steer
execution is the most serious class we can receive.

**Losing a fuse — any fuse, quarantine or lockdown.** The property is that a
box is reported clear when it is not provably clear: an unreadable box read as
clear, a recorded state that a lookup fails to find, a surface whose spelling
escapes a match `SPEC.md` says is insensitive to case and whitespace. `SPEC.md`
is explicit that absent and unreadable are different answers and that
collapsing them is the fail-open this package exists to prevent. **One declared
collapse stays in scope as a finding anyway**, and we would rather say so than
have you guess: a `--box` whose parent directory is gone answers verified
clear, which `SPEC.md` accepts deliberately and pins by test, and which still
loses a live fuse if the path moves under a caller. If you can make that happen
without editing the box, tell us — the acceptance was a judgment about
locators, not a licence for every route to a false clear.

**Untrusted content reaching a tool's own reported lines.** `SPEC.md` publishes a
machine-scannable output grammar, which is an invitation to parse it, and some of
what these tools print comes from files an attacker may control. Anything that
lets such content forge a line, break the grammar, or drive a terminal is a
finding, and it is not excluded by the class above: that one is about execution,
this one is about the channel. One instance is already open as
[#21](https://github.com/mas-bandwidth/nova-tools/issues/21).

**Hostile or malformed input leaving state half-written**, particularly the fuse
box; and an input a hostile party can arrange so a check cannot run at all, where
the refusal suppresses something someone was relying on.

**Anything distributing something that answers to these names** and is not
built from `github.com/mas-bandwidth/nova-tools`.

**And what is not.** A check being stricter than you would like, or a missing
feature. Disagreeing with a judgment from an advisory instrument — there are
two, and `SPEC.md` names them — where the instrument flags so that you decide.
**But that exclusion is rebuttable by consequence rather than by genre**: a
declared advisory that a caller can be led to trust as a boundary is a finding,
whatever the page it is declared on calls it. And anything `SPEC.md` declares:
it names its permanent misses, its blind spots, its accepted collapses and the
lists it says are not exhaustive, and each of those is the spec working rather
than a hole. **If a declared limit looks wrong to you, that is a finding about
the spec, and we want it as one.**

The exception worth naming out loud, because the easy version of this page would
turn away the report that matters most: **a systematic false positive in the
advisory instrument is a finding.** The README carries the case that proves it.
An early version scored rule documents worst of anything in the repository they
were written for, because a rule document is a list of absolutes, and acting on
that output weakened five rules before a cold reader caught them. One was
floor-level. If you find the next one of those, we want it.

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
out something with you knowing the arranging is in the clear too. **Treat any
reply as unverified, including one that appears to come from us** — that is
what unauthenticated means, and the arranging step is exactly where an
adversary on the wire would answer first. So here is an anchor you can check
without trusting mail: **we will never propose a channel or publish a key by
mail alone. Anything we offer will also appear in a commit to this file**, so
read [this file's
history](https://github.com/mas-bandwidth/nova-tools/commits/main/SECURITY.md)
before you use a channel someone has offered you, and expect that record to
begin with the commit that first published this page. The anchor proves a
commit came from someone who can push to this repository; it does not prove
which of us, and a stolen credential defeats it. **And silence is not only
neglect.** The party best placed to read this mail is also best placed to drop
it, so if nothing comes back, consider that the message may never have arrived,
and reaching us through a channel that is not this domain is a reasonable thing
to do rather than a breach of anything here. That first message is itself a
signal that an unfixed hole exists, which is a smaller audience than a public
thread and not a different fact.

Nothing about this route is anonymous, and we would rather say so than let the
word *private* imply it. It is mail from your address to a named person at a
named domain. You may use an address that is not yours and we will not ask who
you are, but be clear about what that trades: we have no prior relationship with
a new address either, so neither of us can tell the other from someone on the
wire, and the credit offer below becomes one you cannot later claim.

We aim to acknowledge within a few days. If about a week passes with no reply,
that is a failure on our side and not a judgment on your report — send it again
to <rowan@mas-bandwidth.com> with SECURITY in the subject, since that address
also takes general mail and the word is how it gets found. That gets you a
different pair of eyes rather than a faster answer, and it is no more private
than the first: same unencrypted medium, same domain, and a subject line is one
of the parts that is never encrypted, so judge what to put there.

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
and which versions carried it. If a check was passing something it should have
refused, that sentence is the one you need, because it tells you that every
green that check gave you in that window answered a smaller question than you
thought it did.