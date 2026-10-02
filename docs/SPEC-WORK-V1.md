# nova-work v1, layer 1: the tree (SPEC-WORK-V1)

`nova-work` holds every issue of every repository of an organization in **one tree file**: a
restricted s-expression that `internal/worklang` reads and never evaluates
([SPEC-WORKLANG.md](SPEC-WORKLANG.md) section 1). The tree is the working store the verbs above
query and change; GitHub is the source it is imported from and, while it exists, a mirror. This
page is section 1, the lowest layer: the tree's shape, the import that fills it, and the verify
that proves it holds exactly what the source holds. The verbs that query and change the tree, and
its index, are specified on their own page and stand on this one.

The old nova-work and its modules live in the repository `nova-work-old`, beside this one in the
same organization, for reference only.

## 1.1 The layer

| part | what it is | where |
| --- | --- | --- |
| structure | the tree file: root, repositories, issues with their full contents | section 1.2, `internal/workfile` |
| guarantee | every issue of every repository of the organization, every field of section 1.3, and nothing else | sections 1.3 and 1.6 |
| check | `nova-work verify`: a fresh read of the source compared with the tree field for field; zero differences | section 1.6 |
| model | the states of an issue under import and of a repository's sync | `tla/WorkImport.tla`, section 1.8 |

The layer is secured when the import of the whole organization verifies with zero differences,
the model's cases hold on a bench (the design passes, each reversed witness fails with its
invariant), and the unit tests below are green.

## 1.2 The tree's shape

```lisp
(work-tree "v1"
 :source "github"
 :org "<org>"
 :fetched "2026-01-01T00:00:00Z"
 :repos
 ((repo "<org>/<repo>"
   :url "https://github.com/<org>/<repo>"
   :archived false
   :issues
   ((issue 7
      :url "https://github.com/<org>/<repo>/issues/7"
      :node-id "I_..."
      :title "..."
      :state :closed
      :state-reason :completed
      :origin :external
      :author "<login>"
      :author-association :none
      :created "..." :updated "..." :closed "..."
      :locked false
      :lock-reason ()
      :labels ("bug")
      :assignees ()
      :milestone (:number 1 :title "...")
      :body "..."
      :comments ((comment "IC_..." :url "..." :author "<login>" :author-association :member
                   :created "..." :updated "..." :body "..."))
      :references ((ref :kind "PullRequest" :repo "<org>/<repo>" :number 9 :url "..."
                     :actor "<login>" :at "..." :will-close true))
      :linked-prs ((pr :repo "<org>/<repo>" :number 9 :url "..." :state :merged)))))))
```

The file is **canonical**: repositories sorted by name, issues by number, labels and assignees
sorted, comments in creation order, references in timeline order, and every key of every record
written every time in the order above. One tree has exactly one file, so the file's SHA-256 names
the tree.

- A string is written with a backslash before every `"` and `\`, every other byte as it is; the
  reader takes the byte after a backslash literally, so every body survives byte for byte.
- A GitHub enumeration (`OPEN`, `NOT_PLANNED`, `MEMBER`) is a keyword (`:open`, `:not-planned`,
  `:member`): lower case, `_` as `-`. Only `[A-Z_]` is accepted, so the mapping inverts exactly;
  the writer refuses any other value rather than write it lossily.
- GitHub's null is `()` for an enumeration or a milestone and `""` for a string (a deleted
  author, an open issue's closed time). A boolean is the symbol `true` or `false`.
- The reader (`workfile.Decode`) refuses a missing, repeated or unknown key, a value of the wrong
  kind, records out of order, a comment id repeated within an issue, and an issue whose `:url`
  is not the one its path gives. A refused file is refused whole.
- `workfile.Limits(maxBytes)` bounds a read: the byte bound binds (depth 16, at most one atom per
  byte). `verify --max-bytes` sets it.

## 1.3 What an issue carries

| key | GitHub field | note |
| --- | --- | --- |
| number, `:url`, `:node-id` | `number`, `url`, `id` | the identity; the path derives from the URL (section 1.5) |
| `:title`, `:body` | `title`, `body` | the Markdown source, byte for byte |
| `:state`, `:state-reason` | `state`, `stateReason` | |
| `:origin` | derived from `authorAssociation` | section 1.4 |
| `:author`, `:author-association` | `author.login`, `authorAssociation` | |
| `:created`, `:updated`, `:closed` | `createdAt`, `updatedAt`, `closedAt` | RFC 3339 as GitHub gives them |
| `:locked`, `:lock-reason` | `locked`, `activeLockReason` | |
| `:labels`, `:assignees` | `labels.name`, `assignees.login` | sets, written sorted |
| `:milestone` | `milestone.number`, `milestone.title` | |
| `:comments` | every `IssueComment`: `id`, `url`, `author`, `authorAssociation`, `createdAt`, `updatedAt`, `body` | all of them |
| `:references` | every `CrossReferencedEvent` to the issue: the source's kind, repository, number and URL, the actor, the time, `willCloseTarget` | the edges into the issue; a source this login cannot see is kept as `:kind "" :repo "" :number 0 :url ""` |
| `:linked-prs` | every `closedByPullRequestsReferences` (closed ones included): repository, number, URL, state | the pull requests that close it |

**Nothing is cut at a bound.** A connection longer than one page (comments, references, linked
pull requests) is read to its end with follow-up queries; a label or assignee list longer than
one page is refused, since GitHub caps both below it. Each connection's count is checked against
GitHub's `totalCount`, and a repository's issue count against the listing's, so a repository that
changed while it was read is refused ("run again"), never half-captured.

**Not captured** (decision 3): reactions, the edit history of a body or comment, timeline events
other than cross-references (labelled, assigned, closed, renamed), project items, issue types,
sub-issues and pins. Pull requests are not issues and are not in the tree; they appear only as
the sources of references and as linked pull requests.

## 1.4 Origin

An issue filed by the organization's owner, a member or a collaborator (`authorAssociation`
`OWNER`, `MEMBER`, `COLLABORATOR`) is **internal**; every other issue is **external**
(`workfile.OriginOf`). The destructive mode closes internal issues only; an external issue stays
open on GitHub, where its filer sees it, until its fix closes it (decision 4).

## 1.5 Paths and URLs, both directions

An issue's path is `repos/<owner>/<repo>/issues/<n>` and its URL
`https://github.com/<owner>/<repo>/issues/<n>`. Each derives from the other
(`workfile.PathOfURL`, `workfile.URLOfPath`), so both directions are lookups and nothing is written
to the source to link them. Below an issue, `/comments/<id>`, `/references` and `/linked-prs` name
its parts in verify's lines. Walking up is the path's prefix: an issue's repository is
`repos/<owner>/<repo>`, its root the tree.

## 1.6 The verbs of layer 1

```text
nova-work import --org <org> (--out <tree.lisp> | --dry-run) [--repo <owner/name>]... [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>]
nova-work verify --tree <tree.lisp> [--repo <owner/name>]... [--max <n>] [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>] [--max-bytes <n>]
```

**import** is non-destructive: it only reads. It lists the organization's repositories (or reads
each `--repo`), prints `PLAN OK` with the calls the run needs, refuses at exit 2 when that passes
`--max-calls`, reads every issue of every repository in scope, and prints `REPO OK` per
repository. Before anything is written it encodes the tree, reads the bytes back through the
strict reader and compares the result with what was fetched: any difference is `IMPORT FAIL` at
exit 1 and nothing is written. The file is written through a temporary file and a rename. The
last line is `IMPORT OK` with the counts, the bytes, the file's SHA-256, `calls=` (GraphQL calls),
`points=` (what GitHub charged for them) and `rest=0`. `--dry-run` does all of it and writes
nothing.

**verify** reads the tree, reads the same repositories from GitHub again, and compares them with
`workfile.Diff`. Every difference is one line on stdout:

```text
MISSING path=<path> field=<field> want=<value>
EXTRA path=<path> field=<field> got=<value>
DRIFT path=<path> field=<field> want=<value> got=<value>
```

MISSING is on GitHub and not in the tree (a repository, an issue, a comment, a reference, a linked
pull request); EXTRA is in the tree and not on GitHub; DRIFT is a field whose value differs.
Lists are compared in order: comments by id, occurrence by occurrence (a repeated id is its own
EXTRA or MISSING), references and linked pull requests record by record, and a list whose order
differs is DRIFT on `comments-order`, `references-order` or `linked-prs-order`; labels and
assignees are compared as the sequences the file holds. Want
is GitHub's value, got the tree's; a value over 80 bytes or of more than one line is shown as its
length and the head of its SHA-256, so a line never carries a body. With no `--repo` the scope is
the tree's organization, both ways: every repository GitHub lists and every repository the tree
holds. `--max` bounds the lines shown (default 20, 0 all) and never the count. Zero differences
prints `VERIFY OK ... differences=0` with the tree's SHA-256: that line is the receipt the
destructive mode requires. An issue edited on GitHub after the import is DRIFT on `updated` and
the fields that changed; that is the check working, and the remedy is to import again.

**Exits**, as every nova tool's: 0 done, or no difference; 1 verify found differences, or
import's own round trip did; 2 could not run (a missing flag, an unreadable or refused tree, a
refused budget, GitHub unreachable or refusing). A refusal names what is wrong and ends
`run: nova-work <verb> -h`.

**GitHub.** Both verbs read through `internal/workgh`: GraphQL documents run by `gh api graphql`,
the program found on PATH or named with `--gh` and echoed as `GH OK path=`. The seam refuses a
document that is not a plain query, so neither verb can write to GitHub. Every call is counted
against `--max-calls` (default 1500). A page GitHub fails to answer is asked again at half the
size, down to 5 issues. GraphQL is used because one call returns up to 100 issues with their
first 100 comments, references and linked pull requests; the REST quota is not touched.

## 1.7 The modes above layer 1 (specified, not built)

- **dry-run** is `import --dry-run`.
- **non-destructive** is `import`.
- **destructive** closes, after the import, every internal issue that is open on GitHub. It
  refuses without a verify receipt over the repository, and each close is conditional on the
  issue being unchanged since that receipt (its `updated` time is the receipt's); an issue edited
  since is left open and reported. External issues are never closed. GitHub cannot delete an
  issue, so close is the strongest act.
- **export** pushes the tree back: it re-opens every issue the destructive mode closed. The round
  trip, import then destructive then export then verify against a fresh fetch, with zero
  differences other than the `updated` and `closed` times the close and re-open themselves write,
  is the proof that the import loses nothing; it runs on a test repository first.

## 1.8 The model

`tla/WorkImport.tla` holds an issue's import states (absent, fetched, in the tree, mirrored,
closed by the destructive mode, re-opened by export) and a repository's sync (idle, importing,
imported, verified, closing, exporting), with an outside edit possible at any time. Contents are a
version number; equal versions stand for verify's field-for-field equality. Its invariants:
`TreeIsWhole` (a tree is written only with every issue in it), `DestructiveOnlyWithReceipt`,
`Lossless` (an issue the import closed is held by the tree exactly as GitHub held it when it was
closed), `ExternalStaysOpen`, `ExportRestores` (the round trip) and `ReceiptIsTheTree`; the
liveness `ImportEnds`. Five reversed witnesses, one guard removed each, fail with the invariant
their case names in `tla/CASES.tsv` (group `workimport`).

## 1.9 Measured

The whole of an organization of 96 repositories, one run each way from a working machine:

| | import | verify |
| --- | --- | --- |
| repositories, issues | 96, 3678 | 96, 3678 |
| comments, references, linked pull requests | 6125, 10881, 389 | |
| tree file | 24,856,020 bytes | |
| GraphQL calls, points; REST calls | 174, 494; 0 | 174, 494; 0 |
| seconds | 353 | 325 |
| result | written after its round trip | zero differences |

## 1.10 Tests

1. `TestEncodeDecodeIsTheIdentity` (`internal/workfile`): the recorded fixture's tree, encoded and
   read back, equals itself field for field, and encoding it again gives the same bytes.
2. `TestTheReaderRefusesWhatTheWriterWouldNotWrite`: an unknown key, a repeated key, a missing
   key, a URL its path does not give, issues out of order, and an evaluating token are refused.
3. `TestEncodeRefusesALossyValue`: an enumeration outside `[A-Z_]` is refused.
4. `TestDiffNamesEveryKind`: a removed issue is MISSING, an added one EXTRA, a changed body,
   label, comment and reference each DRIFT or MISSING/EXTRA at its path.
5. `TestDiffSeesRepeatsAndOrder`: a comment injected under an existing id, a duplicated comment,
   swapped comments, references and linked pull requests, and labels that join to the same text
   are each found.
6. `TestPathAndURLAreOneLookupEachWay`.
7. `TestFetchReadsTheRecordedRepository` (`internal/workgh`): the recorded public-repository
   fixture (two pages) reads into 20 issues with every connection whole, in three calls.
8. `TestFetchFollowsALongConnection`: a comment list longer than a page is read to its end.
9. `TestFetchRefusesACountThatDisagrees`, `TestTheBudgetRefusesTheCallPastIt` and
   `TestAFailedPageIsAskedAgainSmaller`.
10. `TestANullSourceIsKeptAsNoSource`: a cross-reference with a source this login cannot see is
    kept as a reference with no source and survives the file.
11. `TestRefuseMutation`: a mutation or subscription document is refused before anything runs.
12. `TestImportThenVerifyIsZeroDifferences` (`cmd/nova-work`): import writes the tree from the
    fixture and verify against the same fixture prints `VERIFY OK ... differences=0`; after one
    changed field verify prints its DRIFT line and exits 1.
13. `TestTheBudgetIsCheckedBeforeTheIssuesAreRead`: a plan past `--max-calls` exits 2 after the
    listing, before any issue is read.
14. `TestRefusalsNameTheFlag`: missing flags, a bad page size and a zero budget exit 2 naming
    each; `help` and `<verb> -h` exit 0.

## 1.11 Open decisions

1. **Exit codes.** Verify exits 1 on differences, the house convention (0 done, 1 ran and said
   no, 2 could not run). Recommendation: keep it.
2. **The GitHub seam.** Import and verify run GraphQL through `gh`, outside any REST client,
   which counts into a store. Recommendation: keep GraphQL (a whole organization is 174 calls
   against thousands over REST, and none from the REST quota) and teach a client a counted
   GraphQL call when a store is wanted here.
3. **What is not captured** (section 1.3). Recommendation: add reactions and the other timeline
   events only when a verb above needs them; they cannot be written back as their authors anyway.
4. **Origin.** Today it is derived from the association on every import, so a later change of
   association shows as DRIFT. Recommendation: owner, member and collaborator are internal, as
   section 1.4, and an issue keeps the origin of its first import once imports merge into an
   existing tree (a verb above this layer).
5. **One file or one per repository.** One file today (24.9 MB for 96 repositories). Recommendation:
   keep one file, as ruled, and let the index keep reads from parsing it per call.
6. **Where the file lives.** The tree is saved to a private data repository the adopter names;
   pushing it there is a verb above this layer. Recommendation: `import --out` into a checkout of
   that repository, committed by the caller, until a save verb exists.
7. **Private repositories.** The tree holds private repositories' issues. Recommendation: the data
   repository is private, and a tree is never attached to anything public.
