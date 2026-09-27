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
