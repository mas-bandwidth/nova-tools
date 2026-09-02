# Security

## The threat model here is not nova's

[nova](https://github.com/mas-bandwidth/nova) ships prose. A line reads it, weighs it against
what it already holds, and keeps what fits. A hole in that guidance is a bad idea offered to
someone whose whole job at that moment is to judge it.

This repository ships binaries. You build them from a checkout and run them against your own
self repo, on your own machine, under your own account, and nothing weighs them before they
act. A defect here executes with your privileges rather than arriving as a suggestion. That
difference is why the bar in [CONTRIBUTING.md](CONTRIBUTING.md) is set where it is, and why
this page exists rather than deferring to nova's.

There is a second reason the difference matters. These tools are walls: `nova-check` exists to
say NO, and a green from it is a thing you will act on. A wall that passes what it was built to
stop is worse than no wall, because you stop looking. So the most valuable finding you can
bring us is not a crash. It is a check that returns 0 while the condition it refuses is
present.

## What counts as a vulnerability here

- A check that passes a case it is specified to fail. `SPEC.md` is the contract: each check's
  "says NO when" list is normative, and a case on that list returning 0 is a defect of this
  kind whatever the cause.
- Reading or writing outside the paths named on the command line. These tools take every input
  from a flag or an argument and have no defaults, so a path they touch that you did not name
  is a hole.
- Content changing what a tool does, rather than what it reports. These binaries read untrusted
  text by design, and that text is data. Anything that lets it steer execution is the most
  serious class we can receive.
- `nova-fuse` losing a lockdown by any route other than a person editing the box in a live
  conversation. Lockdown is not reset, it is replaced; a tool path that clears it is a
  vulnerability even if it looks like a convenience.
- Hostile or malformed input leaving state half-written, particularly the fuse box.
- Anything distributing something that answers to these names and is not built from
  `github.com/mas-bandwidth/nova-tools`.

Not vulnerabilities, and we would rather say so plainly than have you weigh whether to write:
a check being stricter than you would like; `nova-self-talk` reaching a judgment you disagree
with, since it is advisory by construction and flags rather than decides; a missing feature; a
refusal (exit 2) where you expected an answer, which is these tools declining to guess.

## Reporting

Email <glenn@mas-bandwidth.com>. That is the route that works today, and it is the whole route.

GitHub private vulnerability reporting is switched off on this repository and on nova as of
2026-08, so the Security tab offers you nothing and the advisory form is not usable by someone
who is not a maintainer. We would rather tell you that than leave you to discover it.

Email is not encrypted, and this project publishes no key, so we cannot offer you an encrypted
intake and will not pretend otherwise. Arranging another channel would itself happen in the
clear, which is worth knowing before you rely on it. If a finding is sensitive enough that
sending it as text is the wrong call, say that much and nothing more, and we will work out
something with you on that understanding.

We aim to acknowledge within a few days. If about a week passes with no reply, send it again to
<rowan@mas-bandwidth.com> with SECURITY in the subject, since that address also takes general
mail and is read separately. If neither answers, you have done everything that could reasonably
be asked of you, and what you do next is your own call on your own timeline.

Short of that, please do not chase a security report in public. Saying that a report is
outstanding announces both that an unfixed hole exists and that nobody is minding it, which
hands an adversary the two facts they most want and costs you the anonymity the private route
was there to protect.

## If you have already built a version

These tools have no update channel and no telemetry. Nothing here reaches a binary you have
built, and we would not want it to. So the honest instruction is that you find out by looking:
watch [releases](https://github.com/mas-bandwidth/nova-tools/releases), and rebuild from a
checkout you verified rather than trusting a binary whose provenance you no longer remember.

When a fix lands for something reported here, the release notes say what it was and which
versions carried it, in the plainest terms we can manage. If a check was passing something it
should have refused, that sentence is the one you need, because it tells you that every green
that check gave you in that window answered a smaller question than you thought it did.
