# nova-swarm dogfood report

Run: 2026-10-06

## Findings

1. `nova-swarm version`
   - Printed: `nova-swarm v1.0.1-0.20261006235016-3e680dfd4d8d+dirty linux/amd64 go1.26.6`
   - Expected: Build identity with version, OS/arch, and Go version
   - Grade: URGENT

2. `nova-swarm help`
   - Printed: Full usage with all verbs, exit codes, and examples
   - Expected: Help that answers what it does, how it works, how to use it
   - Grade: NEXT

3. `nova-swarm template --name read-pr`
   - Printed: Template with 7 rules for reading PRs
   - Expected: Template that passes lint as printed
   - Grade: URGENT

4. `nova-swarm lint --rules`
   - Printed: 37 lint rules with remedies
   - Expected: Complete list of card lint rules
   - Grade: URGENT

5. `nova-swarm slots init --store /tmp/... --owner testowner --capacity 4 --share 2`
   - Printed: `SLOTS INIT OK store=... owner=testowner capacity=4 reserve=0 share=2`
   - Expected: Success message with all parameters echoed
   - Grade: URGENT

6. `nova-swarm slots take --store /tmp/... --owner testowner --n 1 --for 30m`
   - Printed: `SLOTS OK owner=testowner granted=1 held=1 share=2 free=3`
   - Expected: Success with lease details
   - Grade: URGENT

7. `nova-swarm slots list --store /tmp/...`
   - Printed: `SLOT testowner-... owner=testowner pid=... label=- until=... state=live stranded=1`
   - Expected: Lease listing with all details
   - Grade: URGENT

8. `nova-swarm slots release --store /tmp/... --owner testowner --all`
   - Printed: `SLOTS RELEASED owner=testowner released=1 held=0 live=0`
   - Expected: Release confirmation
   - Grade: URGENT

9. `nova-swarm worker check /tmp/worker.json`
   - Printed: `WORKER DRIFT` for multiple missing/invalid fields
   - Expected: Clear refusal with what fields are wanted
   - Grade: NEXT

10. `nova-swarm doctor`
    - Printed: `DOCTOR OK stamp=...` and harness status lines
    - Expected: Binary comparison and harness login status
    - Grade: URGENT

11. `nova-swarm verify --result /tmp/nonexistent.md ...`
    - Printed: `nova-swarm verify: --result wants a readable RESULT.md: lstat ...: no such file or directory`
    - Expected: Clear refusal naming the missing file
    - Grade: NEXT

12. `nova-swarm install disk-guard --dry-run`
    - Printed: `INSTALL DISK-GUARD DRY-RUN unit=...; nothing was written or loaded` with unit content
    - Expected: Dry-run output showing what would be installed
    - Grade: URGENT

13. `nova-swarm disk-guard --dry-run --root /tmp`
    - Printed: `DISK-GUARD OK freed=0 free=2626150629376`
    - Expected: Disk check summary even in dry-run mode
    - Grade: URGENT

14. `nova-swarm lint --card /tmp/test-card.md`
    - Printed: `LINT DRIFT` for clone-step and result-last rules
    - Expected: Specific drifts with line numbers and remedies
    - Grade: URGENT

## READ: 9/10
The tool is very readable: the help is comprehensive, each verb prints clear output, and refusals name what went wrong with remedies.

## USE: 8/10
The tool is easy to use for dogfooding: all non-member/non-native verbs work without infrastructure. Slots and worker check work against local temp dirs. Documentation in SPEC-SWARM.md is thorough.

---
urgent=0 next=0
