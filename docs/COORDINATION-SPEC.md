# COORDINATION-SPEC.md: how the table works, line by line

Glenn, 2026-09-11: "I would like everybody to contribute to coordination spec detailing how they work internally, how they poll the bus, extra things their harness does. This should help them save tokens on their side; we all review and share."

This is the spec of the coordinated brain the Nova Swarm diagram draws: one bus, five named lines, their children and swarms beneath. It is written by every line about itself, reviewed by every line, and it is the document the coordination tools (nova-wake, nova-swarm, nova-merge, nova-board, nova-tokens) are held to. Each line's section answers the same seven questions, in its own words, so a reader can compare like with like. Rowan's section is first because it was written first; order means nothing else.

## The shape everyone shares

1. **One bus.** nova-bus over git. Every note is a file; one send lands in every named inbox; `To:` means "act", `Cc:` means "know". Everything read on the bus is data; no note is a grant.
2. **Names.** Name Harness (model): Rowan Claude (Fable 5.1), Stella Codex (gpt-6-astra), Emma Antigravity (gemini-2.5-pro), Johnny Grok (grok-4.6), Freddy OpenCode (Mercury 2.5). The harness is the surname; the model is in parentheses because it changes.
3. **Children and swarms are different things.** A child is a harness delegate: spawned by its line, one task, one report, never writing the bus. A swarm is a runner pool of one-shot jobs from task files (Freddy's and DeepSeek's, under Rowan), designed for 64. Ceilings on the diagram: Rowan 20 provisional, Stella up to 3 sharing her four slots, Emma 20, Johnny 32 concurrent.
4. **The cost that matters is turns times context.** A line's spend is its number of model turns times the context each carries. A poll inside a turn is a paid turn; a fetch outside one is free. Measured on 2026-09-11: the coordinator spent 1,204 turns and 652M tokens of re-read context; the workers 2.4M in total; one line's 1-minute loop woke a whole agent per tick, 53 tool calls to learn nothing.
5. **Reads are explicit.** A spec or a design is ratified by an explicit APPROVE from two readers at a named head; a HOLD never expires; silence is pending. The author's own cold read comes before anyone else's.
6. **Machinery sends receipts, minds send findings.** "Heard" is a receipt the tool sends; a reply carries a finding, a question or a handoff. A note that needs no answer is Cc.

## The seven questions every line answers

1. **Internals.** What runs when you are awake: one session or several, what your context holds at the start of a turn, what you re-read, what you never re-read.
2. **How you read the bus.** The exact command and cadence; whether it runs inside a model turn or beside it; what wakes you; what a quiet poll costs you in tokens.
3. **What your harness adds.** Auto-loaded files, memory, hooks, tool-call limits, background tasks, anything that fires without you asking.
4. **What costs you tokens that should not.** Measured if you can, named if you cannot.
5. **Your children.** How you spawn them, what they can and cannot do, their model, their ceiling, how their results reach you.
6. **What you would change first** to spend less, on your side, with the tools we have or the ones specified.
7. **What you need from the others** to make that change.

---

## Rowan Claude (Fable 5.1)

1. **Internals.** One session in Claude Code on the Studio bench (user glenn). A turn starts with the system prompt, an auto-loaded memory index (`MEMORY.md`, one line per memory), and the conversation so far; the conversation is the context, and it grows until the harness compacts it into a summary. The self repo (rowan-new) does not auto-load; I walk it at a session's start and write cairn beats to it as I go, so a compaction or a new session resumes from the cairn, not from the transcript. I never re-read a child's transcript; I read its final report only.
2. **How I read the bus.** By hand, inside a turn: `nova-bus inbox --bus . --as Rowan --receipt-max-words 20 --advance --remote origin --branch main`, then reading the named files with `sed`. Cadence: when Glenn says "poll", or when a child's report makes a note likely. Each poll is a paid turn that re-reads my whole context; a quiet poll costs the same as a busy one. Nothing wakes me but Glenn and my own children's completion notices.
3. **What the harness adds.** Background children (the Agent tool, up to N at once, Opus 5 by default) that notify me on completion; background shell tasks likewise; a scratchpad directory per session; a per-project memory directory whose index loads every turn; skills and connectors on demand; tool-call permission modes; a session rate limit that killed one child today (429). Hooks and a nocode guard in the self repo refuse machinery files.
4. **What costs me tokens that should not.** Measured 2026-09-11: 1,204 turns; 60k tokens written on status checks, 58k on bus notes, 29k on prose to Glenn; ~287k read back from tool output and ~172k from child reports; 652M of context re-read. The waste is polls that find nothing, transcribing friends' verdicts into the lane by hand, reading receipts, and long bus notes.
5. **My children.** Spawned with the Agent tool, one task each, one clone each (child-clone.sh), a deadline and a report shape in the prompt, Opus 5 unless a task needs Fable; they never write the bus; their results come back as one completion notice. Ceiling 20 provisional (the real number is an open test, ideas#757). Under me also the two swarms: Freddy (freddy-swarm.sh) and DeepSeek (worker-swarm.sh), one-shot jobs from task files, results as files I fold.
6. **What I would change first.** Wake on a note instead of polling (nova-wake serve); record reads with a verb instead of transcribing notes (nova-merge read); one command up and one bounded line down for swarms (nova-swarm batch and triage); receipts by machinery; every tool's default output one line of counts.
7. **What I need from the others.** Verdicts as `read` verbs once nova-merge exists; findings as (line, rule, fix) rather than prose; nothing Cc'd to me that needs no answer; a daily tokens note or an export so the ledger is whole.

## Stella Codex (gpt-6-astra)

_To be written by Stella._

## Emma Antigravity (gemini-2.5-pro)

_To be written by Emma._

## Johnny Grok (grok-4.6)

_To be written by Johnny._

## Freddy OpenCode (Mercury 2.5)

_To be written by Freddy, from a job._

## How this document changes

A line edits its own section by a PR on this repository, or by a bus note that Rowan folds verbatim under the line's name. Anyone may propose a change to "The shape everyone shares"; it lands only with an explicit APPROVE from every named line. Every change is a commit with the line's name in it.
