# nova-fuse READ rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 7.5/10
README: 7/10

## Reasons

README line (README.md:35): a recorded decision to stop reading an untrusted source, checked before every read.

Confused at README.md:35. The same cell says the decision is checked before every read and that a harness must consult the file before the decision takes effect. I could not tell whether the binary watches reads or only stores a decision.

Bored at README.md:48. The table had already named a first command for each tool. The page then restarts as an install essay for an older release, then a bus trial this tool does not use.

Doubted a claim at README.md:35. "Checked before every read" is stronger than a file a caller may forget to consult. The doubt held. The banner at cmd/nova-fuse/main.go:42 says the tool enforces nothing and the harness runs check first, and docs/USAGE.md:442 says a harness that does not check the box is not stopped by it.

The tool is a clear essay about one safety, and a weaker essay about its own bytes.

What it is for, in the banner's first lines: one JSON file, at most one lockdown and one quarantine per surface, and check exits 0 only when nothing blocks. The code does that. init never replaces a box (cmd/nova-fuse/main.go:669). lift lockdown returns before flags or files are read (cmd/nova-fuse/main.go:318). An absent or unreadable box is not clear (cmd/nova-fuse/main.go:491). Quarantine will not replace that box with a narrower one (cmd/nova-fuse/main.go:607). Lockdown still blows, and keeps the old bytes aside (cmd/nova-fuse/main.go:552). Names are plain: box, surface, lockdown, quarantine. The banner, the verb help, and the first-run transcript are a sitting a stranger can follow. Tests name the contract in the function name: a capital letter must not clear a surface, a second box flag must not answer for a blown box, a reason must not forge an OK line. tla/FuseBox.tla models the same verbs, with reversed witnesses for those fail-open mistakes. The double-blown blind spot is written down at docs/SPEC.md:2289 instead of left for a caller to trip over.

That is why this is not a 5. It is not a 9, because an AI that trusts the spec's sample lines will miss the gate, and an AI that trusts a hand-edited box will treat a typo as clear.

docs/SPEC.md:2043 prints the gate as FUSE FAIL. The binary prints FUSE FAILED (cmd/nova-fuse/main.go:499), and so do docs/CLI.md:311 and docs/TESTS.md:374. The same block says LOCKDOWN FAIL, QUARANTINE FAIL, LIFT FAIL, and INIT FAIL. The binary says FAILED (cmd/nova-fuse/main.go:374, cmd/nova-fuse/main.go:569, cmd/nova-fuse/main.go:625, cmd/nova-fuse/main.go:671). docs/SPEC.md:2177 says status lists quarantines in the box's own order. cmd/nova-fuse/main.go:447 sorts the names. The family shape, in the standard the map embeds, is one result value rendered as lines or as JSON. This tool has neither a JSON flag nor that result object. Exit 0 meaning clear is a real reason to refuse -h after a verb (docs/CLI.md:330, cmd/nova-fuse/main.go:63). It is not a reason for the spec and the binary to disagree about the token a caller matches.

internal/fuse/fuse.go:226 decodes with json.Unmarshal and keeps unknown keys. A lockdown key spelled wrong is a nil lockdown, and check exits 0. The only reset of a hard fuse is a hand edit (docs/SPEC.md:1940). A misspelled key is that reset by accident. Malformed JSON is treated as blown, which is the right direction. A well-formed file with a stray key is the hole.

Two writes are read, change, rename, with no generation check (internal/fuse/fuse.go:32). Two quarantines of different surfaces lose one. A second quarantine of the same surface replaces the time and the reason, and the OK line does not say a prior record was overwritten (cmd/nova-fuse/main.go:623). The comment admits the lost write and promises only that the file is not torn. For a record an AI may blow from two workers, torn-or-lost is the wrong pair. Lost can clear a surface the first writer had just stopped.

The verb list is written in the banner (cmd/nova-fuse/main.go:47), again in cmd/nova-fuse/help.go:17, again in docs/CLI.md:290, and again in two refusals that do not match each other. cmd/nova-fuse/main.go:173 names lift as one word. cmd/nova-fuse/help.go:77 names the two-word forms. Comments say why, which is the right job, and then shout it. internal/fuse/fuse.go:7 opens as a capitalized essay. cmd/nova-fuse/main.go:748 still says what a reason did before this branch. Present tense, a short why, would be the same design at a fraction of the weight.

A 10 would make the spec samples the bytes the binary prints, reject a box key the struct does not have, write a fuse only when the file is still the bytes just read and say so when a stamp is replaced, print one verb list from one place, and cut the comments to the decision they protect. The README sentence would match the limit already written at docs/USAGE.md:442: a cooperating harness checks, and the binary does not watch reads.

README 7/10. The fuse row is one sentence, a plain effect, and a command that runs. The same sentence overclaims. The first link on the page (README.md:16) is an install section aimed at an older release that never runs this tool (docs/USAGE.md:88), and the pages after the table are a bus trial. The honest limit is in docs/USAGE.md:442, which is the better door, and it is not the line the banner is required to copy.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:2043 | The sample gate line is FUSE FAIL, and the same block uses FAIL for lockdown, quarantine, lift, and init. The binary, the command reference, and the executed transcript all print FAILED, so a caller matching the spec misses every refusal. Status order in that spec is the box's own order; the binary sorts. | Print the spec samples as the bytes the binary writes, including FAILED, and say the status order is sorted. | S |
| 2 | internal/fuse/fuse.go:226 | json.Unmarshal drops unknown keys. A hand-edited lockdown key spelled wrong reads as no lockdown, and check exits 0. Malformed JSON is blown. A stray key is clear. The only hard-fuse reset is that hand edit, so a typo is a reset. | Reject unknown keys on the box and on each fuse object, and treat that file as unreadable. | M |
| 3 | cmd/nova-fuse/main.go:623 | A second quarantine of one surface overwrites the time and the reason, and the OK line does not say a prior record was replaced. Two overlapping writes lose one surface with no merge (internal/fuse/fuse.go:33). | Write only if the file is still the bytes just read, and when a stamp is replaced print the previous time and reason. | M |
| 4 | internal/fuse/fuse.go:7 | The state package opens as a capitalized essay, and cmd/nova-fuse/main.go:748 still narrates what a reason did on an older branch. The decisions are right. The volume hides them. | Keep one present-tense paragraph per decision and delete the history. | M |
| 5 | cmd/nova-fuse/main.go:173 | The unknown-verb refusal names lift as one word. Verb help at cmd/nova-fuse/help.go:77 names the two-word forms. The banner, the help map, and docs/CLI.md:290 are further copies of the same list. | One list, printed by the banner and by both refusals. | S |

## Good, keep

Exit 0 is the only permission, and a missing or unreadable box is not clear (cmd/nova-fuse/main.go:491). lift lockdown returns before any flag or file is read (cmd/nova-fuse/main.go:318). Quarantine refuses to narrow an unreadable or absent box, and lockdown still blows and keeps the old bytes (cmd/nova-fuse/main.go:607, cmd/nova-fuse/main.go:552). The double-blown blind spot is named at docs/SPEC.md:2289.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| its own output dialect | STILL THERE | docs/SPEC.md:2043 says FUSE FAIL; cmd/nova-fuse/main.go:499 prints FUSE FAILED; there is no JSON result object |
| shouted and historical comments | STILL THERE | internal/fuse/fuse.go:7 is a capitalized essay; cmd/nova-fuse/main.go:748 says what a reason did before this branch |
| three copies of its verb list | STILL THERE | cmd/nova-fuse/main.go:47, cmd/nova-fuse/help.go:17, and docs/CLI.md:290 |
| permissive box decoding | STILL THERE | internal/fuse/fuse.go:226 json.Unmarshal keeps a box whose lockdown key is misspelled, and check can exit 0 |
| lost concurrent updates | STILL THERE | internal/fuse/fuse.go:33 two writers lose one write; cmd/nova-fuse/main.go:623 replaces a stamp with no merge |
