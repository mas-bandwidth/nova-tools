RESULT: dogfood-opencode-card-b sha=5844884e267c tier: flash
REPO: mas-bandwidth/nova-tools
BASE: sprint/mechanical-2026-10-02

# nova-card dogfood report

1. **nova-card -h**
   - Printed: Usage banner showing generate, lint, template, version, help verbs with example flows
   - Expected: Clear usage with examples for a new user
   - Grade: NEXT - The help is comprehensive but could benefit from more prominent examples at the top

2. **nova-card generate --from ledger --ledger serial-tests --repo-dir ./repo --out ./cards --dry-run**
   - Printed: TSV listing 156 cards with id, file, test, wave, deps columns; CARDS OK line at end
   - Expected: Card listing with summary
   - Grade: URGENT - The output is a TSV to stdout which is not immediately human-readable; a summary line before the TSV would help

3. **nova-card generate --from ledger --ledger serial-tests --repo-dir ./repo --out ./cards**
   - Printed: CARDS OK dir=./cards cards=5 waves=2 tier=flash shared-paths=yes
   - Expected: Card files written to output directory
   - Grade: NEXT - The shared-paths=yes flag could be more prominent in the output

4. **nova-card generate --from help --tool nova-bus --out ./cards --dry-run**
   - Printed: TSV with help-nova-bus card; CARDS OK line
   - Expected: Help-based card generation
   - Grade: NEXT - Works well but could include tool version in the card

5. **nova-card lint --card ./cards/serial-tests-cmd-nova-bus-issue1451.md**
   - Printed: LINT OK file=./cards/serial-tests-cmd-nova-bus-issue1451.md
   - Expected: Lint result
   - Grade: NEXT - Clear output when lint passes

6. **nova-card lint**
   - Printed: nova-card lint REFUSED: wants --card <file>, one per brief, and nothing else
   - Expected: Refusal with remedy
   - Grade: NEXT - Clear refusal message

7. **nova-card template**
   - Printed: Card template with RESULT, REPO, BASE, RULES, STEP 1-6 sections
   - Expected: Bare card template
   - Grade: NEXT - Template is complete and follows spec

8. **nova-card version**
   - Printed: nova-card devel linux/amd64 go1.26.6
   - Expected: Build identity
   - Grade: NEXT - Standard version output

9. **nova-card new test-card --repo test/repo --base main --task-file ./task.txt --paths "cmd/test/*.go" --test "cmd/test TestMain" --gate "cmd/test" --tier pro**
   - Printed: CARD OK file=/tmp/new_card.md
   - Expected: New card created
   - Grade: NEXT - New verb works well for creating custom cards

10. **nova-card generate --from ledger --ledger serial-tests** (missing --out)
    - Printed: nova-card generate REFUSED: wants --out <dir>
    - Expected: Refusal with remedy
    - Grade: NEXT - Clear refusal

READ 7/10 - The tool has all expected verbs but the TSV output format for generate --dry-run makes it harder to quickly grasp the summary. A more concise summary mode or summary flag would help.

USE 8/10 - The tool works well for its stated purpose of generating cards from ledgers and help output. The generate, lint, new, and template verbs form a coherent workflow. The help is available and clear. Missing: --max flag works but could be more visible in usage; tier inference from source could be more explicit.

urgent=0 next=10
