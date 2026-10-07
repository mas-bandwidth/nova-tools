# nova-memory READ and USE rating, nova-tools 1.2.0

Rater: Zhi
Build: a65b5cba44ab
READ: 8/10
USE: 8/10

The tool as released in nova-tools v1.2.0 was read cold and tested on a throwaway directory of markdown files. The tool performs well for search and verification of notes.

## Reasons

READ. The banner provides a good overview of the tool. The "how it works" section is clear about the index being in-memory and read-only. The setup and example blocks are runnable and give a quick understanding of the tool's capabilities. Every verb's `-h` is helpful and provides usage info. The exit codes are documented.

What keeps READ at 8. `verify -h` lists `--root` as repeatable (shared flag), but `verify` itself rejects multiple roots. `eval -h` does not describe the expected `gold.tsv` format; it just says `<gold.tsv>`. The CAL rule in the banner is potentially confusing as it suggests a "band" below which results are noise, but it's often close to the actual hits, as noted in previous ratings.

USE. The tool is straightforward to use. `quickstart` makes the first run very easy. `search`, `stats`, `check` worked as expected. The tool's output is well-structured and consistent with other nova-tools. The refusal messages are helpful and point to the correct help commands.

What keeps USE at 8. `verify`'s `--coverage` flag required a relative path format (`notes/lantern.md:notes/index-notes.md`) which wasn't immediately obvious, and the first attempt failed because I used full paths, which is understandable but could be better documented. The `CAL` line is still printed but I found it hard to interpret for real-world usage on a small corpus.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory verify -h` | `verify` lists `--root` as repeatable (shared flag), but rejects multiple roots. | Update `verify -h` to not say repeatable, or implement support for multiple roots. | S |
| 2 | `nova-memory eval -h` | Does not describe the format of `gold.tsv`. | Add a one-line description in `eval -h` and a small example. | S |
| 3 | `nova-memory verify` | `--coverage` path format is not clearly documented in `help` or `verify -h`. | Clarify the path format (relative to --root) in help. | S |

## Good, keep

The `quickstart` verb is an excellent onboarding mechanism. The refusal grammar pointing towards the specific verb's `-h` is great for discoverability. The consistent structure of the tool and the help system makes it easy to work with once the basics are understood.

## Compared with earlier ratings

This is the first rating for 1.2.0 for this rater, so there is no previous rating to compare.
