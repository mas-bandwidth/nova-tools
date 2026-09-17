## Open questions — each with a default, and the default stands unless Glenn says otherwise

1. **One pulse id over two batches.** `nova-swarm batch` takes one `--model`, so a pulse
   with both routes is two admissions. Default: one pulse id, `pulses/<id>.tsv` naming each
   batch, and `harvest --id` taking the pulse id; `batches=<n>` on the `PULSE` line says
   how many. Rejected: a per-card model in the swarm's sidecar, which is a swarm change.
2. **`UNDER-WIDTH` at exit 2.** SPEC.md's table reads a failed check as exit 1. Default: 2,
   as asked, because the alarm is a state the coordinator must act on and nova-wake fires
   on refusals; if the estate reads 1 as the right code, it is one constant and one replay.
3. **The dogfood issue shape without the label.** Default: pooled, template `fix`, because
   the shape is the contract (tool, command, verbatim output, expected, smallest fix) and a
   label is a hand's afterthought. Rejected: label only, which loses first-run stumbles.
4. **This spec needs `nova-swarm batch --then` (card 269) first.** Until it lands, `launch`
   admits and prints its line, and `harvest` is run by a person after `nova-swarm wait`;
   nothing else in this draft depends on it.
5. **What the shipped tool is behind on, named rather than assumed.** This is a draft, and
   `## Tests this spec demands` says so: the replays are demanded of the implementation, not
   read off it. Three deltas are open against `internal/pulse` at this draft's head, each one
   card's work: `pulseVerbs` lacks `--work` on `pool`, the `handoff`, `takeover` and `status`
   lines, and this draft's `--benches` and `--timeout`; `cut.go` prints `flash=<n> pro=<n>` on
   `CUT OK` where rule 7's cost table gives `zero=<n> flat=<n> metered=<n>`, and writes
   `cards.tsv` with four fields where rule 7 gives five; and `TestCutModelByKind` pins the
   routing replay 8 replaces. A fourth is rule 9's: `internal/pulse/launch.go` still writes
   `PULSE REFUSED UNDER-SLOTS` and `launch_test.go` pins it, `pulseVerbs` still offers
   `[--queue]` and carries none of the three gate flags — one card retires the refusal, turns
   that test into replay 9, drops `[--queue]` and adds the gates, in that order, so the red is
   the removal and never a silent behaviour change. Default: the spec leads, the cards follow,
   and no rule here is softened to match code that has not been written. Rejected: documenting the code as it is,
   which is how a draft stops being a design.
