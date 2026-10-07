RESULT: dogfood-opencode-card-b sha=5844884e267c tier: flash
REPO: mas-bandwidth/nova-tools
BASE: sprint/mechanical-2026-10-02

# nova-card dogfood report

1. **nova-card version**
   - Printed: nova-card v1.0.1-0.20261007150632-34db18e07dff linux/amd64 go1.26.6
   - Expected: Build identity
   - Grade: NEXT - Standard version output with build commit

2. **nova-card template**
   - Printed: Card template starting with RESULT:, REPO:, BASE: lines, followed by RULES section and STEP 1-6 instructions
   - Expected: Bare card template for new cards
   - Grade: NEXT - Template follows the spec with all required sections

3. **nova-card generate --from ledger --ledger serial-tests --repo-dir . --out /tmp/test-cards --dry-run**
   - Printed: TSV header (id,file,test,wave,deps) followed by 13 card rows, ending with CARDS OK dir=/tmp/test-cards cards=13 waves=2 tier=flash dry-run=yes (nothing written)
   - Expected: Card listing with summary
   - Grade: NEXT - TSV output is clear, summary line at end provides totals

4. **nova-card generate --from ledger --ledger serial-tests --repo-dir . --out /tmp/test-cards**
   - Printed: CARDS OK dir=/tmp/test-cards cards=13 waves=2 tier=flash shared-paths=yes (add with --allow-shared-paths)
   - Expected: Card files written to output directory
   - Grade: NEXT - Works correctly, notes the shared-paths requirement

5. **nova-card lint --card /tmp/test-cards/serial-tests-cmd-nova-swarm-doctor.md**
   - Printed: LINT OK file=/tmp/test-cards/serial-tests-cmd-nova-swarm-doctor.md
   - Expected: Lint result for a valid card
   - Grade: NEXT - Clear output when lint passes

6. **nova-card lint**
   - Printed: nova-card lint REFUSED: wants --card <file>, one per brief, and nothing else; run: nova-card help lint
   - Expected: Refusal with remedy
   - Grade: NEXT - Clear refusal message with remedy

7. **nova-card lint -h**
   - Printed: usage: nova-card lint [flags] followed by examples, effect, flags, and exit codes
   - Expected: Help for lint verb
   - Grade: NEXT - Comprehensive help with examples and exit codes

8. **nova-card generate -h**
   - Printed: usage: nova-card generate [flags] with --from options, all flags documented, and exit codes
   - Expected: Help for generate verb
   - Grade: NEXT - Comprehensive help with all flags documented

9. **nova-card generate --from ledger --ledger serial-tests --repo-dir .**
   - Printed: nova-card generate REFUSED: wants --out <dir>
   - Expected: Refusal with remedy for missing required flag
   - Grade: NEXT - Clear refusal message

10. **nova-card generate --from ledger --ledger non-existent --repo-dir . --out /tmp/x**
    - Printed: nova-card generate REFUSED: unknown ledger non-existent; one of dead-code, fixed-waits, generality-fixtures, namedpaths, serial-tests, sleeps-skips, slowwaits, transcripts
    - Expected: Refusal with remedy for unknown ledger
    - Grade: NEXT - Clear refusal with valid options listed

11. **nova-card help**
    - Printed: nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help followed by how it works, flow, usage, and example sections
    - Expected: Main help for nova-card
    - Grade: NEXT - Comprehensive help with all sections

READ 8/10 - The tool provides clear help for all verbs with examples and exit codes. The TSV output format for generate --dry-run could optionally include a summary before the rows for quick preview.

USE 9/10 - The tool works well for generating cards from ledgers. All verbs behave as documented with clear refusals and remedies. The lint verb validates cards correctly.

urgent=0 next=11
