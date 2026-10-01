# The work language (SPEC-WORKLANG)

`internal/worklang` is the bounded reader for a restricted s-expression file. It reads data and
never evaluates it. This page specifies the reader: the five kinds of form, the tokens it
refuses, the three bounds it enforces and the shape of a refusal.

The living caller is nova-work's tree file. `internal/workfile` reads it with
`worklang.Read(file, data, limits)` and checks the shape of the records itself
([SPEC-WORK-V1.md](SPEC-WORK-V1.md) section 1); the reader knows nothing of the records and
returns a `Form`. No other tool calls the package.

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
`--max-nodes`). The tree file reader sets depth 16 and lets nodes equal bytes (`workfile.Limits(maxBytes)`). A file longer than
`MaxBytes` is refused before a byte of it is parsed. A list nested past `MaxDepth` is refused
at its opening byte. Every atom (keyword, string, integer, symbol) counts as one node, and the
atom past `MaxNodes` is refused at its byte. A limits value with any bound at zero or below is
refused rather than guessed. Past a bound, the input is refused whole and never truncated.

**One form per file.** Bytes after the first form, an unbalanced list and an unterminated
string are each refused naming the byte.

**Refusals.** Every refusal is a `*worklang.Refusal` whose `ExitCode()` is 2 and whose message is
`plan file=<file>: <reason>`. The reason names the byte or the bound it refuses.

**Byte ranges.** A form carries `Offset` (its first byte), and a list, string, keyword or
integer also carries `End` (the byte just past it). A caller names a byte in its own refusals
from `Offset`.
