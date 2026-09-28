# Message body text

`StripQuotedAndCode` is the shared line filter used before reading typed
records in comments. It uses only the standard library and performs no I/O.

It normalizes CRLF, omits lines whose first non-space character is `>`, and
omits spans between lines starting with three backticks after whitespace.
An unmatched opening backtick fence hides the remainder. It preserves all
other line bytes and their order. Tilde fences and inline Markdown are not
interpreted by this existing protocol.

The webhook decoder and merge verdict reader share the same filter. Callers
supply their own record parsing and policy after filtering.

## Limits of this protocol

This filter does not determine whether GitHub displays a line as ordinary text,
quotation, code, or hidden HTML. Its output alone cannot establish that a record
was visibly presented to a reader. The extraction preserves the existing
protocol; changing which records count requires a separate policy decision.

The following constructs receive no special handling. A record on its own line
inside them stays available to callers unless a separate line starts with `>`
or three backticks:

- Tilde fences, indented code (four spaces or a tab), and multiline inline code.
- Lazy quote continuations: in `> earlier\nRECORD`, `RECORD` stays.
- HTML comments, `<blockquote>`, `<pre>`, and collapsed `<details>` blocks.

Fence recognition is deliberately literal. After trimming whitespace, **any**
line beginning with at least three backticks toggles one Boolean. Fence length,
indentation, nesting, info strings and trailing text do not change that rule.
These examples use `\n` for a newline and `RECORD` for an otherwise parseable line:

| Input shape | What the filter does |
| --- | --- |
| Four-backtick fence containing a three-backtick line | The inner line closes the filter's fence early; subsequent records can be retained. A later fence can hide text after the outer block. |
| A closing line of three backticks followed by ` bar` | Toggles just like a bare fence; records before the next toggle are retained. That next toggle can hide subsequent text. |
| A list item beginning `- ` followed by a fence | The opening line is retained. An indented closing fence toggles instead, so the block's records can remain while later text is dropped. |
| A line beginning with three backticks, `x`, three backticks, then ` is the flag` | Opens the filter's fence and drops following lines until another toggle. |
| Three backticks followed by ` a` then a backtick and `b` | Opens the filter's fence despite the backtick in the info text. |
| An indented fence, including one inside an indented code block | Still toggles, so following records can be dropped. |
| A backtick fence inside a tilde fence | Still toggles; tilde closing lines do not close it. |
| A fence preceded by NBSP, EM SPACE, or U+2028 | Unicode whitespace is trimmed for recognition, so it still toggles. |
| `> quoted\r\rRECORD` | Only LF splits lines: the entire CR-only sequence is one quoted line and is dropped. |
| A closing fence preceded by text and only CR | It is not a new line, so it does not close the filter's fence. |

These are properties of the line filter, not results of a GitHub renderer test.
