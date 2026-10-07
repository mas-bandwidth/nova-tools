# nova-self-talk dogfood report

Date: 2026-10-06

Tool: nova-self-talk v1.2.0-rc1

Usage: 15-40 minutes of dogfood testing

## Findings

1. Command: nova-self-talk /tmp/ftest/journal.md
   Output: SELFTALK FAIL on journal.md line 4 (STANDING) and line 10 (RANKING)
   Expected: Clear finding output
   Grade: NEXT - Works as expected

2. Command: nova-self-talk shapes
   Output: Lists 22 shape patterns for standing and installation classes
   Expected: Comprehensive shape documentation
   Grade: NEXT - Dense but informative

3. Command: nova-self-talk --json /tmp/ftest/journal.md
   Output: JSON output with result, facts, items, and notes fields
   Expected: JSON output for programmatic parsing
   Grade: NEXT - Clean JSON format

4. Command: nova-self-talk --max 1 /tmp/ftest/journal.md
   Output: Still prints all findings instead of limiting to 1 per class
   Expected: Only 1 finding per class printed
   Grade: URGENT - --max flag not working as documented

5. Command: nova-self-talk --skip journal.md /tmp/ftest/journal.md
   Output: SELFTALK SKIP for the skipped file
   Expected: File skipped with clear skip report
   Grade: NEXT - Works correctly

6. Command: nova-self-talk example /tmp/ftest
   Output: EXAMPLE OK with example pages created
   Expected: Example pages created
   Grade: NEXT - Works correctly

7. Command: nova-self-talk version
   Output: nova-self-talk v1.2.0-rc1 linux/amd64 go1.27.1
   Expected: Version info
   Grade: NEXT - Works correctly

## READ Score

READ 7/10

The tool is effective for its core purpose of finding standing self-verdicts, but the --max flag behavior does not match the documentation.

## USE Score

USE 6/10

The tool works well for basic scanning but has usability friction points: the --max flag does not limit output as documented, and the default output format is somewhat terse.

## Summary

urgent=1
next=6
