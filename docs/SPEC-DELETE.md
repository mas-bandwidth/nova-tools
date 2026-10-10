# SPEC: nova-delete

## Summary

nova-delete moves exactly one literal absolute path to a dated quarantine folder instead of deleting it. The sweep verb removes old quarantine entries.

## Allowed roots

The system temp directory and paths named by `NOVA_DELETE_ROOTS` (colon-separated absolute paths).

## Behavior

### `nova-delete <path>`

Moves the path to `<root>/.quarantine-YYYYMMDD/<basename>.<HHMMSS>.<pid>` and prints `MOVED <path> -> <quarantine path>`.

**Refusals (exit 2):**
- No argument or more than one
- Empty argument
- Any of `$ * ? [ ]` or backtick or newline in the text
- Relative path
- `/` or any top-level root itself
- Symlink anywhere on the path
- Path outside allowed roots

**Skip (exit 0):**
- Path does not exist: `SKIP <path>: not there`

### `nova-delete sweep --older-than <duration>`

Removes quarantine entries older than the duration under allowed roots. Prints `SWEPT <path>` for each removed entry.

## Exit codes

- 0: success (path moved or skipped)
- 1: refusal (the verb ran and said no)
- 2: could not run (bad invocation)
