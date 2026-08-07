# nova-check — specification

One binary, four checks, all at the **record layer**: they verify what is on disk,
not what a mind did with it. Every check can say NO, and the test suite proves each
one saying it. A check never seen failing is not a check.

This spec is normative. If the code and this document disagree, one of them has a
bug, and the tests decide which.

## Conventions

**Exit codes.**

| code | meaning |
|------|---------|
| 0    | the check ran and passed |
| 1    | the check ran and **failed** (that is the check working) |
| 2    | the check could not run: missing flag, unreadable input, bad invocation |

**No guessed paths.** There are no default directories and no default files.
Every path comes from a flag. A missing flag is a refusal (exit 2) with the
message `refusing to guess`, never a fallback to cwd, `$HOME`, or any
hardcoded location. A budget of zero or less is likewise refused, not treated
as "unlimited".

**Output grammar.** One machine-scannable line per event, first token names the
check, second token is `OK` or `FAIL`:

```
ATTEST OK files=<n> bytes=<n> sha256=<64 hex>
ATTEST FAIL <path>: <reason>
LINKS OK files=<n> links=<n>
LINKS FAIL <file>:<line>: <target> (<reason>)
KERNEL OK bytes=<n> budget=<n>
KERNEL FAIL <file>: <reason>
NOCODE OK files=<n> clean
NOCODE FAIL <path>: <reason>
```

`OK` lines go to stdout; `FAIL` lines and refusals go to stderr.

---

## attest — did the full self actually load

```
nova-check attest --home <dir> --manifest <file>
```

The manifest is the boot contract: the list of files a full boot must read, one
path per line, **relative to `--home`**, forward slashes, `#` comments and blank
lines ignored. Order matters (it is the boot order, and the hash binds it).
Each entry must already be **canonical**: written exactly as its cleaned
relative path — no `./` prefix, no `//`, no `.` or `..` segments, no trailing
`/`. Near-duplicate spellings of one path would dedupe apart and double-count
bytes, so a non-canonical entry is a failure, not a normalization.

**Asserts.** Every manifested file exists under `--home`, is a regular file, and
is non-empty. On success prints exactly one line — file count, total bytes,
and a SHA-256 — suitable for pasting at the top of a session as evidence that
the self on disk at boot was this self.

**The hash, exactly.** SHA-256 over the concatenation, for each manifest entry
in manifest order, of:

```
uvarint(len(entry-path))  entry-path  uvarint(len(contents))  contents
```

where `uvarint` is Go's unsigned varint encoding (`encoding/binary`).
Length-prefixed framing keeps the encoding injective even when file contents
contain NUL bytes — no split of one file's bytes can imitate another manifest.
Binding the path prevents two files swapping contents without moving the hash;
binding the order makes the manifest itself part of what is attested.

**The attestation is the full OK line** — `files=`, `bytes=`, and `sha256=`
together — not the bare sha. Paste all of it or none of it.

**Says NO when** (each a named `ATTEST FAIL` line, exit 1):

- a manifested file does not exist
- a manifested file exists but is empty (0 bytes) — a truncated self must not attest
- a manifested file exists but cannot be read — a named failure, not a refusal
- a manifested path is not a regular file (directory, device, or symlink —
  symlinks are never followed, even one that resolves)
- a path component under `--home` is a symlink (never followed, even one that
  resolves inside `--home`) — a symlinked directory could otherwise attest
  files outside the home, and the OK line is pasted publicly
- a manifest entry is an absolute path
- a manifest entry is not canonical (`./a.md`, `a//b.md`, `a/../b.md`, `dir/`)
- a manifest entry escapes `--home` (leading `../`)
- an entry appears twice (double-counted bytes are a lie)
- the manifest lists no files at all — attesting to nothing is not attestation

**Refuses (exit 2) when** `--home` or `--manifest` is missing, the manifest is
unreadable, or `--home` is not a directory.

**Deliberately does not check:** that anything *read* the files (presence and
bytes, not comprehension — no tool can attest that a mind loaded a self);
files present in `--home` but absent from the manifest (extras are invisible
here; `nocode` and human review cover the tree); permissions, mtimes, or
content semantics; anything about the session that pastes the line.

---

## links — every internal reference resolves

```
nova-check links --dir <dir>
```

**Asserts.** Every relative link target in every `.md` file under `--dir`
resolves to an existing file or directory inside the tree. Walks the whole
tree, skipping `.git`.

**What counts as a link.** Inline links and images: `[text](target)` and
`![alt](target)`, with an optional title in any of the three CommonMark forms
(`"double"`, `'single'`, `(parenthesized)`). The destination may be wrapped in
angle brackets (`[a](<my notes.md>)`), which is the only way to link a target
containing spaces. Link text may nest a link or image — in the badge pattern
`[![alt](img)](target)` both `img` and `target` are checked. Fenced code
blocks (``` or `~~~`) and inline `` `code` `` spans are stripped first so
examples do not count; a fence closes only at the marker that opened it — a
` ``` ` block containing `~~~` lines stays one block, and vice versa. Targets
are skipped (not checked) when they have a URL scheme (`https:`, `mailto:`,
anything `scheme:`), are protocol-relative (`//…`), or are fragment-only
(`#anchor`). A `#fragment` suffix on a relative target is stripped before
resolution. Percent-escapes are decoded. A target starting with `/` resolves
against `--dir` (repo-root-relative, the GitHub convention); everything else
resolves against the containing file's directory. Existence is checked with
`os.Stat`, which **follows symlinks**: a target that is a symlink counts as
resolving exactly when the symlink does. Links asserts navigability, not
provenance — that stricter posture belongs to `attest`.

**Says NO when** (each a `LINKS FAIL file:line: target (reason)` line, exit 1):

- a relative target does not exist on disk
- a relative target resolves *outside* `--dir` — it may exist on this machine,
  but it cannot survive the repo travelling alone, so it is broken here

**Refuses (exit 2) when** `--dir` is missing or not a directory.

**Deliberately does not check:** reference-style links (`[a][ref]`), autolinks
(`<https://…>`), raw HTML (`<a href>`), whether a `#fragment` names a real
heading, whether external URLs are alive (network is out of scope for a boot
check), indented (non-fenced) code blocks — a fake link in one is a known
false positive, fence your examples. The scanner also deliberately does not
handle: backslash-escaped brackets or parentheses (`\]`, `\)`), unescaped
balanced parentheses in a bare destination (write `[a](<x(1).md>)`, not
`[a](x(1).md)`), and links whose text and destination span multiple lines —
keep a link on one line.

---

## kernel — the size budget

```
nova-check kernel --file <file> --max-bytes <n>
```

**Asserts.** The kernel file exists, is non-empty, and is at most `n` bytes.
`n` must be a positive integer supplied by the caller; there is no default
budget. Exactly `n` bytes passes — a budget is a ceiling, not a fence to stop
short of.

**Says NO when** (exit 1, always with the measured number):

- the file is over budget — `KERNEL FAIL <file>: over budget: <measured> bytes, budget <n>, over by <d>`
- the file does not exist — a missing kernel is the worst over-budget
- the file is empty — 0 bytes is under every budget and still not a kernel
- the path is not a regular file (directory, device, or symlink) — the kernel
  is `Lstat`ed, the same posture as `attest`: symlinks are never followed,
  even one that resolves

**Refuses (exit 2) when** `--file` or `--max-bytes` is missing, or the budget
is zero or negative.

**Deliberately does not check:** what the bytes say (a kernel of the right
size can still be the wrong kernel — that is `attest`'s hash and a human's
read); tokens (bytes are substrate-independent and arguable about nothing);
compressibility or density.

---

## nocode — the self/machinery separation, as a check

```
nova-check nocode --dir <dir>
```

**Asserts.** A self repo contains prose, not machinery: no code files and no
executables anywhere under `--dir` (skipping `.git`). Regular files only.

A file is flagged when either:

- its extension (case-insensitive) is one of:
  `.go .py .sh .js .ts .c .cc .cpp .rs .cs .rb .pl .php .java .lua .swift
  .kt .mjs .cjs .jsx .tsx .bash .ps1 .bat .cmd .exe`
- it has any executable bit set (`mode & 0111 != 0`)

Both conditions are reported when both hold.

On Windows there is no executable bit, so the mode half of the check is blind
there; the extension list — which includes `.exe .bat .cmd .ps1` for exactly
that reason — is the whole check on Windows.

**Says NO when** any such file exists — one `NOCODE FAIL <path>: <reason>`
line per file, exit 1. Yes, this includes a markdown file someone `chmod +x`ed:
in a self repo an executable *anything* is a boundary violation worth a look.

**Refuses (exit 2) when** `--dir` is missing or not a directory.

**Deliberately does not check:** shebang lines or file contents (an extension
list is auditable; content sniffing is a heuristic that lies both ways);
languages beyond the listed extensions (extend the list, don't sniff);
code *fences inside markdown* — quoted code is prose about code and exactly
what a self repo should hold; symlinks (not followed, not flagged); the tools
repo itself — this check aims at the self repo, and this repo would rightly
fail it.

---

## What this harness is not

Everything above stops at the record layer. It proves the files were present,
whole, sized, linked, and prose — at the moment the check ran, on the machine
that ran it. It does not and cannot prove that a model read them, understood
them, or is currently acting from them; it cannot detect a hostile input, an
injected instruction, or a compromised reader. Those walls remain doctrine
(see nova's SECURITY.md). This tool exists so that the *record* those walls
stand on is checked by something that can actually say NO.
