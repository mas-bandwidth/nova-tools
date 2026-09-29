# nova-privacy — specification

`nova-privacy` screens a piece of outgoing writing (a post, a message, a commit
body) against the author's private material before it goes out. A flag is a
reading assignment: a mind reads the payload before it leaves. The tool never
says clean. The strongest claim it makes is that nothing was proven, and a
screen that could not check says so in words and in its exit code, never in the
words of a screen that checked.

The judgment is the library `internal/privacy`; the command is a thin layer
over it. Another tool screens in process by building a `privacy.Spec` (or a
`privacy.Options`, the flags' shape) and calling `privacy.Screen`, or
`privacy.Load` then `privacy.Judge`.

## What it measures

A **source** is a file of private material. It is split into **entries**: a
line starting with an entry token and one blank (`## ` or `- ` by default)
opens an entry; everything before the first entry is a preamble and belongs to
none.

What makes an entry **private**, each rule on its own:

- the marker (`(private)` by default) appears anywhere in its title or its
  body. There is no window: a marked line far down an entry makes the whole
  entry private, title included, since the lines before it are part of the
  same idea;
- a line of the preamble that carries the marker opens a private entry of its
  own, running to the next entry. A preamble that explains the marker by
  writing it therefore becomes a private entry; name the marker without
  writing it to keep the preamble out of the corpus;
- it is under a private heading: an entry opened by a heading token (`##`, or
  any token of `#` characters) that is private owns every entry after it until
  the next heading of the same or a higher level (`## ` or `# ` after a `## `
  section), whether or not that heading opens an entry. A heading line inside
  a body that carries the marker (`### A plan (private)` inside a `## ` entry
  or a bullet) makes its entry private and owns what follows it from its own
  level. Bullets and sub-entries in such a section are private, each still its
  own entry.

Together these leave no line that carries the marker outside a private entry;
a property test checks that over thousands of arrangements of lines.

The marker is matched case-insensitively with whitespace normalised on both
sides: any Unicode space (a no-break space, a tab, an ideographic space) reads
as one space, runs collapse, and invisible formatting characters (Unicode
category Cf: the zero-width space and joiners, the soft hyphen, the
byte-order mark) are dropped. `not public` matches `not<no-break space>public`
and `NOT   public`.

A private heading's entry is measured with the words of the entries under it,
so an idea written as a heading and its bullets is compared as one. Rarity
still counts each entry once, by its own title and body.

Words are taken the same way from the payload, the entries and the
background, after the text is **normalised**:

- invisible formatting characters (Unicode category Cf: the zero-width space
  and joiners, the soft hyphen, the byte-order mark) and variation selectors
  are dropped, so they join rather than split a word;
- Unicode NFKC is applied (`golang.org/x/text/unicode/norm`), so fullwidth
  letters, ligatures and other compatibility forms read as plain letters;
- case is folded;
- accents are removed from Latin letters: a combining mark after a Latin
  letter is dropped (`é` reads `e`), and `ß æ œ ø ł đ ð þ ı` are spelt
  `ss ae oe o l d d th i`; marks on other scripts are kept;
- curly and modifier apostrophes read as `'`, and the Unicode hyphens U+2010
  and U+2011 as `-`.

A **word** is then a run of letters and digits in any script, starting with a
letter, that may hold a single `-` or `'` between two of its characters (`--`
and a dash split words). A trailing `'s` or `'` is stripped. A hyphenated word
is taken whole, with its hyphens removed, and as each of its parts:
`flibberty-wumpus` gives `flibbertywumpus`, `flibberty` and `wumpus`, and
`zar-quon` gives `zarquon`. Each key has simple English plurals folded: `-ies`
becomes `-y`; `-es` is dropped after `s`, `x`, `z`, `ch` or `sh`, and a
singular ending `-se`, `-xe`, `-ze`, `-che` or `-she` drops its `e` so that
`house` and `houses` meet as `wumpus` and `wumpuses` do; otherwise a final `s`
is dropped unless the word ends `-ss`, `-us` or `-is`. Irregular plurals,
`-oes`, `-ves` and the plural of an `-ie` noun (`movies`) are not folded.

A **term** is a key whose word, as written before its plural was folded, is
four or more runes long, and that is not a stop word. Stop words and the
marker's own words go through the same normalisation. A term is
**distinctive** for a private entry when all three hold:

- at most 4% of all entries hold it (never fewer than 2);
- at most 3% of the background documents hold it (never fewer than 3);
- its word, as written, is at least five runes long.

A hyphenated compound shared whole counts as three terms (the whole and its
two parts), so one private compound of two rare words, named in a payload,
reaches the threshold alone.

The **background** is a set of directories of the author's ordinary writing,
each read recursively or flat, keeping the files whose names a pattern matches
(case-insensitively: `*.md` matches `PAGE.MD`). It is what keeps ordinary
English from scoring as rare. The roots form one pool: a file reachable from
two roots, by file identity (device and inode), is one document, counted under
the first root that holds it (`shared=` on the later root's line).

The pool is read whole when it is within both bounds, `max-docs` (20,000 by
default) and `max-bytes` (64 MiB by default, enough for 5,000 documents of up
to 13 KiB each). Over either bound, the documents are taken in order of the
FNV-1a 64 hash of `<root as written>/<path relative to the root>`, while both
bounds hold, and a warning says how many were found and how many read. The
sample follows neither names nor dates, so no directory and no period is left
out as a block, and it is the same every run. `corpus` prints the pool as
`CORPUS SAMPLE found= read= bytes= rule= max-docs= max-bytes=`, where the rule
is `all` or `lowest-path-hash`; every screen line carries `background=`
(read), `found=` and `sample=`.

Which way a sample errs: a term counts as distinctive only when few background
documents hold it, so a sample over-weighting some directories drops their
words out of the fingerprints (the screen clears more) and a sample leaving
directories out adds theirs (it flags more). A hash sample is uniform, so it
errs neither way in expectation.

A payload that shares **three** or more distinctive terms with one private
entry flags that entry. Two shared terms are an accident of prose.

**Structure shapes** are regular expressions in the configuration, matched
against the payload with no corpus behind them. A `refuse` shape flags the
payload; a `warn` shape is reported and does not. Refusing shapes run first and
their matches are removed before the warning shapes run, so one specimen is
reported once. An `allow` entry names a specimen that never fires. No shape is
built in.

## Outcomes and exit codes

| exit | outcome | means |
|------|---------|-------|
| 0 | `UNPROVEN-CLEAN` | screened, nothing proven; the only outcome that permits sending |
| 1 | `FLAGGED` | shared vocabulary with a private entry, or a refused shape; a mind reads it first |
| 2 | could not run | a bad invocation, a payload that is unreadable, empty or not text, a missing or malformed configuration, a configuration with no source |
| 3 | `CORPUS-UNREADABLE` | a declared source could not be read, so the corpus is not the configured one |
| 3 | `NO-PRIVATE-CORPUS` | every source loaded and no entry is marked private |
| 3 | `PAYLOAD-HAS-NO-WORDS` | the payload was read and holds no word: invisible characters, punctuation or digits alone |
| 3 | `NOTHING-CAN-EVER-FIRE` | private entries exist and none has three distinctive terms |

Exit 3 is this tool's own code: "I could not verify" is neither a pass nor a
finding. The four exit-3 outcomes are spelled apart because their remedies
differ. `Outcome.Cleared` is an equality against `UNPROVEN-CLEAN`, so an outcome
added later cannot clear by default.

The order of judgment is fixed. A refused structure shape outranks every
exit-3 outcome, and the corpus problem is still reported beside it. Then an
unreadable source, then no private entries, then a payload with no words, then
vocabulary flags, then the corpus that can never fire.

A declared source is text: UTF-8, with a leading byte-order mark dropped, or
UTF-16 that opens with a byte-order mark. A source that is not text, or that
yields no entry at all (empty, prose with no entry token, `# ` headings only),
is `CORPUS-UNREADABLE` by name, whether or not the other sources are fine: its
private material, if it holds any, was not seen. A source that has entries and
none of them private raises a `WARN` line on every screen and in `corpus`,
since a lost marker looks exactly like that.

With no background document read, the screen runs and warns: rarity is measured
against the private sources alone, which flags more readily, never less. A
missing or unreadable background root is a warning for the same reason. A
missing source is a refusal, because it hides private material.

## Configuration

`--root <dir>` reads `<dir>/.nova-privacy`; `--config <file>` reads that file
instead. Paths in the file are relative to the file's directory. One keyword
per line; a blank line or one starting `#` is ignored.

```
source private/ideas.md
source private/later.md
background recursive *.md notes
background flat *.md journal
marker (private)
entry ##
entry -
stop harbour lantern
refuse home-path /home/[a-z]+(/[A-Za-z0-9._-]+)*
warn tool-name \bacme-[a-z]+\b
allow acme-docs
max-docs 20000
max-bytes 64M
```

| keyword | value |
|---|---|
| `source` | a file of private material (the rest of the line) |
| `background` | `recursive` or `flat`, a file pattern, a directory (the rest of the line) |
| `marker` | the text that declares an entry private; at most once |
| `entry` | a token that opens an entry when followed by a blank; the list replaces `##` and `-` |
| `stop` | extra stop words |
| `refuse`, `warn` | a class name and a regular expression (RE2) that needs at least one character; one that matches the empty string matches every payload and is refused |
| `allow` | a specimen that never fires, matched case-insensitively |
| `max-docs` | background documents read, at most; one or more |
| `max-bytes` | background bytes read, at most; one or more, with an optional `K`, `M` or `G` (KiB, MiB, GiB) |

Every malformed line is reported in one run, each with its line number. A
configuration with no source is refused. Two sources that are one file, by file
identity once the path is resolved (the same line twice, `private/./ideas.md`
beside `private/ideas.md`, a symlink to another source), are refused by name
(exit 2): a source counted twice doubles every count in it and can push a
term over the rarity bound, which silences a flag. `privacy.Load` given such a
`Spec` in process marks the second one unreadable. A source the configuration declares
that cannot be read is `CORPUS-UNREADABLE`, and the remedy is
`remove it from <config> or restore the file`.

Flags override the file: any `--source` replaces its sources; any
`--background` or `--background-flat` replaces its roots (with `--pattern`,
`*.md` by default); `--marker`, `--max-docs` and `--max-bytes` replace theirs. A `--root` or
`--config` that is named must hold a configuration, even when `--source` is
given: a misspelt root would otherwise drop the refuse shapes, the marker, the
stop words and the background unseen. `--source` with neither names the corpus
alone. Flag paths are relative to the working directory.

## Bounds

Every read is bounded, and every refusal or warning a bound causes names it.

| bound | value | past it |
|---|---|---|
| `MaxConfigBytes` | 64 KiB | the configuration is refused (exit 2) |
| `MaxSourceBytes` | 8 MiB | the source is unreadable (exit 3) |
| `MaxPayloadBytes` | 4 MiB | the payload is refused (exit 2) |
| `MaxDocBytes` | 1 MiB | the background document is left out, with a warning |
| `MaxWalkEntries` | 200,000 | a recursive walk stops there, with a warning |
| `max-docs` | 20,000 documents | the lowest path hashes that fit, with a warning |
| `max-bytes` | 64 MiB | the lowest path hashes that fit, with a warning |

## Verbs

```
nova-privacy screen [corpus flags] [--json] [--max <n>] <file|->
nova-privacy corpus [corpus flags] [--json]
nova-privacy version
nova-privacy help [<verb>]
```

`screen` reads the payload from a file, or from standard input for `-`. There
is no flag that takes the payload as an argument: an argument is visible to
every process that can list processes. An empty payload is refused, since it
cannot be told from content that never arrived.

A payload must be text. UTF-16 that opens with a byte-order mark is decoded; a
leading UTF-8 byte-order mark is dropped; anything else must be valid UTF-8
with no NUL byte, or it is refused (exit 2) with the offset of the first bad
byte. Measured as bytes, such a payload would yield no words and clear. A
payload that is text and holds no word (a word is a run of letters and digits
with at least one letter, in any script) is `PAYLOAD-HAS-NO-WORDS`, exit 3.

`corpus` loads what a screen would load and prints it, so a corpus can be seen
before a screen is trusted. It measures no payload and matches no structure
shape (`privacy.JudgeCorpus`). Its exit follows the same table: 0 when a screen
could reach a verdict, 3 when every screen would come back unverified.

The tool reads no environment variable. Nothing it reads is interpreted: the
payload and the corpus are counted as words and matched against patterns.

## Output

One line per event. Lines for a cleared payload and the corpus inventory go to
standard output; flags, refusals, warnings and remedies go to standard error.

```
SCREEN UNPROVEN-CLEAN chars=<n> terms=<n> private=<n> checkable=<n> entries=<n> background=<n> found=<n> sample=<all|lowest-path-hash> config=<path|->
SCREEN NOTE nothing was proven: ...
SCREEN FLAG source=<path> entry=<n> shared=<n> terms=<t,...> title=<title>
SCREEN STRUCTURE class=<class> specimen=<text>
SCREEN MORE kind=flag shown=<n> total=<n> <remedy>
SCREEN FLAGGED flags=<n> structure=<n> chars=<n> ...
SCREEN REMEDY <remedy>; after an edit, run: nova-privacy screen <the same inputs>
SCREEN <CORPUS-UNREADABLE|NO-PRIVATE-CORPUS|PAYLOAD-HAS-NO-WORDS|NOTHING-CAN-EVER-FIRE> <reason>
SCREEN REMEDY <remedy>; then run: nova-privacy corpus <the same inputs>
SCREEN WARN <warning>        (for example: source: <path> has <n> entries and none is marked <marker>; ...)
CORPUS SOURCE path=<path> entries=<n> private=<n>
CORPUS SOURCE path=<path> unreadable=<error>
CORPUS BACKGROUND root=<dir> mode=<recursive|flat> pattern=<glob> found=<n> shared=<n> read=<n>
CORPUS SAMPLE found=<n> read=<n> bytes=<n> rule=<all|lowest-path-hash> max-docs=<n> max-bytes=<n>
CORPUS OK sources=<n> entries=<n> private=<n> checkable=<n> background=<n> config=<path|->
CORPUS <outcome> sources=<n> ...
CORPUS REMEDY <reason>: <remedy>; then run: nova-privacy corpus <the same inputs>
CORPUS WARN <warning>
```

`checkable` is how many private entries could raise a flag at all; `private`
minus `checkable` is the part of the corpus no payload could be caught against.
A flag prints at most twelve shared terms, and `shared=` is always the whole
count. `--max` caps the flag lines (20 by default, 0 prints all).

`--json` prints one JSON object on standard output instead: `verb`, `outcome`,
`exit`, `cleared`, `reason`, `remedy`, `config`, the counts, `sources`, `roots`,
`flags`, `structure`, `warnings`, `bounds` and `sample` (`found`, `read`,
`bytes`, `rule`, `max_docs`, `max_bytes`). The exit code is the same.

## Tests

`internal/privacy` pins the judgment with hand-built corpora and tempdir trees:
each refusal path, the threshold at exactly three, the background model as the
thing that keeps ordinary words quiet (the same payload flags without it), the
hash sample under each bound, one file under two roots counted once, and every
bound. `cmd/nova-privacy` pins the exit
contract, the evidence on a flag, the remedies, `--json`, verb help, and the
docs/TESTS.md first run against `cmd/nova-privacy/testdata/example`.
