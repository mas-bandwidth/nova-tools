package main

import "strings"

// serverWords is how a real fleet connects, in nova-sprint help: one server, the
// coordinator's verbs through it, and the members and readers sending it theirs
// (docs/SPEC-SPRINT.md section 14, The server; serve.go, forward.go). Its indented
// run line is what `nova-sprint run -h` quotes.
func serverWords() string {
	return strings.TrimSpace(`
A real fleet: one server writes the sprint, beside its store (a Redis; a twin
cannot serve), and every other machine is its client. On the coordinator's
machine, with NOVA_SPRINT_REDIS naming the store:
  nova-sprint run --listen <address>:<port> --land
    ticks; serves the workers' verbs (take, finish, read, queue, fleet beat) on
    <address>:<port>, this machine's address on the fleet's private network (it
    checks no credential, so an every-network address such as 0.0.0.0 is
    refused); serves the coordinator's verbs on 127.0.0.1:<port>; with --land
    lands what the readers passed, so land is not run by hand beside it.
The coordinator's shell then sets NOVA_SPRINT_SERVER=127.0.0.1:<port> and
NOVA_SPRINT_ACTOR, and needs no store address or credential: every verb is sent
to the server, the reads included (where --watch and inbox --wait draw here and
read through it). Run where typed, on a store named there: run, tick, land,
play, fleet sync, and any verb given its own --redis. Each
member is a fleet row first (init --members, fleet up <name> --width <n>, or
fleet sync) and each reader a readers row (init --readers, reader add); then on
each fleet machine, which opens no store:
  nova-swarm member --as <name> --server <address>:<port> --harness <path> --root <dir>
  nova-swarm member --as <reader> --server <address>:<port> --reader --width <n> --harness <path> --root <dir>
The first beats, takes to its row's width and runs each card as one child; the
second runs up to <n> reads at once. A harness that reads its provider key from
the environment needs --pass <KEY>; a store with no routes (nova-config route)
needs --model, --tokens and --deadline; nova-swarm member -h names every flag.`) + "\n"
}

// wordsSection defines, one line each, the words the help and the verbs' output use,
// from docs/SPEC-SPRINT.md and docs/SPEC-CARD-CONTRACT.md.
func wordsSection() string {
	return strings.TrimSpace(`
words:
  primary      one unit of work, as add admits it: waiting, ready, working, review, merging, landed
  work card    <primary>.w<attempt>, one attempt handed to a member: s1-1.w1 is s1-1's first
  read card    <primary>.r<attempt>.<reader>, one reader's read of one attempt: s1-1.r1.reader-a
  kind         a card's: work or read (a primary is a sentinel or not); a note's: judgment, happened, decided
  sentinel     a primary that is a stop in its stream: what sorts after it waits until release
  judgment     an inbox note that needs the coordinator; it prints its decisions as commands
  HAPPENED     an inbox note that needs no decision: work came back ok, a member up or down, a batch landed
  DECIDED      an inbox note recording a judgment's answer: what was decided, by whom
  group        one inbox line: the notes of one type, stream and cause, named by its oldest note's id
  cursor       the coordinator's place in the notes; inbox --read moves it; it bounds HAPPENED and
               DECIDED lines, never the open judgments
  deal         the tick placing a ready primary's work card on an up member below twice its width
  level        moving cards dealt and not taken from a member that cannot start them to one with
               free lanes; asked reads are levelled across the readers up the same way
  drain        the tick's first update of the work table: every change steps queued since the last
               tick; MEMBER DRAIN is a member whose binary was replaced, taking no new card
  tier         a card's class of model, line 1 of its brief: tier: flash|pro|frontier (none is flash)
  route        a nova-config route row: tier, provider/model, token budget, deadline; the deal draws
               one of the card's tier for each work card, the ask one for each read
  provider     the first half of provider/model; a run the provider failed is redealt, never failed work
  head         the commit a work card finished at (finish --head, default the work card's id);
               reads and merges are of that head
  the stream's base  a first attempt's base in its packet: the branch its brief's BASE: line names
               (a rework starts from the attempt before's branch)
  held         a fleet member's status while fleet down or fleet sync holds it, whatever it beats;
               card's HELD line names what holds a primary from stalling
  stuck        the merge column of a card that could not merge: its stream stops until resume
  staging      native checking out the card's repository at its start commit before any child
               runs; staging refused is the member's failure, and the card goes to another member
  reaped       a launch whose claim moved (a clear, a redeal) or whose card left the member's queue
               (a drop, a return): nothing is reported
  resolve      moving waiting primaries whose needs landed to ready: the tick does it, the verb by hand
  quack        the sprint's end-to-end test cards: each asks for one file quacks/<id>.txt holding quack
  play         a seeded simulation of the world outside the tables (members, readers, merges)
               through the verbs, on a running machine
  wall         the nova-sandbox boundary a child runs inside; in a usage line, a run's wall-clock time`) + "\n"
}
