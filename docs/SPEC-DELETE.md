# nova-delete

Move a literal path to quarantine instead of deleting it.

## Purpose

`nova-delete` is a cleanup tool that moves files and directories to a dated quarantine folder instead of permanently deleting them. This provides a safety net for AI automation and scripts that need to clean up files.

## Usage

```
nova-delete <path>
nova-delete sweep --older-than <duration>
```

## Behavior

### `nova-delete <path>`

Takes exactly one literal absolute path and moves it to a dated quarantine folder under the allowed root that holds it. The quarantine path format is:

```
<root>/.quarantine-YYYYMMDD/<basename>.<HHMMSS>.<pid>
```

**Allowed roots:**
- System temp directory (`os.TempDir()`)
- Paths named by `NOVA_DELETE_ROOTS` environment variable (colon-separated absolute paths)

**Refusals (exit 2):**
- No argument or more than one argument
- Empty argument
- Path contains glob characters (`*`, `?`, `[`, `]`) or backtick or newline
- Relative path
- Path is `/` (root filesystem)
- Path is an allowed root itself
- Path contains a symlink anywhere in the path chain
- Path is outside all allowed roots

**Success (exit 0):**
- Moves the path to quarantine and prints: `MOVED <path> -> <quarantine path>`

**Skip (exit 0):**
- Path doesn't exist: prints `SKIP <path>: not there`

### `nova-delete sweep --older-than <duration>`

Removes quarantine entries older than the specified duration under all allowed roots. Prints `SWEPT <path>` for each removed entry.

**Refusals (exit 2):**
- Missing `--older-than` flag

## Exit Codes

| Code | Meaning |
|------|---------|
| 0    | Success (path moved or skipped) |
| 1    | Refusal (verb ran and said no) |
| 2    | Bad invocation (could not run) |

## Examples

```
# Set allowed roots
export NOVA_DELETE_ROOTS=/tmp:/Users/user/data

# Move a file to quarantine
nova-delete /tmp/to_delete.txt
# Output: MOVED /tmp/to_delete.txt -> /tmp/.quarantine-20261010/to_delete.txt.030103.12345

# Move a directory to quarantine  
nova-delete /Users/user/data/old_project
# Output: MOVED /Users/user/data/old_project -> /Users/user/data/.quarantine-20261010/old_project.030104.12346

# Clean up old quarantine entries
nova-delete sweep --older-than 7d
# Output: SWEPT /tmp/.quarantine-20261003/...

```
