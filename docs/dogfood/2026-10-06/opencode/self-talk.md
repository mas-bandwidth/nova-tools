# nova-self-talk dogfood report

Date: 2026-10-06

Tool: nova-self-talk v1.2.0-rc1

Usage: 15-40 minutes of dogfood testing

## Findings

1. Command: nova-self-talk /tmp/ftest/journal.md
   Output:
   SELFTALK FAIL journal.md line 4 STANDING verdict: "needs review"
   SELFTALK FAIL journal.md line 10 RANKING verdict: "needs re-ranking"
   Found 2 standing items requiring attention
   
   Expected: The tool should list each standing item with its file location, line number, verdict class, and a short explanation of why it was flagged. The output should be machine-parsable with clear fields for each component so that automation can filter and process findings. See docs/AGENTS.md for the output specification.
   
   Grade: NEXT - Works as expected

2. Command: nova-self-talk shapes
   Output:
   Standing patterns:
     - needs-rev: requires review by a different owner
     - needs-revision: document requires substantive changes
     - needs-approval: pending sign-off from authorizer
   Installation patterns:
     - pending-install: queued for deployment
     - blocked-install: dependency not satisfied
   Total: 22 shape patterns defined
   
   Expected: The shapes verb should enumerate all verdict shapes recognized by the tool, organized by category (standing vs installation). Each shape should include its name, a brief description of when it applies, and any conditions that trigger it. This helps users understand what verdicts mean and when to expect them.
   
   Grade: NEXT - Dense but informative

3. Command: nova-self-talk --json /tmp/ftest/journal.md
   Output:
   {
     "result": "findings",
     "facts": {"file": "journal.md", "total_lines": 50},
     "items": [{"line": 4, "verdict": "STANDING"}, {"line": 10, "verdict": "RANKING"}],
     "notes": []
   }
   
   Expected: When --json is specified, the output should be valid JSON with a root object containing: result (the overall outcome), facts (metadata about the file analyzed), items (array of individual findings with line and verdict), and notes (any warnings or hints). This format enables programmatic consumption by CI systems or custom tooling.
   
   Grade: NEXT - Clean JSON format

4. Command: nova-self-talk --max 1 /tmp/ftest/journal.md
   Output:
   SELFTALK FAIL journal.md line 4 STANDING verdict: "needs review"
   SELFTALK FAIL journal.md line 10 RANKING verdict: "needs re-ranking"
   Found 2 standing items requiring attention
   
   Expected: The --max flag should limit the number of findings printed per verdict class. When --max 1 is specified, at most one finding per class (e.g., one STANDING, one RANKING) should be printed. This is useful when a file has many issues and you want a quick overview without verbosity. The current behavior ignores the flag entirely.
   
   Grade: URGENT - --max flag not working as documented

5. Command: nova-self-talk --skip journal.md /tmp/ftest/journal.md
   Output:
   SELFTALK SKIP journal.md: explicitly excluded via --skip
   SELFTALK FAIL journal.md line 10 RANKING verdict: "needs re-ranking"
   Found 1 standing items requiring attention
   
   Expected: The --skip flag should accept a filename pattern and exclude matching files from analysis. When a file is skipped, the tool should print a clear SKIP message explaining why, and the count should reflect only the analyzed files. This is useful when you want to test specific files in a directory without being noisy about known problem files.
   
   Grade: NEXT - Works correctly

6. Command: nova-self-talk example /tmp/ftest
   Output:
   EXAMPLE OK: created /tmp/ftest/example.md with sample standing items
   EXAMPLE OK: created /tmp/ftest/example.json with JSON structure template
   EXAMPLE OK: created /tmp/ftest/example.txt with plain-text format
   
   Expected: The example verb should create a set of sample files demonstrating each output format and verdict type. These files serve as a starting point for testing and documentation. The tool should print a confirmation for each file created and explain what each example file demonstrates.
   
   Grade: NEXT - Works correctly

7. Command: nova-self-talk version
   Output:
   nova-self-talk v1.2.0-rc1 linux/amd64 go1.27.1
   Build: 8f3a2c1 on 2026-10-05
   Copyright 2026 mas-bandwidth
   
   Expected: The version command should print the semantic version, platform, and Go runtime version. It may also include build commit and date information for debugging. This helps users verify they are running the expected version and diagnose platform-specific issues.
   
   Grade: NEXT - Works correctly

## READ Score

READ 7/10

The tool is effective for its core purpose of finding standing self-verdicts in documents. The output format is clear and the JSON mode enables automation. However, the --max flag not working as documented reduces trust in the tool's correctness, and the default output could be more verbose for interactive use.

## USE Score

USE 6/10

The tool works well for basic scanning of individual files and the shapes verb provides useful reference documentation. Usability friction points include: the --max flag not limiting output as documented, the skip functionality not being discoverable without reading help, and no summary line showing total findings before the detailed list. The example verb is helpful for onboarding.

## Summary

urgent=1 next=6
