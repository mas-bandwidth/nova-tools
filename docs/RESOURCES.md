# Resources

What each machine in a fleet has, and what work takes from it.

This page is a design. Nothing on it is built. It was settled with Glenn on
2026-11-15. Rules marked *Rowan's* are choices he left to Rowan and can
overrule. [Not settled](#not-settled) lists what is still open.

Everything here belongs to nova-tools and works without nova-sprint.
nova-sprint is an optional extra that builds on it: its dealer uses resources
to decide where a card's work runs.

## The idea

Work requires resources, and can run anywhere those resources are provided.

Glenn: "The actual pattern is that cards require resources, and can run anywhere
those resources are provided. Be it CPU, memory, GPU, dev tools whatever..."

Today a fleet machine has one number that limits work: how many jobs it runs at
once. A job costs one of them whatever it does (or all of them, when it asks
for the whole machine), and CI jobs are not counted at all.
The number is a guess. With resources a machine says what it has, work says what
it needs, and how many jobs fit follows from the two.

The first thing this is for is sending work to the right machine: work that
needs a GPU, Metal, Windows or an attached device goes only where that exists.

## Two words: tags and resources

A **tag** is a name a machine has or does not have: `linux`, `mac`, `windows`,
`gpu`, `metal`. A tag is never used up. Any amount of work can run on a `linux`
machine at once; the tag only says where work may go.

A **resource** is a name and a whole number: `cpu 16`, `memory 64`, `ps5 1`.
Work holds an amount of it while it runs, and that amount is not available to
anything else until the work ends.

Amounts are whole numbers, never fractions, and the units are coarse on
purpose. Glenn: "the idea is to keep it simple and not have it super fine
grained."

| Resource | Unit | On a machine |
| --- | --- | --- |
| `cpu` | CPU units ([below](#cpu-units)) | `cpu 16` |
| `memory` | gigabytes | `memory 64` |
| a thing the machine has one of | 1 | `ps5 1` |

A thing is a resource with amount 1. It cannot be split: work holds all of it
or does not name it.

The same hardware can be a tag or a resource, and the choice says whether work
shares it. `gpu` as a tag means everything sent to the machine may use its GPU
at once. `gpu 1` as a resource means one piece of work holds the GPU.

Within a fleet a name is one or the other, on every machine. `gpu` is never a
tag on one machine and a resource on another.

## Machines

A machine's tags and resources are configuration, kept in nova-config beside
the machine. The link runs both ways: from a machine to what it has, and from
a tag or resource to the machines that have it.

| Machine | Tags |
| --- | --- |
| captain | `gpu` |
| studio | `gpu`, `metal` |
| mini-m5 | `gpu`, `metal` |

Work that names `gpu` can go to any of the three. Work that names `gpu` and
`metal` can go to studio or mini-m5, and never to captain.

The default set of tools is installed on every machine with Ansible, so the
fleet is as uniform as it can be made. Tags and resources are therefore not a
list of what is installed. They name what is counted (CPU, memory) and what
only some machines have.

## Work

A piece of work states what it needs as one list of names, some with numbers.

    cpu 4, memory 8, mac, metal, ps5

Here `cpu 4` and `memory 8` are amounts, and `mac` and `metal` are tags. `ps5`
is a resource with no number, and a resource with no number means 1. The
writer does not have to know which names are tags and which are resources:
each machine's configuration says what it has, and a bare name reads correctly
either way.

Work that names no `cpu` holds `cpu 1`, and work that names no `memory` holds
`memory 1` (*Rowan's*). Everything that runs uses some of both.

Work fits on a machine when the machine has every tag the work names, and for
every resource the work names, the amount already held plus the amount asked
is at most what the machine has. This one rule covers every resource, CPU
included.

Work that names something a machine does not have, or more of a resource than
the machine has in all, can never fit there. It is refused at once and does
not wait.

Work acquires everything it needs before it starts, holds it while it runs,
and releases all of it when it ends, however it ends. Together that is a
**hold**. A hold carries a short label saying what it is for, so anyone asking
what is held can tell a CI job from a build.

## What takes holds

Everything that uses a machine goes through holds. That is what makes the
numbers true.

- **Work an AI does.** An AI decides where its own work runs. Where its session
  runs does not matter; what matters is the machine it chooses to do the work
  on, which it reaches over ssh.
- **Work placed by nova-sprint,** for a fleet that uses it. The sprint decides
  where.
- **A CI runner.** Today a runner's jobs are counted nowhere. With resources a
  runner has tags (the labels a workflow selects it by) and takes a hold like
  anything else. It holds only while it runs a job. Glenn: "holds only when it
  runs work." An idle runner holds nothing, so no CPU is wasted while CI is not
  running. When a CI job does run, it comes ahead of other work, and other
  work waits for it. Glenn: "The design I want does not waste CPU while we are
  not running CI, so it does work, but then prioritizes CI ahead of work, when
  we do.", "CI can make cards wait".
- **A person or a script** running a command on a machine.

Glenn, on runners: "previously, they were sort of an invisible resource. now
they should become a thing that has tags, and grabs resources too".

## Holds on a machine: nova-reserve

nova-reserve is how a machine holds its resources. It is kept and upgraded.

Glenn: "nova-reserve is superceded by this design, or implements it. it is a
preview of this design, basically."

Today nova-reserve does the whole job for one unnamed resource. A machine has a
capacity in slots. A command run through it waits for a slot, holds it while it
runs, and frees it when it ends. On Linux it pins the command to that slot's
share of the CPUs, so `nproc`, `make -j` and Go size themselves to what was
reserved. It needs no server.

The upgrade is from one number to named numbers. A machine's capacity is its
tags and resources. A command states what it needs by name, and nova-reserve
holds exactly that. It still works on one machine with no fleet.

A hold always belongs to one command running on the machine, as it does today.
There are no tokens and no leases. When the command ends, for any reason, the
operating system frees the hold. An AI working on another machine runs each
command there through nova-reserve over ssh, so the hold lives and dies with
that command and not with the AI's session.

Asking for the whole machine stays, as it is today: it holds everything the
machine has.

Four rules, all *Rowan's*:

- **nova-reserve on a machine is the one authority on what is held there.**
  Two askers can never hold the same resources, whoever sent them.
- **A hold steers; it does not police.** It tells the work's tools how many
  CPUs they have. Nothing stops work using more than it holds. The measurement
  [below](#the-check) shows when it did.
- **No room means wait.** Work that fits the machine but finds no room now
  waits for it. Nothing running is ever stopped to make room.
- **Waiters are served strictly in arrival order** on a machine, as
  nova-reserve's roadmap already asks: a later, smaller request does not pass
  an earlier one. Sending important work first is the job of whoever sends it.

## Asking the whole fleet

nova-reserve knows one machine. To send work to the right machine, something
has to answer for the whole fleet: given tags and amounts, which machines have
them free. An AI, nova-sprint and a person all ask the same question.

The answer is advice, a moment old. The hold itself is still taken on the
machine, by nova-reserve, when the work starts there. If two askers were
pointed at the same free room, one gets it and the other waits or asks again.

This does not exist and is not designed. See [Not settled](#not-settled).

## What can be asked

nova-tools has no view of resources. It answers questions, for the AIs and
people who use the fleet:

- what tags and resources there are, and on which machines;
- what is held now, and by what;
- to be given resources, and to release them.

Glenn: "there is no resource view without a sprint. except whatever info the
AIs want to be able to query, to seew hat resources there are and ask for
resources/release them."

The answers cover everything that holds, CI included, so a picture drawn from
them is complete. nova-sprint's board draws one.

## CPU units

CPU is measured in CPU units, not cores, because cores differ in power from
machine to machine. Glenn: "i like cpu units", "not cores."

A machine has one CPU number: how much compute it has. Work says how much
compute it needs, and the machine it lands on works out how many of its own
cores that is. The same request is more cores on a slow machine than on a fast
one.

Glenn's example: "we could reserve say, 4 cores for a CI runner doing golang
tests that must finish in under 2m on a slower machine like hulk".

A machine's number is approximate, and that is accepted. Glenn: "this will all be
approximate, but better than what we have which is weirdly non-scientific
around guessing the # of cards per-machine".

### What a CPU unit is for

A CPU unit has one job: to let work say how much compute it needs, once, in a
way that means the same thing on every machine. Three things follow from that.

- Work can be sent to any machine with enough free, without its writer knowing
  the machines.
- The machine it lands on can turn the request into the right number of its
  own cores.
- What a machine hands out can be added up, and never comes to more than it
  has.

It is not a promise of speed and not a precise measurement.

### How big it is

*Rowan's.*

One CPU unit is one standard core: about one core of an ordinary machine.
"This build needs `cpu 4`" reads as "about four cores". A machine with fast
cores is worth more CPU units than it has cores, and a machine with slow cores
or hyperthreads is worth fewer.

Nobody ever sets or thinks about the number. Glenn: "no user of nova tools or
sprint will want to think for more than 1 second about how cpu units are
measured. they just want it working."

### How a machine gets its number

*Rowan's.*

nova-tools measures it. The benchmark is built into the tools: a small fixed
mix of the work a compiler and a test run do, for a few seconds, counting how
much got done. It runs with every thread busy, so hyperthreads and slow cores
are inside the measurement and need no rule of their own.

The standard core is a constant inside the benchmark, the same in every copy
of the tools, so one CPU unit is the same amount on every fleet. A machine's
CPU units are its score divided by the constant, rounded down, at least 1.

A machine is measured once, when it joins a fleet, or the first time
nova-reserve is used on a machine with no fleet. The number is kept, and it is
measured again only when asked.

### From CPU units to cores

*Rowan's.*

CPU is counted in CPU units, like every other resource. Cores are not counted.
They are only what the work is told to use.

A machine knows its CPU units and its cores: the CPUs nova-reserve can use on
it, hyperthreads included, counted by the tool. Work that holds some CPU units
is told to use a whole number of the machine's cores:

    cores to use = units held x machine's cores / machine's CPU units, rounded down, at least 1

With made-up numbers: a machine with 16 ordinary cores measures 16 CPU units,
so `cpu 4` is told to use 4 cores there. A machine with 16 stronger cores
measures 48 CPU units, so `cpu 4` is told to use 1 core, and `cpu 12` to use 4.

Rounding down means the cores handed out never come to more than the machine
has, except where several small pieces of work each get the minimum of one.
Those share cores, which is right: `cpu 1` on that stronger machine is a third
of a core. What work gets is within one core of what it asked for. That is
the approximation this design accepts.

Where nova-reserve can pin (Linux), it pins the work to that many cores. On
macOS it cannot, and the number is only what the work's tools are told.

### Work that cannot be split

*Rowan's.*

More cores do not help work that runs on one thread. It needs one fast core.

The benchmark also runs once on a single thread, which gives the speed of the
machine's best core. A machine whose best core clears a fixed speed has a
resource, `fast-core`: how many such cores it has. Work that cannot be split
asks for `fast-core 1`, so it is sent only where a fast core is free.
`fast-core` is a resource like any other.

## The check

Each machine still measures its real CPU use. The measurement is kept for
checking and can be asked for like the rest.

Glenn: "you should still have this data, so you can be aware of it internalyl
and check it".

What is held is a plan; what the machine measures is what happened. A machine
far busier than its holds has something running without a hold. Work that
uses far more or far less than it held was given the wrong numbers.

## The first version

*Rowan's.*

1. The benchmark, so every machine's CPU units are measured from the start.
2. nova-config keeps each machine's tags, CPU units and memory. CPU units and
   memory are filled in by measuring the machine; tags are written by its
   owner.
3. nova-reserve holds named resources on one machine.

Not in the first version: asking the whole fleet, `fast-core` and CI runners
taking holds. Each is a later step that builds on those three.

## Not settled

1. **Asking the whole fleet.** Which tool answers, and from what: each
   machine's own report of what it has free, and how fresh that must be.
2. **CI runners.** GitHub, not the fleet, chooses which runner gets a job, and
   it starts the job's clock when it hands the job over. If the runner then
   had to wait for room on a full machine, that wait would be part of the
   job's time, and a job that must finish in two minutes would fail without
   any test failing. So a CI job does not wait (*Rowan's*): it takes its hold
   at once, even if that puts the machine over what it has for a while. Other
   work on the machine runs at a lower priority, as it does today, so the CI
   job still gets its CPU, and nothing new is sent to that machine until it is
   back under. Giving way works for CPU: lower priority work slows down and
   the CI job runs at full speed. It does not work for memory, which cannot be
   taken back from work that is using it, so a machine short of memory when a
   CI job starts is a case this does not solve. Three more things remain open. A job is not one command, so what its
   hold belongs to, and how it is freed if the runner dies, is not designed. Who states how much a runner's jobs hold is not chosen (the
   runner's configuration is the likely place). And nova-reserve has no
   Windows build, while the fleet has Windows runners.
3. **Operating system and architecture as tags.** nova-config already holds
   both for each machine. Whether they become tags automatically, or every tag
   is written by hand, is not chosen.
4. **The speed that makes a core fast.** It is set after the fleet has been
   measured, from the numbers.
5. **Memory on a machine with no way to hold it.** Holding memory is a count,
   not a limit. Whether that is enough is not known until it is used.

## Ideas for later

Not designed, and not started.

- **Work across several machines.** Glenn: "maybe a card could even grab
  resources across mulitple machines for a friend to work on". Work would
  state more than one set of needs, each held on one machine, and would hold
  all of them or none, so two pieces of work cannot block each other.
- **State a deadline, not an amount.** Work says "finish in two minutes". The
  system learns the size of each kind of work from the measurement and picks
  how much to hold.
- **Split work across machines.** A test suite that needs 64 CPU units gets
  them as shards on several machines.
- **Rent what the fleet lacks.** When work waits on a resource, the fleet knows
  exactly what is missing and can rent it and hand it back, within a budget
  its owner sets.
