# nova-roadmap

nova-roadmap writes the feature tables in a repository's `ROADMAP.md` from data, and checks them for drift.

The data is `docs/roadmap.sexp`: a graph of tasks, read as data and never evaluated. Each area of the roadmap is one table, with a row per feature, a ✅/❌ cell per column, a complete row and a link to the data. A cell is ✅ only when every task under it is `:done` with `:evidence`, which is a test or a commit that proves it.

In `ROADMAP.md`, each table sits between two marker lines, and nova-roadmap writes only the bytes between them. Everything else on the page is written by hand and kept exactly as it is: the title, the headings and the prose.

```
<!-- nova-work:<area id>:start -->
<!-- nova-work:<area id>:end -->
```

It is a Go port of schema's `tools/roadmap/render.lisp`, and renders schema's tables byte for byte. It is one static binary with no runtime dependencies, so a repository in any language can use it.

## Install

You need Go 1.24 or later. The repository is private for now, so tell Go not to use the public module proxy for it. Your git must also be able to read `github.com/mas-bandwidth`.

```sh
GOPRIVATE=github.com/mas-bandwidth/* go install github.com/mas-bandwidth/nova-roadmap@latest
```

Or build it from a clone:

```sh
git clone https://github.com/mas-bandwidth/nova-roadmap
cd nova-roadmap
go install .
```

The binary lands in `$(go env GOPATH)/bin`. Then run `nova-roadmap help`. The manual is in the tool itself.

## Verbs

| verb | what it does |
|---|---|
| `nova-roadmap example [--out docs/roadmap.sexp]` | writes a starter data file (the worked example below). It never writes over an existing file. |
| `nova-roadmap render [--file docs/roadmap.sexp] [--out ROADMAP.md] [--evidence DIR] [--dry-run]` | renders each table between its markers. When the page does not exist, it writes a starter page with a heading and a marker pair per area. |
| `nova-roadmap check [--file ...] [--out ...] [--evidence DIR]` | the drift gate, for CI. It writes nothing, and exits 1 when a table differs from what the data renders, or when the page is missing. |
| `nova-roadmap help [example \| render \| check \| form]` | the manual. `help form` is the data's shape, with a worked example. |
| `nova-roadmap version` | the version. |

Exit codes:
- 0: done (render), or up to date (check).
- 1: drift (check).
- 2: refused. Either the data or the page is malformed (every problem is named), or the invocation is bad.

## A first run

In a repository's root:

```sh
nova-roadmap example    # writes docs/roadmap.sexp
nova-roadmap render     # writes a starter ROADMAP.md
nova-roadmap check      # OK: the tables in ROADMAP.md are up to date ...
```

Then edit the page's title, headings and prose by hand. When the data changes, run `nova-roadmap render` and commit both. In CI, run `nova-roadmap check`.

A Makefile can carry both:

```make
roadmap:
	nova-roadmap render
roadmap-check:
	nova-roadmap check
```

## The form

`docs/roadmap.sexp` is one `(:key value ...)` list. This example has two areas. The first has one `done` column. The second uses an axis as its columns, here languages; platforms, drivers or modes work the same way.

```lisp
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

The nodes:
- **`:task`** is a piece of work. Its `:state` is `:unknown`, `:todo`, `:doing` or `:done`. A `:done` task must carry `:evidence`.
- **`:work-set`** groups nodes under `:children`. Use one for a cell that needs several tasks: above, Go's "Send and receive" needs both of its sends.
- **`:roadmap`** is an area, which is one table. It has:
  - `:title`, the area's heading on a starter page.
  - `:rows`; a row may end with `:later t`.
  - `:columns`.
  - `:cells`, one cell for every row and column.

`:root` names the node every area is reached from. `:page (:roadmaps (...))` lists the areas in page order.

The rules:
- **✅:** a cell is ✅ only when every task under it is `:done` with non-blank `:evidence`.
- **Complete row:** each column's share of ✅ cells, rounded down.
- **Other keys:** nodes may carry them (notes, links, metadata). They are kept, and not rendered.
- **No prose in the data:** a `:text` on an area, or a `:page` key other than `:roadmaps`, is refused. Prose lives in `ROADMAP.md`.
- **Titles:** no title may hold `|` or a newline.
- **Data, not a program:** the reader refuses `#`, `'`, `` ` ``, `,` and `\` outside strings.

**The evidence overlay** is `docs/roadmap-evidence/*.sexp`; `--evidence` names another directory. It holds one entry per task. Entries are applied in file-name order, and each file is applied whole or not at all. An entry sets a task's `:state`, and its `:evidence` when given. A test or a commit can then prove a task without rewriting the graph:

```lisp
(:id "t-ipv6" :state :done :evidence ("ipv6_test.go: TestIPv6"))
```

## Worked example

`nova-roadmap render` on the data above, with no `ROADMAP.md` yet, writes:

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

"Send and receive" is ❌ for Go because one of its two tasks is still `:todo`.

Next, add the overlay entry above for `t-ipv6`. Then `nova-roadmap check` exits 1. Running `nova-roadmap render` turns the IPv6 cell ✅ and Protocol's complete row to 100%, and leaves every hand-written line alone.

## Development

```sh
go test ./...
go vet ./...
```

The unit tests use no real time, sockets or network. `roadmap/testdata/schema` holds schema's data and page. The golden test renders the page from the data, byte for byte.

## License

MIT. See [LICENSE](LICENSE).
