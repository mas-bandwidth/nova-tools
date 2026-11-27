# nova-roadmap

Keep a project's roadmap as data, and let the tables in its `ROADMAP.md` be
written from that data. You list the work in `docs/roadmap.sexp`: the areas of
the project, the features in each, and the tasks behind every feature.
`nova-roadmap render` turns each area into a table of ✅ and ❌ cells, and
`nova-roadmap check` tells CI when a table no longer matches the data. A cell is
✅ only when every task under it is done and names the test or commit that
proves it, so the page cannot claim more than the repository shows.

Only the tables are generated. The title, the headings and every paragraph of
`ROADMAP.md` are yours, and nova-roadmap keeps them byte for byte.

## Install and try

[Install one tool](../../docs/README.md#install-one-tool), choosing `roadmap`.
It is one binary with no service, network access or runtime dependency, so a
repository in any language can use it. `nova-roadmap help` prints the manual
and changes nothing; `nova-roadmap help form` prints the data's shape with a
worked example.

The blocks below run in order, in one shell, from a fresh directory:

```sh
cd "$(mktemp -d)"
```

## Start a roadmap

`example` writes a starter data file, and `render` writes the page from it.
When there is no `ROADMAP.md` yet, `render` writes a starter page: a title, a
comment saying which parts are generated, and a heading with a table for each
area. `check` then confirms the page matches the data.

```sh
nova-roadmap example
nova-roadmap render
nova-roadmap check
```

```text
OK: wrote docs/roadmap.sexp
next: nova-roadmap render --file docs/roadmap.sexp
OK: wrote a starter ROADMAP.md; everything outside the nova-work markers is yours to edit (2 areas, 4 rows; 0 evidence files, 0 entries)
OK: the tables in ROADMAP.md are up to date with docs/roadmap.sexp (2 areas, 4 rows; 0 evidence files, 0 entries)
```

The page it writes:

```markdown
# Roadmap

<!-- The tables between the nova-work markers are generated from docs/roadmap.sexp by nova-roadmap: edit the data and run `nova-roadmap render`. Everything else on this page is written by hand. -->

## Protocol

<!-- nova-work:protocol:start -->

| feature | done |
|---|:---:|
| Connect tokens | ✅ |
| IPv6 (later) | ❌ |
| complete | 50% |

[Source data](docs/roadmap.sexp)

<!-- nova-work:protocol:end -->

## Clients

<!-- nova-work:clients:start -->

| feature | Go | C# |
|---|:---:|:---:|
| Connect | ✅ | ❌ |
| Send and receive | ❌ | ❌ |
| complete | 50% | 0% |

[Source data](docs/roadmap.sexp)

<!-- nova-work:clients:end -->
```

Now edit the title, the headings and the prose by hand. From here on, `render`
changes only what sits between the markers.

## The data

`docs/roadmap.sexp` is one list of `:key value` pairs. It is read as data and
never run: there is no evaluation, and the reader refuses anything that would
make the file a program. This is the file `example` wrote:

```lisp
; docs/roadmap.sexp: the data the tables in ROADMAP.md are generated from (nova-roadmap render).
(:schema 1
 :root "goal"
 :page (:roadmaps ("protocol" "clients"))
 :nodes
 ((:id "goal" :type :work-set :children ("protocol" "clients"))
  (:id "protocol" :type :roadmap :title "Protocol"
   :rows (("connect" "Connect tokens") ("ipv6" "IPv6" :later t))
   :columns (("done" "done"))
   :cells (("connect" "done" "t-connect") ("ipv6" "done" "t-ipv6")))
  (:id "t-connect" :type :task :title "Connect tokens" :state :done
   :evidence ("token_test.go: TestConnectToken" "commit 1a2b3c4"))
  (:id "t-ipv6" :type :task :title "IPv6" :state :todo)
  (:id "clients" :type :roadmap :title "Clients"
   :rows (("connect" "Connect") ("send" "Send and receive"))
   :columns (("go" "Go") ("cs" "C#"))
   :cells (("connect" "go" "go-connect") ("connect" "cs" "cs-connect")
           ("send" "go" "go-send") ("send" "cs" "cs-send")))
  (:id "go-connect" :type :task :title "Go client connects" :state :done :evidence ("client_test.go: TestConnect"))
  (:id "cs-connect" :type :task :title "C# client connects" :state :doing)
  (:id "go-send" :type :work-set :children ("go-send-reliable" "go-send-unreliable"))
  (:id "go-send-reliable" :type :task :title "Go reliable send" :state :done :evidence ("send_test.go: TestReliable"))
  (:id "go-send-unreliable" :type :task :title "Go unreliable send" :state :todo)
  (:id "cs-send" :type :task :title "C# send and receive" :state :unknown)))
```

The top level has four keys:

| Key | What it holds |
|---|---|
| `:schema` | `1`, the version of this format. |
| `:root` | The id of the node every area is reached from, usually a work-set holding the areas. |
| `:page` | `(:roadmaps ("<area id>" ...))`: the areas the page shows, in page order. It holds nothing else. |
| `:nodes` | Every node: areas, tasks and work-sets. Each has an `:id` and a `:type`. |

### Areas, rows and columns

An **area** is a node with `:type :roadmap`, and it is one table on the page.

- `:title` is the area's heading on a starter page.
- `:rows` lists the features, `(("<row id>" "<feature>") ...)`. The feature is
  the text in the first column. A row that ends with `:later t` renders
  `(later)` after its feature, for work that is planned but not next.
- `:columns` lists the columns, `(("<column id>" "<column title>") ...)`. Use
  one column, `("done" "done")`, for a plain list of features, or one column
  per language, platform, driver or mode to compare them side by side.
- `:cells` connects them, `(("<row id>" "<column id>" "<node id>") ...)`: one
  entry for every row and column, naming the task or work-set behind that cell.

Under each table, a `complete` row gives each column's share of ✅ cells,
rounded down; a `(later)` row counts toward it. A `[Source data]` link points
from the page to the data file.

### Tasks and their states

A **task** is a node with `:type :task`, a `:title` and a `:state`:

| State | Meaning | Cell |
|---|---|---|
| `:unknown` | Nobody has looked yet. | ❌ |
| `:todo` | Known work, not started. | ❌ |
| `:doing` | In progress. | ❌ |
| `:done` | Finished, and its `:evidence` names what proves it. | ✅ |

The three unfinished states all render ❌; the difference is for the people
reading the data. A `:done` task must carry `:evidence`, a list of non-blank
strings such as `"token_test.go: TestConnectToken"` or `"commit 1a2b3c4"`.
A `:done` task without it is refused, even when no cell reaches it.

### Work-sets

A **work-set** is a node with `:type :work-set` and `:children`, a list of node
ids. Point a cell at a work-set when the feature needs several tasks: the cell
is ✅ only when every task under it is done. Above, Go's "Send and receive" is
❌ because `go-send-unreliable` is still `:todo`, although its reliable half is
done.

### Notes and other keys

A node may carry any other key, such as `:note`, a link or a card id. Those
keys are kept in the data for people and other tools, and are never rendered:

```lisp
(:id "t-ipv6" :type :task :title "IPv6" :state :todo
 :note "Needs a dual-stack runner first.")
```

Prose about the project belongs on the page, not in the data: a `:text` key on
an area, or a `:page` key other than `:roadmaps`, is refused.

### Syntax rules

- Comments run from `;` to the end of the line.
- Strings are in double quotes. A title may not hold `|` or a line break.
- Outside strings, the reader refuses `#`, `'`, `` ` ``, `,` and `\`.
- A file over 5 MiB, nested more than 64 deep, or holding more than 10,000
  nodes is refused.

## Prove a task with evidence

An **evidence overlay** marks tasks done without editing the graph, so a test
run or a release step can write its own file. It is every `*.sexp` file in
`docs/roadmap-evidence/` beside the data (`--evidence <dir>` names another
directory). Each file holds entries one after another, with no list around
them. An entry names a task by `:id` and sets its `:state`, and its
`:evidence` when given; other keys, such as `:note`, are kept and not rendered.
Files apply in name order, and a file whose name starts with `.` is skipped.

Add one, and `check` reports the drift before `render` fixes it:

```sh
mkdir -p docs/roadmap-evidence
printf '%s\n' '(:id "t-ipv6" :state :done :evidence ("ipv6_test.go: TestIPv6"))' > docs/roadmap-evidence/protocol.sexp
nova-roadmap check || echo "exit $?"
nova-roadmap render --dry-run
nova-roadmap render
nova-roadmap check
```

```text
DRIFT: a table in ROADMAP.md differs from what docs/roadmap.sexp renders (areas: protocol); run: nova-roadmap render, then commit ROADMAP.md
exit 1
DRY RUN: would have spliced the tables into ROADMAP.md (2 areas, 4 rows; 1 evidence files, 1 entries); wrote nothing
OK: spliced the tables into ROADMAP.md (2 areas, 4 rows; 1 evidence files, 1 entries)
OK: the tables in ROADMAP.md are up to date with docs/roadmap.sexp (2 areas, 4 rows; 1 evidence files, 1 entries)
```

The IPv6 cell is now ✅ and Protocol's `complete` row reads 100%. Every line
outside the markers is as you left it. One bad entry, such as an id that is not
a task or a `:done` without `:evidence`, refuses the whole run, and nothing is
written.

## The page and its markers

Each table sits between two marker lines, each on a line of its own:

```markdown
<!-- nova-work:<area id>:start -->
<!-- nova-work:<area id>:end -->
```

`render` replaces only the bytes between each pair. The page must hold exactly
one pair per area of `:page :roadmaps`, in that order; place each pair wherever
that table belongs. A page with no markers, or with the wrong ones, is refused,
and the refusal prints the exact lines to paste:

```sh
printf '# My project\n' > OTHER.md
nova-roadmap render --out OTHER.md || echo "exit $?"
```

```text
nova-roadmap: REFUSED: OTHER.md: the page carries marker pairs for []; the data's :page :roadmaps are ["protocol" "clients"]. Each area needs its own pair of marker lines, each on a line of its own, in this order, wherever its table belongs on the page:
  <!-- nova-work:protocol:start -->
  <!-- nova-work:protocol:end -->
  <!-- nova-work:clients:start -->
  <!-- nova-work:clients:end -->
run: nova-roadmap help
exit 2
```

The `[Source data]` link is the path from the page to the data, so a page kept
in `docs/` (`--out docs/ROADMAP.md`) links to `roadmap.sexp`, and the page is the
same wherever the command runs from.

## Keep it current in CI

After a change to the data, run `render` and commit the data and the page
together. In CI, run `check`: it renders the tables in memory, compares them
with the page on disk byte for byte, writes nothing, and exits 1 when they
differ or the page is missing. A repository whose page lives in `docs/` passes
the same flags to both:

```sh
nova-roadmap render --file docs/roadmap.sexp --out docs/ROADMAP.md
nova-roadmap check --file docs/roadmap.sexp --out docs/ROADMAP.md
```

The same data always renders the same bytes, on any machine.

## Reference

| Verb | What it does |
|---|---|
| `example` | Writes a starter data file, making its directory. It never writes over a file that is there. Flags: `--out <path>` (default `docs/roadmap.sexp`), `--dry-run`. |
| `render` | Writes each area's table between its markers, or a starter page when the page does not exist. Flags: `--file <path>` (default `docs/roadmap.sexp`), `--out <path>` (default `ROADMAP.md`), `--evidence <dir>` (default `roadmap-evidence/` beside `--file`, when it exists), `--dry-run`. |
| `check` | Compares the page with what `render` would write, and writes nothing. Flags: `--file`, `--out` and `--evidence`, as for `render`. |
| `help` | Prints the manual; `help <verb>` prints one verb, and `help form` the data's shape. `<verb> -h` and `<verb> --help` print the verb's help too. |
| `version` | Prints `nova-roadmap <version>`: the release tag for a release binary. |

Relative paths are read from the working directory. Every result is one line
on stdout, except `DRIFT` and refusals, which go to stderr:

| Line | Meaning |
|---|---|
| `OK: wrote <file>` | `example` wrote the data file; a `next:` line follows. |
| `OK: wrote a starter <page>; ...` | `render` found no page and wrote a starter one. |
| `OK: spliced the tables into <page> (...)` | `render` changed at least one table. |
| `OK: <page> is up to date (...)` | `render` found nothing to change and wrote nothing. |
| `OK: the tables in <page> are up to date with <file> (...)` | `check` found no drift. |
| `DRY RUN: would ...; wrote nothing` | `--dry-run`: what would have been written. |
| `DRIFT: ...; run: nova-roadmap render, then commit <page>` | `check` found a table that differs (naming its areas) or no page. |

The counts in parentheses are areas, rows, evidence files and evidence entries.

Exit codes: 0 when `render` succeeds or `check` finds the page up to date; 1
when `check` finds drift; 2 for a refusal.

## Refusals

A refusal is two lines on stderr: `nova-roadmap: REFUSED: <what is wrong>`,
then `run: nova-roadmap help`. It names the first problem found, writes
nothing, and exits 2. A problem in the data names the file.

| Refusal | Exit | What to do |
|---|---|---|
| `nova-roadmap: REFUSED: <file> is there already; example never writes over data` | 2 | Keep your data, or pass another `--out`. |
| `reading <file>: open <file>: no such file or directory` | 2 | Point `--file` at the data, or run `nova-roadmap example` first. |
| `done task "<id>" has no :evidence; name the test or commit that proves it` | 2 | Add `:evidence` to the task, or set its state back to `:doing`. |
| `task "<id>" has state :<state>; want :unknown, :todo, :doing or :done` | 2 | Use one of the four states. |
| `area "<id>" carries :text; prose lives in ROADMAP.md, written by hand, never in the data` | 2 | Move the text onto the page, outside the markers. |
| `the page carries marker pairs for [...]; ...` | 2 | Paste the marker lines the refusal prints, in order. |
| `"<verb>" is no verb; the verbs are example, render, check, version and help` | 2 | Run `nova-roadmap help` for the verbs. |
