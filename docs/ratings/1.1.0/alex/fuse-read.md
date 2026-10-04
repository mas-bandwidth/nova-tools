# nova-fuse READ rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 044b5dfe9c1b
Score: 9/10
README: 9/10

## Reasons

Read cold, running nothing. The README row (README.md:36) says what the tool
is for in one sentence — a recorded decision to stop reading an untrusted
source, checked before every read — and the banner's first line
(cmd/nova-fuse/main.go:36) is that same sentence, the family rule kept. The
spec names the two powers and their hardness in seven lines
(docs/SPEC.md:1935-1941), the banner's example sitting runs as printed
(cmd/nova-fuse/main.go:84-96), and a reader finds the entry point, the verbs
and the one state file inside a minute: the box is one JSON file and its shape
is printed in the banner itself (cmd/nova-fuse/main.go:67-70).

Reading impressions, taken before any code: first confused at the generated
maps, which introduce this tool as a workspace isolation and boundary CLI
(internal/docs/catalog.go:32, with the same stale row for the state package at
internal/docs/catalog.go:67) — the one-line map is what a stranger reads
first, and it names a different tool's job; first bored at
docs/SPEC.md:2117-2138, where the widened-matching point is made three times
over, the both-directions rule, then its consequences, then the consequences
again; first doubt at cmd/nova-fuse/main.go:122, a comment asserting a past
build's byte count ("1,927 bytes to say a dash was in the wrong place") — a
claim this tree can no longer check, told as history under a present-tense law.

The spec section is the best writing in the repository: it is normative
(docs/SPEC.md:1949-1950), it gives every verb's asserts, says-no and refuses
conditions with exit codes (docs/SPEC.md:2140-2263), and it names its own
blind spot rather than hiding it — the double-blown quarantine invisible
behind a blown lockdown (docs/SPEC.md:2289-2299). The code bears out the
claims I checked: the atomic, exclusive create (internal/fuse/fuse.go:250-252
over internal/atomicfile), the fail-closed read that refuses a bare null
(internal/fuse/fuse.go:229-238), the one-line escape on every stored value
(cmd/nova-fuse/main.go:505-506). Comments say why, each rule naming the hurt
it answers (internal/fuse/fuse.go:7-68, notes 1 to 5), and the tests teach
the contract by name: lift lockdown refused forever, before anything is read
(cmd/nova-fuse/main_test.go:83,113), the environment cannot redirect or lift
anything (cmd/nova-fuse/main_test.go:159), one run naming every problem
(cmd/nova-fuse/firstrun_test.go:178). A TLA+ model with reversed witnesses
sits beside the verbs (tla/FuseBox.tla, cited from cmd/nova-fuse/main.go:652-656).

What it costs: the verb list is hand-synced in five places that must agree; the
comments, for all their why, are shouted and half historical; and the family
shape is broken twice without a named exemption — no verb takes --json and no
write verb takes --dry-run, where the -h refusal and the path exemption are
each named by name in the spec (docs/SPEC.md:2063, docs/SPEC.md:294), so a
reader of the set discovers these by refusal. A 10 would need the maps to
name the tool's real job, the war stories trimmed to the rules they taught, one
verb table behind the five copies, and the missing renderings named as
deliberate where the standard states them.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/docs/catalog.go:32 | the generated map introduces nova-fuse as a workspace isolation and boundary CLI, and the state package as workspace isolation boundaries (catalog.go:67), so the first map a stranger reads names another tool's job for this one | state the ingestion fuse's purpose in both rows and regenerate the maps | S |
| 2 | cmd/nova-fuse/main.go:47 | the verb list is hand-synced in five sites that must agree: the usage block (main.go:47), the dispatch switch (main.go:159), the unknown-verb refusals (main.go:173, help.go:77) and the help map (help.go:17); a verb added to one and missed in another meets the reader as a wrong answer | one slice of verb names the dispatch, the help and both refusals print from | S |
| 3 | cmd/nova-fuse/main.go:225 | the comments are shouted and half historical: the 1,927-byte refusal story (main.go:120-126) and the repeated-flag war story (main.go:225-238) keep the why but read as changelog under the tree's own present-tense law | keep each rule's why, drop the byte counts and the past-bug narration | M |
| 4 | cmd/nova-fuse/version.go:29 | no verb takes --json and no write verb takes --dry-run; the family standard states both, and unlike the -h and path exemptions (docs/SPEC.md:2063, docs/SPEC.md:294) the absence is named nowhere, so a reader of the set discovers it by refusal | one sentence in the spec's fuse section naming the one-line grammar as the only rendering, and why | S |
| 5 | cmd/nova-fuse/main.go:266 | the --box hint is a continuation line opening with neither NOTE nor a grammar token; the family rule says a continuation line opens with MORE or NOTE | prefix the hint with NOTE | S |

## Good, keep

- The read has one yes and two noes, and the noes are told apart only to
  name the right remedy (internal/fuse/fuse.go:214-243).
- Every write verb re-reads the box and says verified — the exit code of a
  remedy is not evidence the remedy worked (cmd/nova-fuse/main.go:385-396,
  573-578).
- The spec names its own blind spot, the double-blown quarantine behind a blown
  lockdown, and refuses to smuggle the repair in (docs/SPEC.md:2289-2299).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| its own output dialect | CHANGED | docs/SPEC.md:299-306 now blesses this binary's own event tokens and exit table; refusals keep the family's one-line shape with a run: door (cmd/nova-fuse/main.go:127-130) |
| shouted and historical comments | STILL THERE | cmd/nova-fuse/main.go:120-126 tells the 1,927-byte story and main.go:225-238 the repeated-flag story, in caps |
| three copies of its verb list | STILL THERE | five sites now: cmd/nova-fuse/main.go:47, main.go:159, main.go:173, help.go:17, help.go:77 |
| permissive box decoding | FIXED | internal/fuse/fuse.go:229-238 unmarshals through a pointer and refuses a bare null; a wrong shape fails the unmarshal and reads as blown |
| lost concurrent updates | CHANGED | still last-writer-wins, but named and bounded: one write lost, never a corrupt box (internal/fuse/fuse.go:36-37) |
