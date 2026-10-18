# nova-roadmap: ROADMAP.md from data

`nova-roadmap` renders a repository's `ROADMAP.md` from its `docs/roadmap.sexp` and checks the
committed page against the data. It is the one roadmap generator: every repository runs
`nova-roadmap render` from `make roadmap` and `nova-roadmap check` from `make roadmap-check`
and its CI, using the binary from a pinned nova-tools release, so a repository in any language
needs nothing else. The tool is `cmd/nova-roadmap` over `pkg/roadmap`, which reads the data
through `pkg/sexp` and never evaluates it. It depends on nothing of the sprint.

## The rules

1. The page is generated: the same data renders the same bytes, and nobody edits the page.
2. `check` writes nothing. It exits 0 when the page on disk is exactly what the data renders,
   1 on any difference (a missing page is a difference), and 2 when the data is refused.
3. The data is refused whole, every problem named, never rendered in part.
4. A cell is ✅ only when every task under it is `:done`, and a `:done` task carries non-blank
   `:evidence`: the test or the commit that proves it. Evidence lives in the data, never in
   the table.

## The feature-table form

The standard form of every `ROADMAP.md` (owner, 2026-10-17): one `##` section per area, a line
saying what the area is, then a table with one row per feature a user would recognise, ✅/❌
cells, a `complete` row (per column, the floor of the share of ✅ rows) and a link to the
source data. An area has one `done` column, or declares an axis (languages, platforms,
drivers, modes) as its columns. `nova-roadmap help form` prints the shape with a worked example,
and `nova-roadmap example` writes it as a starter file.

- `:root` names the node every area is reached from; `:page` holds the title, the areas in page
  order (`:roadmaps`) and optional closing sections.
- Nodes are `:task` (a state of `:unknown`, `:todo`, `:doing` or `:done`, with `:evidence`),
  `:work-set` (children whose tasks a cell needs all of) and `:roadmap` (an area: `:rows`,
  `:columns` and one `:cells` entry per row and column). A row with `:later t` renders
  `(later)` and still counts in the complete row.
- Nodes may carry other keys (notes, card ids, pull requests); they are kept and not rendered.
- References resolve, the graph has no cycle, every area has every cell, and a title holding
  `|` or a newline is refused, since it would break the table.

## The evidence overlay

`docs/roadmap-evidence/*.sexp` beside the data (`--evidence <dir>` names another), when it
exists, holds any number of `(:id "<task>" :state :done :evidence (...))` forms. The files are
applied in name order (a dot file is skipped), each whole or not at all, and each entry is held
to rule 4. The page's notice names the overlay when there is one.

## The older forms

The card list, `(roadmap "v1" :title ... :groups (...) :items (...))`, and `docs/fixes.sexp`
(`--kind fixes`) render as they did, until each repository converts; `pkg/roadmap`'s package
documentation holds their shapes.

## The verbs

| verb | effect | exit |
| --- | --- | --- |
| `example --out <file>` | writes a starter feature-form file, never over one that is there | 0, 2 |
| `render [--file] [--out] [--evidence] [--kind]` | writes the page; `--dry-run` writes nothing | 0, 2 |
| `check [--file] [--out] [--evidence] [--kind]` | compares the page with the data | 0, 1, 2 |

`--file` defaults to `docs/roadmap.sexp` (`docs/fixes.sexp` with `--kind fixes`) and `--out` to
`ROADMAP.md` (`FIXES.md`). The tests are `cmd/nova-roadmap` (the drift gate, the refusals, the
first run in [TESTS.md](TESTS.md#nova-roadmap)) and `pkg/roadmap` (the golden page, every shape
refusal, the overlay).
