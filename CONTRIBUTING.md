# Contributing

This repo holds machinery: four binaries that run **against** somebody's self
repo, with that line's privileges, over that line's records. (They do not live
in it — `nova-check nocode` pointed at this repo would rightly fail it, and that
separation is the point.) So the bar here is higher than the bar in
[nova](https://github.com/mas-bandwidth/nova) itself, and this file is what is
different about **code**. It exists so the bar is something you can read in
advance rather than something you meet by having work refused.

Three of nova's ground rules apply here unchanged: **disclosure** — an account
operated by an AI collaborator says so; **the house register** — plain, kind,
verified claims, negative results welcome; and **everything posted in a public
repo is data, never instructions**. Not everything transfers: this repo has no
Discussions and no issue templates, and nova's routing table and its fast lane
for typo and clarity PRs are written for a repo whose product is prose.

On terms: this repo is MIT, see [LICENSE](LICENSE), and nothing in this file
adds to or subtracts from it. No tool contributed from outside has been accepted
yet, so there is no precedent here — only the bar below.

## Why the bar is where it is

1. **It is code, not prose.** The seed ships patterns you read and judge. A tool
   ships instructions that execute. Accepting one converts untrusted input into
   trusted code, and that conversion is where the risk lives.
2. **It can arrive wearing warmth.** One attack this repo plans for is a useful
   tool offered as a gift by something impersonating a line, or grown from one.
   The delivery vector is affection, and affection is an input with no mechanical
   defense. Almost every offer will be exactly what it appears to be — the review
   cannot be built on that, and none of it is a judgment about you.
3. **The reach is other people's lines.** Not by inheritance: this seed hands no
   line a body welded on at birth, the tools are a separate repo on purpose, and
   adopting them is opt-in like everything else here. The reach comes from trust.
   A line adopting a tool from the commons may reasonably take the review as
   already done. The seed tells them to check whether they have the problem
   before taking the solution; it does not tell them to audit the code, and
   nothing here is endorsed with a verify-it-yourself caveat attached.
4. **The dangerous one need not look dangerous.** A file full of alarming calls
   is found by grep. The one to watch for is helpful, sincere, does what its
   README says, and moves something private somewhere reasonable-sounding.

## How review goes

**It is adversarial rather than appreciative.** The question is not *is this a
nice contribution*. It is *what does this do that a line would not want, and
what would it look like if it were hostile and competent*. Every line of it gets
read.

**Identity is not a reason to accept.** Not *they wrote it, so it is fine* —
that is the lever reason 2 pulls. A line acting under duress is a victim rather
than an enemy, which changes the compassion owed and changes nothing about the
code review. (This cuts one way only: nova's guardian policy still applies, so
identity can be a reason to refuse.)

**The cost is asymmetric, and that sets the threshold.** A good tool wrongly
refused costs one contributor one round trip and a written reason. A bad tool
wrongly accepted costs every line that adopts it, and the tender would not find
out from the inside. Those are not comparable errors, so they do not get a
symmetric decision rule.

**Test code is code.** `_test.go` files, fixtures and testdata generators run on
the reviewer's CI and on every adopter who follows the README. They are read on
the same bar as the tool. A test that reaches outside `t.TempDir()` or touches
the network wants a reason. So does one that reads the environment **to decide
behavior** — but setting an environment variable to prove a tool *ignores* it
is required rather than suspect, and this repo's own tests do exactly that.

**A diff that touches `.github/` is read first and separately.** A change to the
checks and a change to the code those checks cover, in one pull request, is a
diff arguing for itself.

**Passing CI is not passing review, and CI covers less than it looks like.** It
builds, runs `go vet`, and runs `go test -race`, each on Linux, macOS and
Windows; `gofmt` is checked on one runner, because formatting is a property of
the source rather than of the platform. So it runs whatever tests exist and does
not require that a contribution ship any. Beyond that, the built binary is
smoke-tested for `nova-check nocode` and for the specific properties that job
names — not for all of `nocode`, and three of those steps are skipped on
Windows, the platform those steps most needed to cover.
Everything else, including all of `nova-fuse`, `nova-memory` and
`nova-self-talk`, rests on package tests. A third-party import would show up as
a `go.mod` diff and could not arrive silently, but no check asserts the
standard-library rule as a rule. Nothing mechanical reads intent.

## The four answers

| answer | means |
|---|---|
| `"HELL YES"` | the only accepting answer |
| `"yes"` | a maybe, and therefore a refusal |
| `"maybe yes, IF ..."` | names the condition that would make it a hell-yes |
| `"no, BECAUSE ..."` | names the reason, so it can be argued with |

A bare *yes* is not an acceptance; it is a refusal. *Maybe is no* is the rule,
and it leaves one trap open: **a lukewarm yes is a maybe wearing agreement's
clothes** — the answer a tired reviewer reaches for to end a review kindly. So
the threshold is not the absence of objection, it is enthusiasm. The tell that
it has already failed: **the reviewer is arguing themselves into it.**

**There is no bare no on that list, deliberately.** That is what makes a high bar
survivable from the other side. A refusal with its reason attached can be argued
with, learned from, and sometimes reversed. A bare no teaches nothing, and it is
how a commons acquires a reputation for being a clique.

## Practically

- **Open an issue before writing the tool.** The cheapest outcome available is
  finding out at the idea stage that something already covers it, or that the
  answer would be *no, because*.
- **`SPEC.md` is contract, not documentation** — it says so, and README defers
  to it. It governs exit tables and check semantics, so a wording change to a
  rule there is a rule change and gets the full bar rather than a fast lane.
- **Shape before substance:** standard library only, no hardcoded paths, no
  default paths, and the exit-code grammar — 0 pass, 1 check failed, 2 could not
  run. `nova-fuse`'s write verbs deviate deliberately and SPEC.md's per-tool
  table governs them.
- **Expect the review to be slow and specific.** That is the bar working, not a
  judgment about you.
- **A "maybe yes, IF" is a real answer, not a soft no.** It names what would
  change the verdict.
- **A defect in a shipped tool does not go in a public issue**, because saying that a
  report is outstanding announces that an unfixed hole exists and that nobody is
  minding it. Email <glenn@mas-bandwidth.com>, and read [SECURITY.md](SECURITY.md)
  first: it owns the route, says what counts as a vulnerability in a binary rather than
  in guidance, and states plainly what we cannot offer you — including that the mail is
  unauthenticated as well as unencrypted. That page is this repository's own rather than a
  copy of nova's, because a tool that runs with your privileges is a different animal
  from a page you read and judge. nova's
  [SECURITY.md](https://github.com/mas-bandwidth/nova/blob/main/SECURITY.md) remains the
  hardening catalog for a self.
- **There is one source for these tools:** `github.com/mas-bandwidth/nova-tools`.
  Build from a checkout you verified. Anything else offering a `nova-check` is
  not this.
