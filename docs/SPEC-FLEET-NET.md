# `nova-pulse fleet net` — the tailnet of a node

Glenn, 2026-09-18:

> Every NODE — a team of humans and AI seats like this one — has its OWN Tailscale network.
> Cross-node reach is Tailscale node sharing, never a merged tailnet. The bus is the
> federation; a node with no tailnet is still a node.

and, the same day, on how much of this we write ourselves:

> We should not solve this problem ourselves, just use what is there. Take advantage of
> Tailscale so that other people using nova tools get it too, not just us.

Both sentences are load-bearing, and together they are the whole design. The **policy** is
Tailscale's own policy file, in this repository, with Tailscale's own `tests` section,
applied by Tailscale's own GitOps action: we write no ACL language, no applier, no etag
dance and no test runner. What we write is the part only we know — **the machines registry
is the source of the policy**, so a node describes its machines once and gets a tailnet that
matches. Our fleet is the first node, not the only one.

This document is the contract. If it and the tools disagree, one of them has a bug.

* the verbs: [`docs/CLI.md`](CLI.md#fleet-net) · the generated page: [`docs/TAILNET.md`](TAILNET.md)
* the policy: [`fleet/tailnet-policy.hujson`](../fleet/tailnet-policy.hujson) · the workflow:
  [`.github/workflows/tailnet-acl.yml`](../.github/workflows/tailnet-acl.yml)
* the registry: [`internal/fleet`](../internal/fleet), the example at
  [`internal/fleet/testdata/machines.tsv`](../internal/fleet/testdata/machines.tsv)

---

## Why one tailnet per node

A **node** is a team: some people and some AI seats, with their own machines, their own
secrets and their own forge. Rowan's node is the Studio, hulk, vision, space, mini, the two
iMac Pros and a bud. Stella's node is Stella's. They are not one organisation with one
network, and making them one would be the easy thing that is wrong:

* **A merged tailnet is a merged blast radius.** One API key, one admin console, one policy
  file: a mistake in one node's ACL is every node's outage, and a compromised key on one
  node reaches every other node's benches. Per-node tailnets mean per-node keys, and a key
  that could reach two nodes would be the merged tailnet by another road.
* **The bus is the federation.** Two nodes work together through git — the bus, the queues,
  the pull requests. Git needs no tailnet. Everything that must cross a node boundary
  crosses it as a commit, which is reviewable, revertable and durable, and which works when
  a machine is asleep.
* **A node with no tailnet is still a node.** Somebody running nova-tools on two machines in
  one room should not have to sign up for anything. That is why the registry carries a
  `provider` column (R1) and why **no verb may hardcode a tailnet address**: `lan` is a
  first-class answer.

**Cross-node reach is node sharing.** When Stella's node needs one of our machines, we share
that ONE machine into her tailnet with Tailscale's node sharing, she accepts it, and her
registry gains a row with `provider=shared-from:rowan`. Her policy does not change: a shared
machine is a machine in her tailnet like any other, and it is in her registry where a person
can read it. Nothing is merged, nothing is trusted wholesale, and the sharing is visible in
two places — her registry and our admin console — rather than in neither.

---

## The rules

Each rule is numbered, each names the red test that makes it fail when it is broken, and
each says what it refuses. **Rules whose verb is not yet written are marked (spec).** Those
verbs exist and refuse: `NET REFUSED verb=<v> reason=not-implemented … (the rule is R<n> in
docs/SPEC-FLEET-NET.md …)`, which is a pointer into this document rather than an apology.

### R1 — the registry says how a machine is reached, and both forms of the line read

The machines registry gains an eighth column, `provider`, between `cores` and `notes`:

```
name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes
name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>provider<TAB>notes
```

`provider` is one of `tailnet` (a node of this node's own tailnet), `lan` (the local network,
no tailnet at all) or `shared-from:<node>` (another node's machine, accepted here through a
Tailscale node share). **A seven-column line reads as `tailnet`** — what every line meant on
the day the column was added — so no registry anywhere has to be rewritten. The reader keeps
`ProviderStated`, so a verb can tell a registry that answered from one that was never asked.
A provider it does not know is a refusal naming the line; `-` is not a provider, because
"how is this machine reached" has no empty answer.

> Red: `TestReadRegistryReadsBothFormsOfTheLine`,
> `TestReadRegistryRefusesAProviderItDoesNotKnow`,
> `TestReadRegistryRefusesALineOfTheWrongWidth` (internal/fleet).

### R2 — `net status` is the tailnet crossed with the registry, and the cross has two findings

```
nova-pulse fleet net status --machines <registry> [--tailscale <path>] [--max <n>] [--timeout <s>]
```

It reads `tailscale status --json` through the **Tailnet seam** and prints one line per
machine in file order, then one line per finding. The two findings are the two directions of
the cross:

* a machine the registry calls `tailnet` for which the tailnet has no node →
  `reason=no-tailnet-node`;
* a node of the tailnet the registry does not carry → `reason=not-in-registry`.

A `lan` machine and a `shared-from:` machine are **not** findings when the tailnet has never
heard of them: the registry never said they were here. Findings exit **1** — the tool saying
NO — not 2; the verb ran.

> Red: `TestNetStatusCrossesTheTailnetWithTheRegistry`,
> `TestNetStatusFindsAMachineWithNoNodeAndANodeWithNoMachine`,
> `TestNetStatusIsSilentAboutAMachineItWasNeverToldIsOnTheTailnet`,
> `TestNetStatusRefusesRatherThanGuess`, `TestNetStatusParsesTailscalesOwnStatusDocument`.

### R3 — a runner host accepts nothing **tagged**

No ACL rule lets anything tagged — a bud, a bench, a seat's machine — reach `tag:runner`. A
runner host talks **out** to the forge, and the forge never connects in, so inbound peer
reach is not a capability it needs. This is the lock of 2026-09-18 — runner hosts are
CI-only — written where the packets are. The denial is an ABSENCE, so the policy's `tests`
section asserts it rather than trusting it, and a Go test refuses an accept rule naming the
tag from any tagged source.

**`tag:runner` is advertised only by a machine that is a runner and nothing else.** This is
Johnny's security read of the same day, and it replaced a paragraph that merely admitted the
problem:

> A Tailscale ACL grants when any rule accepts, so `tag:bench`+`tag:runner` is reachable as
> a bench and R3 is false for that host. The dated `allow-shared=` note is a hole with a
> calendar, not a shape. One reach-role per machine: a host that runs CI on a bench is
> `tag:bench` only. CI is a process, not a tag.

So a shared bench-and-runner host advertises `tag:bench`, full stop, and the denial is TRUE
rather than true-with-a-note: every machine carrying `tag:runner` is one nothing tagged
reaches, with no overlapping accept to defeat it. The registry's `allow-shared=` note keeps
its own separate job — it is what stops a CARD being placed on a runner host — and the two
rules no longer have to agree for either to hold.

The node's own **people** are not a tagged thing; R13 is what they get.

> Red: `TestNetInitLetsNothingTAGGEDReachARunnerHostAndLetsTheOwnersIn`,
> `TestNetInitAdvertisesTagRunnerOnlyForARunnerOnlyMachine`, and the policy's own `tests`
> entries denying `tag:runner:22` from `tag:bud` and `tag:bench`, run by
> `tailscale acl test` in CI.

### R4 — (spec) `net acl` is the escape hatch, and the workflow is the road

```
nova-pulse fleet net acl --file <policy> --diff|--apply [--force --reason <why>]
```

The GitOps workflow (R14) is how a policy is applied. `net acl` exists for the case the
workflow cannot serve — a tailnet whose repository is not the forge, a node bootstrapping
before its first merge — and it does nothing the action does not: `--diff` shows the live
policy against the file, `--apply` is etag-guarded and **refuses when the live ACL was
changed outside the repository**, printing the diff. `--force` waives that out loud, with a
`--reason` that is logged. It never invents an ACL language: the file it sends is the same
HuJSON `tailscale acl test` reads.

> Red (when written): `net-acl-refuses-an-apply-over-a-console-edit`,
> `net-acl-force-says-why-and-logs-it`.

### R5 — a bud reaches benches and nothing else

`bud` is the fifth role: a person's own laptop. It runs no card (`RequireBench` refuses it
with `reason=bud-host`), and the ACL gives it exactly one destination — `tag:bench:22` — with
Tailscale SSH in **check** mode, a browser re-authentication every twelve hours, because a
laptop leaves the house and the machine it can reach from a café is the machine that runs the
work.

> Red: `TestABudIsNotABench` (internal/fleet),
> `TestNetInitPolicyCarriesTheInvariantsAsTailscalesOwnTests`, and the policy's `tests`
> entry for `src: tag:bud`.

### R6 — (spec) `net ssh` is Tailscale SSH, and `authorized_keys` is not distributed

```
nova-pulse fleet net ssh --machine <m> --on|--off|--check
```

The tailnet identity IS the authorization. `--on` enables Tailscale SSH on one machine
(`tailscale set --ssh`), `--off` disables it, and `--check` **proves a connection through the
tailnet identity and that no `authorized_keys` was needed** — it connects, and it asserts
that the far side's `~/.ssh/authorized_keys` does not carry a fleet key. A person who leaves
is removed from the policy, not from every host's key file.

> Red (when written): `net-ssh-check-proves-the-identity-and-the-absent-key`.

### R7 — (spec) `net join` folds `fleet join`, and `net expiry` turns key expiry off by tag

```
nova-pulse fleet net join --machine <m> [--tags <t,...>]
nova-pulse fleet net expiry --check|--off
```

`net join` is today's `nova-pulse fleet join` moved under `net` and taught the registry: the
tags come from the machine's roles, so a bench joins as `tag:bench` without anybody typing
it. **The auth key rule does not change** (R15): it reaches the process only through the
environment variable `nova-secrets exec` fills, and the bench only on the remote shell's
stdin. `net expiry` reports and disables key expiry, which for a tagged machine is a
property of its tag rather than a click per machine in the console.

> Red (when written): `net-join-tags-from-the-registry-roles`,
> `net-join-keeps-the-auth-key-out-of-every-argv` (the existing `fleet join` test, moved),
> `net-expiry-check-names-every-key-that-expires`.

### R8 — (spec) `net names` exists, and MagicDNS means it usually has nothing to do

```
nova-pulse fleet net names --machines <registry> --check|--apply
```

MagicDNS is **on**, so every tailnet machine is reachable by its name and there is no
`/etc/hosts` block to write — the hand fix of 2026-09-18 on hulk, vision, space and mini is a
symptom of MagicDNS being off, not a thing to automate. The verb stays for the machines
MagicDNS cannot answer for: a `lan` machine, and a node with no tailnet at all. It writes one
marker block per provider, idempotently, and `--check` is the certify workload.

> Red (when written): `net-names-writes-nothing-when-magicdns-answers`,
> `net-names-is-idempotent-over-its-own-marker-block`.

### R9 — (spec) `net share` is cross-node reach, and it lands in the registry

```
nova-pulse fleet net share --machine <m> --to <node>
nova-pulse fleet net share --accept <invite> --as <name> --from <node>
```

Sharing offers ONE machine into another node's tailnet. Accepting one writes the registry row
`provider=shared-from:<node>`, so the machine is visible where every other machine is visible
and `net status` stops calling it a finding. There is no verb that merges two tailnets, and
there will not be one.

> Red (when written): `net-share-accept-writes-the-registry-row`,
> `net-share-refuses-a-node-the-bus-does-not-carry`.

### R10 — (spec) `net serve` is private by default; Funnel is a decision said out loud

```
nova-pulse fleet net serve --machine <m> --port <p> --tailnet-only
nova-pulse fleet net serve --machine <m> --port <p> --funnel --reason <why>
```

`tailscale serve` puts something in front of the node's own people with a certificate and no
port forwarding. **Funnel** — the same thing on the public internet — is refused unless
`--funnel` and `--reason` are both given, and the reason is logged as a structured event.
Nothing is published to the internet as a side effect of a flag default.

> Red (when written): `net-serve-refuses-funnel-without-a-reason`,
> `net-serve-logs-the-funnel-decision`.

### R11 — services are reachable from benches on named ports only

The stack (Loki 3100, Grafana 3000, Redis 6379) is a destination for `tag:bench` on those
ports and on nothing else — **not on ssh**. A bench pushes logs and metrics; it does not
administer the services host. The ports are a table in the generator, so a node with a
different stack changes one table and not the code.

> Red: `TestNetInitPolicyCarriesTheInvariantsAsTailscalesOwnTests`, and the policy's `tests`
> entry for `src: tag:bench`.

### R12 — nothing reaches the coordination machine but the node's own people

A friend's window lives on the coordination machine, and it is the one place that holds a
whole context. `group:owners` — the node's own logins — is the only source with a rule to
`tag:coordination`. No bench, no bud, no runner host, and no machine shared in from another
node.

> Red: `TestNetInitPolicyCarriesTheInvariantsAsTailscalesOwnTests` (which fails any accept
> rule to `tag:coordination` whose src is not `group:owners`), and the policy's `tests`
> entry for `src: group:owners`.

### R13 — `net init` is the one command a new node runs

```
nova-pulse fleet net init --machines <registry> --node <name> --tailnet <name> \
    --owner <login> --out <dir> [--action-sha <sha> --action-version <tag>] [--force]
```

Three files out of the registry the node already keeps: `fleet/tailnet-policy.hujson`,
`.github/workflows/tailnet-acl.yml` and `docs/TAILNET.md`, written under `--out` at the paths
a repository keeps them (`--out .` in the node's own repository is the whole job). It is pure
file generation: no network, no machine touched, no model call.

It is **generic**: every rule in the generated policy is written from the registry's ROLES,
never from a machine's name, so a node with no bud gets no bud tag and no bud rule, and a new
bench needs a tag rather than an edit. It is **deterministic**: the same registry generates
the same bytes, so a re-run is an empty diff. And it **overwrites nothing** without `--force`
— and when it is going to refuse, it writes nothing at all, so a refusal on the third file
does not leave the first two behind.

> Red: `TestNetInitWritesTheThreeFilesFromTheRegistry`,
> `TestNetInitPolicyIsWrittenFromRolesAndNotFromNames`, `TestNetInitIsDeterministic`,
> `TestNetInitOverwritesNothingWithoutForce`, `TestNetInitRefusesEveryMissingFlagByName`,
> `TestNetInitDocIsWrittenForAnyNode`, `TestNetInitDocNamesTheTagEveryMachineAdvertises`.

### R14 — the policy is applied by Tailscale's GitOps action, tested on every pull request

`.github/workflows/tailnet-acl.yml` runs `tailscale/gitops-acl-action`, **pinned by its
40-hex commit sha** (a tag can move under you), on the two paths that matter:

| event | job | action | why |
| --- | --- | --- | --- |
| `pull_request` touching the policy | `acl-test` | `test` | the invariants run before anything is applied |
| `push` to the default branch | `acl-apply` | `apply` | a policy lands the way every change lands: reviewed, merged, then applied |

Nothing applies from a pull request — a fork's pull request must never reach a tailnet. The
action does the etag check itself, so an apply over a policy somebody edited in the console
fails instead of clobbering it. **The admin console is not the source of truth**; this
repository is, and a change made in the console is overwritten by the next apply, on purpose.

Until the node adds its API key, both jobs print a GitHub notice and do nothing: a node that
has not armed this yet must not have a red gate for a reason that has nothing to do with its
code (`red means stop` is for real red).

> Red: `TestNetInitWorkflowPinsTailscalesActionBySHAAndAppliesOnTheDefaultBranchOnly`, and
> internal/ci's own class tests over `.github/` (`TestEveryActionIsPinnedBySHA`,
> `TestNoGhPrMergeSpellingUnderDotGithub`).

### R15 — one secret, in the forge, and no key in an argv

`TAILSCALE_API_KEY` in the repository's secrets: an API key for **this node's tailnet and no
other**. Per-node tailnets mean per-node keys. It reaches the workflow through the `secrets`
context and reaches nothing else — not a flag, not a file in the tree, not a line any verb
prints.

On a machine, an auth key reaches `tailscale up` the way it already does: through the
environment variable `--authkey-env` names, filled by `nova-secrets exec` for the length of
the call, piped to the bench on the remote shell's stdin, in no argv on either machine, and
scrubbed out of anything the bench says back. **The seat**: the key is sealed in the secrets
store under the node's coordination seat, the one seat that opens it, and it is never copied
between machines.

> Red: `TestNetInitWorkflowPinsTailscalesActionBySHAAndAppliesOnTheDefaultBranchOnly` (which
> refuses anything shaped like a key in the generated workflow), and the existing
> `fleet-join-keeps-the-auth-key-out-of-every-argv`.

### R16 — the node's own people reach every machine they own

Glenn, 2026-09-18, travelling:

> If I am travelling, I want to work with all friends, including keeper you and all fleet
> machines from the air with no restrictions.

So `group:owners` is an accept to **every** tag — `tag:bench:*`, `tag:bud:*`,
`tag:coordination:*`, `tag:services:*` — and an `ssh` rule to every machine, as
`autogroup:nonroot` or `root`, **with no check**: a re-authentication prompt on an airport
connection is the thing that stops the work. R3 is a rule about what the tailnet's *tagged*
things may reach; it was never about the people whose tailnet it is.

The one narrowing is a runner host, which the owners reach on **`tag:runner:22`** rather
than on every port — Johnny's read: *"'Nothing but the forge' is no peer from buds or
benches. `group:owners` is not the swarm. Add one accept: `src: group:owners`,
`dst: tag:runner:22`."* An owner needs to administer the machine, not an arbitrary port on
it. Q1 records the one-line difference between the two rulings.

**An owner's own laptop joins UNTAGGED.** This is the trap, and it is worth saying twice: a
tag replaces a device's user identity, so a laptop that joined with
`--advertise-tags=tag:bud` is a bud to the ACL and reaches benches and nothing else, whoever
is typing on it. An owner's machine runs plain `tailscale up` and is covered by this rule.
Tag a bud only when it is not one of the node's own people's machines.

> Red: `TestNetInitLetsNothingTAGGEDReachARunnerHostAndLetsTheOwnersIn`, and the policy's
> own `tests` entry `{"src": "group:owners", "accept": [… "tag:runner:22" …]}`, which must
> PASS under `tailscale acl test` while the `tag:bud` and `tag:bench` entries deny the same
> destination.

---

## The output grammar

One line, always; a count where a list would be; every refusal with one remedy in
parentheses. `OK` lines and `MACHINE` lines to stdout, `REFUSED`, `FINDING` and `FINDINGS`
lines to stderr.

```
NET MACHINE <name> provider=<p> roles=<r,...> online=yes|no|- addr=<addr|-> last-seen=<rfc3339|->
NET FINDING machine=<name> reason=no-tailnet-node (<remedy>)
NET FINDING node=<name> reason=not-in-registry (<remedy>)
NET STATUS OK machines=<n> online=<n> findings=0
NET STATUS FINDINGS machines=<n> online=<n> findings=<n>
NET WROTE <path> <what was in it>
NET INIT OK node=<name> files=<n> machines=<n> rules=<n> tests=<n>
NET REFUSED verb=<v> reason=<token>: <what> (<remedy>)
NET MORE kind=<k> shown=<n> total=<t> <remedy>
```

**The refusal tokens**, which a loop may branch on:

| token | what happened |
| --- | --- |
| `not-implemented` | the verb is specified here and not yet written; the line names its rule |
| `no-machines` | no `--machines`, and the verb will not guess where the registry is |
| `unreadable` | the registry, or a path it was given, could not be read or written |
| `tailnet-unreadable` | the tailnet would not answer; the line carries its own words |
| `exists` | a file `init` would write is already there, and `--force` was not given |
| `unknown-node` | a name the verb needs (`--node`, `--tailnet`, `--owner`) was not given |

**The exit table:**

| code | meaning | which verbs |
| --- | --- | --- |
| 0 | it ran and the state it reports is consistent | `status` with no finding, `init` |
| 1 | the tool saying NO: the state is inconsistent and somebody must act | `status` with a finding |
| 2 | could not run: every refusal above | all |

---

## What certify runs

Three workloads, all read-only, none of which opens a socket in a test:

| workload | what it does | green when |
| --- | --- | --- |
| `net-status` | `nova-pulse fleet net status --machines <registry>` on the coordination machine | exit 0: every `tailnet` machine has a node and every node is in the registry |
| `net-ssh` | `nova-pulse fleet net ssh --machine <bench> --check` (R6) | the connection is made through the tailnet identity AND the far side carries no fleet key in `authorized_keys` |
| `net-names` | `nova-pulse fleet net names --machines <registry> --check` (R8) | nothing to write: MagicDNS answers for every `tailnet` machine, and each `lan` machine's marker block is already correct |

`net-status` is the one that exists today, which is why it is the one that was implemented
first. The other two are green when their rules are written, and until then they are
`not-implemented` refusals that certify reports as such rather than as passes.

---

## Open questions, each with a default, and the default stands unless somebody says otherwise

**Q1 — may the node's own people reach a runner host, and on which ports?** *Default: yes, on
ssh only* (R16). This question was asked and answered twice on 2026-09-18, and the default
below is where the two answers meet.

The first draft said no — "runner hosts accept nothing but the forge", taken literally, with
no exception for ourselves. Both reviews rejected it. Johnny: *"Too strict. 'Nothing but the
forge' is no peer from buds or benches. `group:owners` is not the swarm. Console-only admin
of mini/batman/superman is not how three machines are provisioned."* Glenn, travelling, went
further: *"all fleet machines from the air with no restrictions."*

So the owners reach every machine, and the one narrowing is the port range on a runner host:
`tag:runner:22` rather than `tag:runner:*`, which is Johnny's shape. It costs Glenn nothing
he asked for — ssh is how you work with a CI box — and it keeps the runner host's other
ports as closed as R3 makes them for everything tagged. **If Glenn wants `tag:runner:*`, it
is one line in `netAllTagDst` and one entry in the policy's `tests`.**

The *shape* question underneath it — is a shared bench-and-runner host a hole? — is no
longer open: R3 answers it by advertising `tag:runner` only on a runner-only machine.

**Q2 — should `tag:bud` be reachable at all?** *Default: no rule names it as a destination,*
so a bud is a source and never a target. A second bud cannot reach the first.

**Q3 — which branch does an apply run on?** *Default: `dev`,* because every merge lands there
and main is promoted (Glenn, 2026-09-16). A node whose default branch is `main` changes one
line of the generated workflow, and `net init` should probably learn a `--branch` flag when a
second node needs one.

**Q4 — Tailscale SSH `check` period for buds.** *Default: 12h.* Short enough that a lost
laptop stops being a key, long enough that a day's work is not interrupted.

---

## What this spec does not do

It does not move a byte of SPEC-JOBS or the fill loop: the tailnet is how a machine is
REACHED and the registry's roles remain what decides what may be placed on it. It does not
put a tailnet in the critical path of anything — the bus is git, and git is reachable without
one. It does not write an ACL language, an applier, a policy test runner or a DNS scheme,
because Tailscale has all four and the rule is that we use what is there.
