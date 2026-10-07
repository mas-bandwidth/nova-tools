1. nova-self-talk version
   Printed: nova-self-talk v1.0.1-0.20261007135756-08d5d63f4651+dirty linux/amd64 go1.26.6
   Expected: Version string with build info
   Grade: NEXT

2. nova-self-talk shapes
   Printed: SHAPES OK rows=22 standing=1 installation=16 licensed=5
   Expected: Table of all detector rules
   Grade: NEXT

3. nova-self-talk example /tmp/nova-self-talk-test
   Printed: EXAMPLE OK dir=/tmp/nova-self-talk-test wrote=RULES.md,journal.md
   Expected: Two example pages written
   Grade: NEXT

4. nova-self-talk /tmp/nova-self-talk-test/journal.md
   Printed: SELFTALK FAIL /tmp/nova-self-talk-test/journal.md:4: STANDING match="cannot check": I cannot check my own work
   Expected: Findings from example file
   Grade: NEXT

5. nova-self-talk --skip journal.md /tmp/nova-self-talk-test/journal.md /tmp/nova-self-talk-test/RULES.md
   Printed: SELFTALK SKIP /tmp/nova-self-talk-test/journal.md (--skip)
   Expected: Skipped file reported
   Grade: NEXT

6. nova-self-talk --rule-doc RULES.md /tmp/nova-self-talk-test/journal.md /tmp/nova-self-talk-test/RULES.md
   Printed: SELFTALK RULEDOC /tmp/nova-self-talk-test/RULES.md: rule documents: a finding here is a self-verdict to relocate,
   Expected: Rule document banner printed
   Grade: NEXT

7. nova-self-talk shapes --json
   Printed: {"result":{"verb":"shapes","status":"ok","exit":0},"facts":{"rows":22,"standing":1,"installation":16,"licensed":5},...
   Expected: JSON output with same data
   Grade: NEXT

8. nova-self-talk help
   Printed: nova-self-talk: flags sentences where a writer passes a standing verdict on themselves
   Expected: Usage documentation
   Grade: NEXT

9. nova-self-talk help example
   Printed: usage: nova-self-talk example [flags]
   Expected: Verb-specific help
   Grade: NEXT

10. nova-self-talk /nonexistent/file.md
    Printed: nova-self-talk REFUSED: cannot read "/nonexistent/file.md": no such file or directory
    Expected: Clear error message with remedy
    Grade: NEXT

11. nova-self-talk --max 1 - (via stdin)
    Printed: SELFTALK MORE kind=standing shown=1 total=2 --max <n> raises the ceiling
    Expected: Truncated output with MORE line
    Grade: NEXT

12. nova-self-talk example --dry-run
    Printed: nova-self-talk example REFUSED: takes one directory to write the pages into, got 0 arguments
    Expected: Error for missing required argument
    Grade: NEXT

READ 8/10 - Documentation is present but verbose; example pages help start quickly.
USE 9/10 - Tool works as expected; flags like --skip and --rule-doc behave correctly.

urgent=0 next=12
