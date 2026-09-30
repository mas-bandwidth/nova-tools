# The work language (SPEC-WORKLANG)

`internal/worklang` reads the work language: restricted s-expressions a person writes to
describe a set of work as **units**. A work set is data the reader reads, never a program it
evaluates. This page specifies the reader, the `(work-set ...)` form, the keys a unit carries
and the shape each key must have, and the accessors a caller reads a unit through.

The living caller is `nova-tokens fold --units <set.lisp>`. It loads the set with
`worklang.ParseWorkSet(path, raw, worklang.DefaultLimits())` and reads each unit's id, `PR()`,
`Branch()` and `Lane()` to attribute a transcript's spend to a unit;
[SPEC-TOKENS.md](SPEC-TOKENS.md) specifies that attribution rule.

The package also holds a plan form (`ParsePlan`, `Plan.Graph`), a checker
(`WorkSet.Check`, `Ready`, `Blocked`) and an attempt writer (`Record`, `Take`). No living
tool calls them, and this page does not specify them.

## 1. The reader

`worklang.Read(file, data, limits)` reads exactly one form from `data`. The grammar is five
kinds of form and nothing else:

| form | written | held as |
| --- | --- | --- |
| list | `( ... )`, possibly empty | `List`, its members in order |
| keyword | `:name` | `Keyword`, the name without the colon |
| string | `"..."` | `String`, decoded; a backslash takes the next byte literally |
| integer | decimal digits, optionally signed with `+` or `-` | `Integer` |
| symbol | any other bare token, such as `go-fix` or `false` | `Symbol`, verbatim |

A `;` starts a comment that runs to the end of its line. Comments and whitespace are text,
never syntax. A keyword that is empty or holds a second `:` is refused as a forbidden token
at its byte. **Every token that starts with a digit, `+` or `-` goes to the integer reader**, so
it must be an integer: a sign with no digits after it (`-x`, a bare `+`) or digits followed
directly by a non-boundary byte (`12abc`) is refused as a forbidden token at its byte, never read
as a symbol.

**Nothing is evaluated.** Before parsing, a lexical pass refuses every token that would
evaluate or escape, each at its own byte offset: `#` (a dispatch macro such as `#.`), `|`,
`'`, `` ` ``, `,` and `\`. Inside a string or a comment the same bytes are text.

**Three bounds, all enforced while reading.** `Limits` carries `MaxBytes`, `MaxDepth` and
`MaxNodes`; the names are the flags a caller exposes (`--max-bytes`, `--max-depth`,
`--max-nodes`). `DefaultLimits()` is 65536 bytes, depth 64 and 4096 nodes. A file longer than
`MaxBytes` is refused before a byte of it is parsed. A list nested past `MaxDepth` is refused
at its opening byte. Every atom (keyword, string, integer, symbol) counts as one node, and the
atom past `MaxNodes` is refused at its byte. A limits value with any bound at zero or below is
refused rather than guessed. Past a bound, the input is refused whole and never truncated.

**One form per file.** Bytes after the first form, an unbalanced list and an unterminated
string are each refused naming the byte.

**Refusals.** Every refusal is a `*worklang.Refusal` whose `ExitCode()` is 2 and whose message is
`plan file=<file>: <reason>`. The reason names the byte, the bound or the key it refuses.

**Byte ranges.** A form carries `Offset` (its first byte), and a list, string, keyword or
integer also carries `End` (the byte just past it). `Form.Bytes(data)` returns that range out of
the source.

## 2. The work-set form

```lisp
(work-set "<id>" <key> <value> ...
  :units ((unit "<id>" <key> <value> ...)
          ...))
```

`worklang.ParseWorkSet(file, data, limits)` reads one work set through the reader above and
refuses, at exit 2 and naming the file:

- a top form that is not a list whose head is the bare symbol `work-set`;
- a set whose second element is not a non-empty string (the set's id);
- a body element in key position that is not a keyword, and a key with no value after it;
- a `:units` value that is not a list, and a set with no `:units` at all;
- a member of `:units` that is not a list headed by the bare symbol `unit`;
- a unit whose second element is not a non-empty string (the unit's id);
- a unit id another unit of the set already carries;
- any unit key whose value has the wrong shape (section 3).

A refused set is refused whole. `ParseWorkSet` never returns a half-read set.

The unit's **id** is its identity and `:title` is its display text; the two are kept apart.
`:was` names the old id of a renamed unit, and the reader carries it as written.

Keys the reader knows go into `Fields` and every other key goes into `Unknown`, on the set and
on each unit alike. An unknown key is kept and ignored, never refused. A unit also keeps every
key it carried, known or not, in written order (`Unit.Keys()`), and its own byte range
(`Unit.Offset`, `Unit.End`, `Unit.Bytes(data)`).

A form nested inside another key's value (a template inside `:derive`, say) is data of that key
and is not a unit of the set.

## 3. Unit keys

A unit's body is keyword and value pairs. The reader knows these keys:

```text
:needs :blocks :title :inputs :owner :status :evidence :note :lane :deadline :budget
:affinity :derive :replaces :findings :pr :prs :card :cards :spec :sprint :under :kind
:repo :base :output :done-when :units :branch :id :was :resources :writes :tools
:collects :warm :attempts :acceptance :state
```

It checks the shape of the nine keys in the table below, and the agreement of `:lane` with
`:resources`. Every other known key is carried as written. The rule number in the table is the
name the package's code comments and test names use for that rule.

| key | rule | shape the reader requires |
| --- | --- | --- |
| `:attempts` | A3 | a list of records; each is a list with `:n <integer>`, `:rung "<name>"` and `:outcome` from `green red refused abandoned uncertain`; every outcome but `uncertain` also carries `:proof` |
| `:state` | A4 | one of `open ready live blocked uncertain closed refused abandoned`, written as a keyword or a bare symbol |
| `:resources` | A5 | a list of `(:<name> <value>)` entries: `(:lane "<name>")` with a non-empty string, `(:class "<name>" :n <integer>)`, and any other name with an integer count; at most one `:lane` entry |
| `:lane` | A6 | when it is a string and `:resources` also names a lane, the two must be the same lane |
| `:writes` | A7 | a list of non-empty path strings, none absolute (a leading `/`) and none escaping the repo (`..`, a leading `../`, or `/../` inside) |
| `:tools` | A10 | a list of entries, each a list headed by a keyword naming the verb and carrying `:at "<version>"`; `:key "<key>"` is optional |
| `:collects` | A11 | a list of entries, each carrying `:name "<name>"` and `:under "<path>"`; `:members :unknown-before-run` is optional |
| `:warm` | A12 | a list carrying both `:retained (...)` and `:active (...)` |
| `:owner` | A13 | a non-empty string; the reader checks nothing else about it. Its refusal text suggests the conventional forms (a friend `"Emma"`, a child rung `"child:opus"`, a swarm `"swarm:flash"`, `"all"`), which are not enforced |
| `:acceptance` | A14 | a non-empty list of criteria; each is a non-empty sentence string, or a list with `:id "<id>"`, `:subject "<subject>"`, `:kind` from `test job merged attested landed` and `:predicate` from `passes succeeds merged-at attested-by merged-or-closed-in-base landed-in` |

Rule A1 is the work-set form itself (section 2) and A2 is the unit id rule (section 2).

A unit carrying every checked key:

```lisp
(unit "verb:hygiene"
  :needs () :pr 1253 :branch "rowan/verb-hygiene" :lane "pulse"
  :owner "swarm:flash"
  :state :open
  :resources ((:lane "pulse") (:cpu 2) (:memory-gb 4) (:disk-gb 10) (:class "darwin-runner" :n 1))
  :writes ("cmd/nova-ci/" "internal/ci/cired.go")
  :tools ((:go :at "1.26" :key "toolchain-a"))
  :collects ((:name "receipts" :under "out/receipts" :members :unknown-before-run))
  :warm (:retained ((:worktree "mas-bandwidth/nova-tools")) :active ((:cpu 2)))
  :attempts ((:n 1 :rung "flash" :owner "swarm:flash" :started "2026-09-18T09:10Z"
              :outcome :red :proof (:kind :exit :value "1"))
             (:n 2 :rung "child:opus" :owner "Rowan" :outcome :uncertain))
  :acceptance ((:id "a1" :kind :test :subject "test:internal/ci@HEAD" :predicate :passes)
               "the hygiene verb runs clean on every bench")
  :title "bench hygiene as a verb")
```

## 4. Reading a unit

`WorkSet` carries `File`, `ID`, `Title`, `Fields`, `Unknown` and `Units` (in written order).
`WorkSet.Unit(id)` finds a unit by id. `WorkSet.WithoutAcceptance()` returns the ids of the
units that carry no criterion in the `(:id ...)` form, and `WorkSet.LoadCheck()` refuses the set
naming those ids.

The accessors on `Unit` read its fields and never refuse:

| accessor | returns |
| --- | --- |
| `ID` | the unit's id |
| `PR()` | the `:pr` value as written: an integer rendered in decimal, or a string, keyword or symbol as its text; `""` when absent |
| `Branch()` | the `:branch` value as text; `""` when absent |
| `Lane()` | the lane named in `:resources`, else the plain `:lane` string; `""` when neither |
| `Needs()`, `Writes()` | the strings of the `:needs` and `:writes` lists, in written order |
| `Owner()` | the `:owner` string; `""` when absent |
| `Title()`, `Deadline()` | the `:title` and `:deadline` strings as written |
| `Status()` | the `:status` value as text |
| `State()` | the `:state` word; `open` when the unit carries none |
| `Resources()` | the vector as counts: a lane entry is `lane`=1, a class is `class:<name>`=n, any other entry keeps its name; a unit with no `:resources` and a lane has `lane`=1 |
| `Attempts()` | each record's byte range, `N`, `Rung`, `Owner`, `Started`, `Outcome`, and its `Proof` when it carries one |
| `Tools()` | each entry's `Verb`, `At` and `Key` |
| `Collections()` | each entry's `Name`, `Under`, and `MembersUnknown` when `:members` is `:unknown-before-run` |
| `Warm()` | the `:retained` and `:active` lists |
| `Acceptance()` | the `(:id ...)` criteria, each with `ID`, `Kind`, `Subject`, `Predicate` |
| `AcceptanceText()` | every criterion as text: a sentence as written, a `(:id ...)` criterion as its subject |

## Tests this spec demands

The reader tests run on byte fixtures and the work-set tests on the fixtures under
`internal/worklang/testdata/`; none reaches a network or a model.

1. `TestWorklangReader/worklang-reader-refuses-a-dispatch-macro` — a `#.` is refused naming the dispatch macro, its byte offset and the file.
2. `TestWorklangReader/worklang-reader-enforces-the-three-bounds` — input past `--max-bytes`, `--max-depth` or `--max-nodes` is refused naming the bound, never truncated.
3. `TestWorklangWorkSet/worklang-reads-the-real-work-set` — a `(work-set ... :units ...)` file reads with every unit, a `:derive` template is not a unit, and unknown keys are kept.
4. `TestWorklangWorkSet/worklang-unit-id-is-stable-and-required` — a unit with no id, an empty id, or a repeated id is refused.
5. `TestWorklangWorkSet/worklang-attempt-without-a-termination-proof-is-uncertain` — an attempt whose outcome is not `uncertain` and carries no `:proof` is refused.
6. `TestWorklangWorkSet/worklang-uncertain-is-a-state` — `uncertain` is a state of its own in the closed set.
7. `TestWorklangWorkSet/worklang-resources-are-a-vector-not-a-slot` — `:resources` reads as a vector of counts; an entry that is not a `(:<name> <value>)` list is refused.
8. `TestWorklangWorkSet/worklang-lane-is-a-resource-of-capacity-one` — a lane is `lane`=1; two lane entries, or a `:lane` that disagrees with `:resources`, is refused.
9. `TestWorklangWorkSet/worklang-writes-are-paths-under-the-repo` — `:writes` reads as paths; a non-string or an absolute path is refused.
10. `TestWorklangWorkSet/worklang-tools-name-a-verb-and-a-version` — `:tools` reads verb, version and key; an entry with no `:at` is refused.
11. `TestWorklangWorkSet/worklang-a-collection-names-its-members-after-the-run` — `:collects` reads name, directory and unknown members; an entry with no `:name` or `:under` is refused.
12. `TestWorklangWorkSet/worklang-warm-state-is-retained-apart-from-active` — `:warm` reads both halves; one half alone is refused.
13. `TestWorklangWorkSet/worklang-owner-is-a-mind` — `:owner` reads as a string; a non-string owner is refused.
14. `TestWorklangWorkSet/worklang-acceptance-is-read-and-the-real-set-has-none` — criteria read in the `(:id ...)` schema, `WithoutAcceptance` names the units with none, and a predicate outside the closed set is refused.
15. `TestAWorkSetWithNoUnitIdIsARefusal` (`internal/tokens`) — `nova-tokens` refuses a `--units` set that carries no unit.
16. `TestUnitsMatchTheThreeThingsATranscriptNames` (`internal/tokens`) — a unit's `:pr`, `:branch` and `:lane`, read through the accessors above, are what a transcript is matched on.
