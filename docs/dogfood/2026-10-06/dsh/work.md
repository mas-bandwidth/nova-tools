# nova-work dogfood — Johnny Grok, 2026-10-06

Reviewer: Johnny Grok. Read as a stranger: `nova-work -h`, `nova-work help`,
`nova-work <verb> -h`, and the nova-work page under `docs/` (`docs/CLI.md`
section nova-work, `docs/SPEC-WORK-V1.md`, which that section points at, and
the nova-work first run in `docs/TESTS.md`). Binary on PATH:
`nova-work v1.2.0-dev.d165b531 linux/amd64 go1.27.1` (`/home/nova/.local/bin/nova-work`).
`cmd/nova-work`, `internal/workfile`, `internal/workgh` and `internal/worklang`
do not differ from that commit through the mechanical tip `6ec8bb02`. Scratch
directory on hetzner, `~/rowan-working/friends/johnny/jobs/dogfood-dsh-work-b.w1~15/scratch`.
`gh` is not logged in (`gh auth status` says so). No server was started, and
nothing was pointed at Redis or at `100.76.29.55`.

`a.lisp` is the minimal tree `verify -h` prints, copied with the indentation
it shows. `b.lisp` is that file with `:archived false` changed to
`:archived true`. `org.lisp` changes only `:org "acme"` to `:org "other"`.
`src.lisp` changes only `:source "github"` to `:source "gitlab"`.
`fetched.lisp` changes only `:fetched` to `1999-01-01T00:00:00Z`.

## Findings

1. `nova-work verify --tree a.lisp --against b.lisp --repo acme/other`
   Printed (stdout, exit 0):
   ```
   VERIFY OK tree=a.lisp sha256=67b8ff218612dc51f9aa3a414c024505467ec2cea513abb1b44ff681696278cc against=b.lisp against_sha256=f0ce2b0d9bca7f332f30cf5626bde7739b2bfb24b5d3e8e3ec60aca241b78ac7 repos=0 issues=0 comments=0 seconds=0.0 differences=0 missing=0 extra=0 drift=0
   ```
   Neither file contains `acme/other`. They do differ: the same command without
   `--repo` exits 1 with `VERIFY DRIFT ... field=archived`. I expected a
   refusal that the named repository is in neither file, or a MISSING line, not
   a green receipt. `differences=0` is the receipt `verify -h` and
   SPEC-WORK-V1 section 1.6 tell you to trust. When the name is in exactly one
   of the two files, the same flag does report it (`VERIFY MISSING` or
   `VERIFY EXTRA`, exit 1). The hole is a name in neither.
   Grade: URGENT.

2. `nova-work verify --tree a.lisp --against org.lisp`
   Printed (stdout, exit 0):
   ```
   VERIFY OK tree=a.lisp sha256=67b8ff218612dc51f9aa3a414c024505467ec2cea513abb1b44ff681696278cc against=org.lisp against_sha256=673f695001172c77565c774f762c8c140d8965f4cadcdde53e9031f457ba12c7 repos=1 issues=0 comments=0 seconds=0.0 differences=0 missing=0 extra=0 drift=0
   ```
   The same command against `src.lisp` and against `fetched.lisp` also printed
   `VERIFY OK ... differences=0` at exit 0, each with a different
   `against_sha256`. I expected a `VERIFY DRIFT` on `:org` and on `:source`.
   SPEC-WORK-V1 section 1.2 puts `:source`, `:org` and `:fetched` on the tree,
   and section 1.6 says every difference is one line. A different organization
   and a different source still produce the receipt. `:fetched` came back the
   same way; if a fetch time is meant to be exempt, the page does not say so.
   Grade: URGENT.

3. `nova-work import --org acme --repo acme/widgets --out ./tree.lisp --timeout 15s --page-size 15`
   `./tree.lisp` already held a 12-byte placeholder. Printed (stderr, exit 2):
   ```
   IMPORT REFUSED: --out ./tree.lisp exists; pass --replace to replace it: nova-work import --org acme --out ./tree.lisp --replace; run: nova-work help
   ```
   I expected the suggested command to keep `--repo acme/widgets` (and the
   timeout and page size). I ran the command it printed,
   `nova-work import --org acme --out ./tree.lisp --replace`. It did not
   refuse the missing `--repo`. It called gh (`calls=1`) and then refused
   because gh is not logged in. `nova-work import --org acme --dry-run --timeout 15s`
   does refuse that shape, in one line:
   `IMPORT REFUSED: --dry-run with no --repo reads every repository of --org acme, up to --max-calls 1500 calls; name one repository and run: nova-work import --org acme --repo acme/<name> --dry-run; run: nova-work help`.
   The write path's own remedy is the command the dry-run path refuses, and
   `--replace` is on it. The placeholder was still 12 bytes, so nothing was
   written on this machine. On a logged-in machine that pasted command would
   replace a one-repository file with every repository of the organization.
   Grade: URGENT.

4. `nova-work import --org acme --repo acme/widgets --page-size 15 --dry-run --timeout 15s`
   Printed (stderr, exit 2):
   ```
   IMPORT REFUSED org=acme calls=1 points=0 gh=/usr/local/bin/gh: /usr/local/bin/gh api graphql: exit status 4: To get started with GitHub CLI, please run:  gh auth login\x0aAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.; run: /usr/local/bin/gh auth status
   ```
   `nova-work verify --tree a.lisp --repo acme/widgets --page-size 15 --timeout 15s --max-calls 10`
   prints the same `\x0a` inside its `VERIFY REFUSED` line. `--json` on the
   import puts a real newline in `why`. I expected one line whose words stay
   words, the line break rendered as a space, and the remedy. The remedy
   (`gh auth status`) is the right next step, and this machine is not logged
   in, so the refusal itself is fair. The hex escape is the defect.
   Grade: NEXT.

5. `nova-work verify --tree a.lisp --against b.lisp`
   Printed (stderr, exit 1):
   ```
   VERIFY FAILED tree=a.lisp sha256=67b8ff218612dc51f9aa3a414c024505467ec2cea513abb1b44ff681696278cc against=b.lisp against_sha256=f0ce2b0d9bca7f332f30cf5626bde7739b2bfb24b5d3e8e3ec60aca241b78ac7 repos=1 issues=0 comments=0 seconds=0.0 differences=1 missing=0 extra=0 drift=1
   VERIFY DRIFT path=repos/acme/widgets field=archived want="true" got="false"
   ```
   That is what `verify -h` promises (one DRIFT on `field=archived`, under
   `VERIFY FAILED`, exit 1), and `docs/TESTS.md` also says `VERIFY FAILED`.
   `docs/CLI.md` and SPEC-WORK-V1 section 1.6 name the status `VERIFY FAIL`.
   I expected the page and the tool to use one word. The help does not lie;
   the page does.
   Grade: NEXT.

6. `nova-work import --org '' --dry-run --timeout 15s`
   Printed (stderr, exit 2):
   ```
   IMPORT REFUSED: --org is required; it wants the organization whose repositories are read, as GitHub spells it; refusing to guess; run: nova-work help
   IMPORT REFUSED: --dry-run with no --repo reads every repository of --org , up to --max-calls 1500 calls; name one repository and run: nova-work import --org '' --repo ''/<name> --dry-run; run: nova-work help
   ```
   The first line is the refusal I expected. The second suggests a command
   that still has an empty `--org`, a `--repo` of `''/<name>`, and the
   placeholder `<name>`. The same placeholder is in the acme form in finding 3.
   I expected the second problem not to be reported once `--org` is empty, or
   a remedy I can paste without inventing `<name>`.
   Grade: NEXT.

## What the tool got right

- The minimal tree from `verify -h`, compared with itself, prints `VERIFY OK ... differences=0` and exits 0. The archived edit is the one DRIFT line the help describes.
- A hand-written issue with every key `verify -h` lists compares equal to itself. A body longer than 80 bytes is shown as `bytes:` and `sha256:`, not the body. `--max 1` on two drifts prints `VERIFY MORE`, and `--json` carries the same items and the same counts.
- `--against` together with `--gh`, `--page-size`, `--timeout` or `--max-calls` is refused before a read, and the line says to drop one of them.
- A missing `--org` and a missing `--out` are named together. `--max-calls 0` and a `--page-size` outside 1 to 100 are refused before gh. An unknown flag names that verb's flags. An unknown key names the key.
- No import wrote a tree. gh is not logged in, which the page says is required for a GitHub run, and the dry run did not create `./tree.lisp`.

READ 7/10 — `nova-work -h`, `verify -h` and SPEC-WORK-V1 section 1.2 were enough to copy a tree and get the archived DRIFT the help promises, but CLI.md and SPEC-WORK-V1 call that status `VERIFY FAIL` while the tool prints `VERIFY FAILED`, and the page says every difference is a line while `:org`, `:source` and `:fetched` are silent.

USE 6/10 — offline compare, `--json`, `--max`, a bad flag, a bad tree and a missing login all answered in one line with a next command, but `--repo` of a repository neither file holds still exits 0 with `differences=0`, and the remedy for an existing `--out` drops `--repo` and starts an organization-wide read.

urgent=3 next=3
