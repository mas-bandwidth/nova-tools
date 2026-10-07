# Dogfood Report: nova-local

## Date: 2026-10-06
## Tool: nova-local v1.0.1-0.20261006233510-fdee3091571a+dirty
## Testbed: vision (linux/amd64 go1.26.6)

## Findings

1. `nova-local status`
   Printed: STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve
   Expected: A clear message when no engine is running, with a remedy
   Grade: URGENT

2. `nova-local status --json`
   Printed: {"result":{"verb":"status","status":"failed","exit":1,"remedy":"ollama serve","why":["no engine answers (1 asked); start one, such as the ollama daemon"]},"facts":{}}
   Expected: Structured JSON output for programmatic use
   Grade: URGENT

3. `nova-local version`
   Printed: nova-local v1.0.1-0.20261006233510-fdee3091571a+dirty linux/amd64 go1.26.6
   Expected: Version and build info
   Grade: URGENT

4. `nova-local help serve`
   Printed: usage: nova-local serve [flags]
   ... (shows all flags and examples)
   Expected: Clear flag documentation with examples
   Grade: URGENT

5. `nova-local worker --engine ollama --model gemma4-32k --out /tmp/gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir /tmp/home --key-file /tmp/key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m --json`
   Printed: {"result":{"verb":"worker","status":"refused","exit":2,"remedy":"nova-local help","why":["--worker-dir /tmp/home is not a directory: mkdir -p /tmp/home","--key-file /tmp/key does not exist; the local engine wants no key, and nova-swarm a non-empty file: printf 'local\\n' > /tmp/key && chmod 600 /tmp/key"]},"facts":{}}
   Expected: Clear error messages with fix commands when required files are missing
   Grade: URGENT

6. `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run`
   Printed: SERVE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   Expected: --dry-run flag is accepted and prevents any action
   Grade: NEXT

7. `nova-local worker ... --dry-run`
   Printed: WORKER FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   Expected: --dry-run flag is accepted and prevents any action
   Grade: NEXT

8. `nova-local serve --stop --engine ollama --model gemma4-32k`
   Printed: SERVE FAILED: the engine did not unload gemma4-32k: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused
   Expected: Clear error when engine is not running
   Grade: URGENT

## Summary

READ 8/10 - The tool provides clear error messages, structured JSON output, and helpful remediation commands. The specification is well-documented.

USE 7/10 - Core verbs work well (status, version, help), but the --dry-run flag on serve and worker is not being read properly, which breaks a critical workflow for testing changes without making them.

urgent=5 next=3
