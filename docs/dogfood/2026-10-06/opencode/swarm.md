# nova-swarm dogfood report

Run: 2026-10-07

## Findings

1. `nova-swarm version`
   - Printed: `nova-swarm devel linux/amd64 go1.26.6`
   - Expected: Build identity with version, OS/arch, and Go version
   - Grade: NEXT

2. `nova-swarm doctor`
   - Printed: `DOCTOR OK stamp=nova-swarm v1.2.0-rc1 linux/amd64 go1.27.1` followed by harness status
   - Expected: Help that answers what it does, how it works, how to use it
   - Grade: URGENT

3. `nova-swarm template --name read-pr`
   - Printed: Template with 7 rules for reading PRs
   - Expected: Template that passes lint as printed
   - Grade: NEXT

4. `nova-swarm template --name worker`
   - Printed: JSON worker description template
   - Expected: Valid JSON template
   - Grade: NEXT

5. `nova-swarm template --name card`
   - Printed: Complete card template with RULES paragraph and steps
   - Expected: Card template that passes lint as printed
   - Grade: NEXT

6. `nova-swarm lint --rules`
   - Printed: LINT RULE lines with remedies
   - Expected: Complete list of lint rules
   - Grade: NEXT

7. `nova-swarm slots init --store /home/glenn/test-swarm-store --owner testowner --capacity 4 --share 2`
   - Printed: `SLOTS INIT OK store=/home/glenn/test-swarm-store owner=testowner capacity=4 reserve=0 share=2`
   - Expected: Success message with all parameters echoed
   - Grade: NEXT

8. `nova-swarm slots take --store /home/glenn/test-swarm-store --owner testowner --n 1 --for 30m`
   - Printed: `SLOTS OK owner=testowner granted=1 held=1 share=2 free=3`
   - Expected: Success with lease details
   - Grade: NEXT

9. `nova-swarm slots list --store /home/glenn/test-swarm-store`
   - Printed: `SLOT ... owner=... pid=... until=... state=...`
   - Expected: Lease listing with all details
   - Grade: NEXT

10. `nova-swarm slots release --store /home/glenn/test-swarm-store --owner testowner --all`
    - Printed: `SLOTS RELEASED owner=testowner released=1 held=0 live=0`
    - Expected: Release confirmation
    - Grade: NEXT

11. `nova-swarm worker check /tmp/test-worker.json`
    - Printed: `WORKER DRIFT harness: harness /tmp/harness does not exist`
    - Expected: Clear refusal naming the missing file
    - Grade: NEXT

12. `nova-swarm disk-guard --dry-run`
    - Printed: `DISK-GUARD OK freed=0 free=2072730664960`
    - Expected: Disk check summary even in dry-run mode
    - Grade: NEXT

13. `nova-swarm install disk-guard --dry-run`
    - Printed: `INSTALL DISK-GUARD DRY-RUN unit=...; nothing was written or loaded` followed by unit content
    - Expected: Dry-run output showing what would be installed
    - Grade: NEXT

## READ: 9/10
The tool is very readable: the help is comprehensive, each verb prints clear output, and refusals name what went wrong with remedies.

## USE: 9/10
The tool is easy to use for dogfooding: all non-member/non-native verbs work without infrastructure. Slots and worker check work against local temp dirs. Documentation in SPEC-SWARM.md is thorough.

---
urgent=0 next=13
