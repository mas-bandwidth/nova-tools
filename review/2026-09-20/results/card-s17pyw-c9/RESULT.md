RESULT: s17pyw-c9 sha=7c29513b0e8f

**BLOCKED** — base-repo at `/tmp/schema14-ftf.bundle` is inaccessible from this sandboxed session.

## Environment diagnosis

The harness grants sandbox read/write access to these paths only:

- `--read  /Users/glenn/rowan-working/tmp/a91ae186-71f9-73e7-ad99-9fe4080077f6-card-s17pyw-c9`
- `--write /Users/glenn/rowan-working/tmp/a91ae186-71f9-73e7-ad99-9fe4080077f6-card-s17pyw-c9/jobs/card-s17pyw-c9`

The base-repo bundle lives at `/tmp/schema14-ftf.bundle`, **outside** all granted paths. Every access attempt fails with `PermissionError: [Errno 1] Operation not permitted`:

```bash
$ cat /tmp/schema14-ftf.bundle | head -c 20
cat: /tmp/schema14-ftf.bundle: Operation not permitted
```

```python
>>> os.stat('/tmp/schema14-ftf.bundle')
PermissionError: [Errno 1] Operation not permitted
```

```bash
$ git fetch /tmp/schema14-ftf.bundle fixed-table-form
fatal: '/tmp/schema14-ftf.bundle' does not appear to be a git repository
```

The working tree repo at `jobs/card-s17pyw-c9/` is completely empty (zero commits, no tracked files beyond session artifacts). The expected schema layout (`docs/SPEC.md`, `docs/FIXED-FORM-ALGORITHM.md`, `bench/paired/corpus/`, C++ reference sources, nine table backend implementations) is nowhere in the accessible filesystem.

Search of every reachable path under the root grant returned zero matches for `*.bundle`, `*schema*`, `*fixed*form*`, or any non-opencode/non-npm content.

## What this blocks

- **STEP 2** — Cannot read `docs/SPEC.md` or `docs/FIXED-FORM-ALGORITHM.md` to quote the row C9 rule.
- **STEP 3** — Cannot grep/read C++ oracle source or paired corpus to identify row C9's behaviour and its line range.
- **STEP 5–6** — Without knowing what row C9 specifies, no test can be written RED, and no python implementation can follow GREEN. No commit is possible without schema context.

## Toolchain status (not the blocker)

- `python3` present: Python 3.14.7 (homebrew) — available and usable.
- `pytest` absent; network unavailable (`SSLCertVerificationError`). Using `python3 <case.py>` directly when test scaffolding lands. This alone is acceptable per the card rules.
