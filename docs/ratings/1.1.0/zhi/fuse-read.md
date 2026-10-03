# nova-fuse READ rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 2c02b2aa2042
Score: 8.5/10
README: 8.5/10

## Reasons

**README 8.5/10.** The README is a "You want to… / Tool / What it does / First command" index that gets a cold reader to the right tool and a runnable command in under a minute, and its honesty about prerequisites is the strongest thing on the page — the fuse row itself says "Your harness must consult it for the decision to take effect."

First **confused** — README.md:36: the fuse row's first command is `quarantine`, which needs an existing box, yet the cell says "Creates or updates a JSON box file"; a reader pasting it cold is refused (exit 2, no box), and the real first step, `init`, is only reached later at docs/CLI.md:301.

First **bored** — README.md:24-42: the sixteen-row table is one uniform four-column shape; by the middle rows the identical "what it does / first command" cells blur and the eye starts skipping.

First **doubted** — README.md:36: "checked before every read" is the page's strongest absolute, and it is only made true by the same cell's "Your harness must consult it": the tool records and answers, it does not check unless called.

The tool reads as writing even better than the index. `nova-fuse` says what it is for in three lines (README.md:36, cmd/nova-fuse/main.go:36-42, docs/SPEC.md:1935-1941) and the code does exactly that: one JSON box, two powers, a gate answer, and no enforcement. Entry point, verbs and data are found in a minute — `main` calls `run`, which dispatches eight verbs over one state file. Each file is one thing: main.go is the whole command (755 lines, the one heavy file), help.go the verb help, version.go the build line, lift_remedy.go the shell quoting, internal/fuse the state, internal/oneline the escaping, internal/bounded the listing cap. Names a stranger understands on first sight: box, fuse, lockdown, quarantine, surface, lift. Comments say why in the present tense and cite the rule — internal/fuse/fuse.go:1-64 is a design rationale that names each decision and why it is not arbitrary.

Two things keep this from a 10. First, weight and repetition: the folding-and-whitespace rule is stated three times (docs/SPEC.md:2103, internal/fuse/fuse.go:123-138, cmd/nova-fuse/main.go:740-755), and the spec section is near four hundred lines for a tool that stores one small file; a reader pays the same explanation three times. Second, the family divergence: nova-fuse does not use internal/tool, prints no `--json`, refuses `-h` after a verb, and inverts the exit table (0 is CLEAR, 1 is blown) — each justified by the design (a `-h` must never read as permission), but an AI that knows the rest of the family must unlearn three conventions to read this one. What a 10 would need: fold the repeated rule into one normative home, and lead the spec section with a one-line "why this tool is the exception" so the divergence reads as a decision, not drift.

The tests teach the contract — their names are the rules (`bare_verb_refusal_names_door_test.go`, `status_grammar_test.go`, `repeatflag_test.go`, `firstrun_test.go`) and tla/FuseBox.tla models the box. The claims the code bears out: an unreadable box is treated as blown (fuse.go:215-233), the write is temp-file + rename (fuse.go:253-269), the escape lives at print time (oneline.go:58-78), and exact mode 0644 is set through atomicfile.ExactMode (fuse.go:269).

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:36 | The fuse row's first command is `quarantine`, which refuses without an existing box, while the cell says "Creates or updates a JSON box file"; pasting it cold fails at exit 2. | Point the row's first command at `nova-fuse init --box ./trial-fuse.json` (or name init in the setup line), as docs/CLI.md:301 already does. | S |
| 2 | docs/SPEC.md:2103 | The folding-and-whitespace rule is stated three times (also fuse.go:123-138 and main.go:740-755), and the spec section is near four hundred lines for one small file, so a cold reader pays the same explanation three times. | Keep the normative statement once (in the spec) and have the code comments point at it instead of restating it. | M |
| 3 | docs/SPEC.md:2027 | nova-fuse diverges from the family skeleton: no internal/tool, no `--json`, `-h` after a verb refused, exit table inverted — all justified, but an AI that knows the other tools must unlearn three conventions to read this one. | Add one line at the top of the section: "this tool is the exception because its exit 0 means CLEAR", so the divergence reads as a decision, not drift. | S |

## Good, keep
- internal/fuse/fuse.go:1-64 — the package doc names every decision and why it is not arbitrary (one yes and two noes, temp-file + rename, normalized both ways, escape at print time); a model of comments that say why.
- The honest self-scope: cmd/nova-fuse/main.go:42 "The tool enforces nothing: your harness runs check first", and `status`'s "never gate on it" (docs/SPEC.md:2164-2173).
- The tests and tla/FuseBox.tla — test names are the contract, and the TLA model pins that only init, lift or a person's hand clears a surface.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| its own output dialect (2026-10-02, 1aac13259) | CHANGED | Still its own FUSE/LOCKDOWN/QUARANTINE grammar and inverted exits, now a named contract with its own exit table: docs/SPEC.md:2027-2038. |
| shouted and historical comments | CHANGED | Comments are present tense and cite the rule (main.go:1-17, fuse.go:1-64); ALL-CAPS emphasis remains as a deliberate style (main.go:224). |
| three copies of its verb list | STILL THERE | Verbs listed in main.go:47-56, docs/CLI.md:290-298, docs/SPEC.md:1943-1947 and help.go:17-63, by the repo's banner + reference + spec convention. |
| permissive box decoding | FIXED | fuse.go:215-233 unmarshals into a typed Box; malformed JSON or a wrong-shaped lockdown is unreadable, treated as blown (fuse.go:23-26). |
| lost concurrent updates | CHANGED | Still last-writer-wins, but now named rather than hidden: fuse.go:32-33 "two copies of the tool blowing fuses at once lose one WRITE, but neither can produce a corrupt box". |
