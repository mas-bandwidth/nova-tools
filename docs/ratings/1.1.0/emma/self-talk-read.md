# nova-self-talk READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8.5/10
README: 8.5/10

## Reasons
The tool addresses a genuine and difficult problem: helping automated agents audit self-reflective prose without penalizing necessary rules, prohibitions, or dated records. The separation between negative capability denials (STANDING) and neutral-worded trait or foreclosure constructs (INSTALLATION) is thoughtful and backed by empirical specimens. The documentation sets clear boundaries: findings are advisory judgments for the writer rather than mechanical gate refusals, and dated observations are welcomed rather than flagged.

Before examining the code, the README entry presents a clear summary and an executable trial command. However, reading through the documentation and source reveals friction points that prevent a top score:

1. Confused: The second classification family is designated INSTALLATION in docs/CLI.md:275 and cmd/nova-self-talk/main.go:61 without explaining why this terminology was chosen. A newcomer understands STANDING and DATED immediately, but INSTALLATION remains unexplained jargon across both CLI help and the specification.
2. Bored: The usage string in cmd/nova-self-talk/main.go:30 spans 85 lines, presenting an exhaustive essay on linguistic classifications before providing command syntax. This verbosity duplicates details available through the shapes command and CLI documentation.
3. Doubted a claim: The package comment in internal/selftalk/selftalk.go:21 asserts that widening the pattern to reach neutral traits flags half of any file. This semantic overstatement is repeated in line 97, asserting an exaggerated failure mode rather than characterizing measured false positive rates.

To achieve a 10/10 rating, the tool would need to:
- Adopt the standard tool framework from internal/tool for flag handling and dispatch instead of maintaining bespoke positional parsing.
- Provide a clear one-line definition for the INSTALLATION category or align terminology with standard grammatical descriptors such as trait or verdict.
- Condense the CLI usage banner to the primary onboarding contract, delegating deeper grammatical exposition to documentation.
- Replace polemical comments and uppercase section headers with measured, present-tense rationale.

## Findings
| # | where | finding | fix | size |
| 1 | cmd/nova-self-talk/main.go:61 | The label INSTALLATION is introduced without defining why that noun was chosen for neutral trait verdicts | Add a brief gloss clarifying that installation refers to adopted standing self-verdicts | S |
| 2 | cmd/nova-self-talk/main.go:190 | Main routine hand-rolls positional flag handling and error printing rather than using the standard tool skeleton | Migrate command dispatch to the shared tool runner while preserving positional scan syntax | M |
| 3 | cmd/nova-self-talk/main.go:30 | The usage banner extends over 85 lines, embedding an extensive linguistic essay into standard terminal help | Trim the usage banner to essential onboarding points and refer readers to the shapes verb | S |
| 4 | internal/selftalk/selftalk.go:21 | Package documentation asserts that widening patterns flags half of any file, an unmeasured rhetorical claim | Rephrase comment to describe concrete false-positive thresholds on evaluated corpora | S |
| 5 | internal/selftalk/installation.go:8 | Internal commentary relies on capitalized headings and dramatic rhetoric rather than calm technical explanation | Convert all-caps section titles into standard sentence-case design rationale | S |

## Good, keep
- The strict structural distinction between negative capability denials and neutral-worded trait or foreclosure shapes.
- The no-defaults invariant for --skip and --rule-doc ensuring no specific repository filenames are hardcoded.
- The shapes command which prints the live detector table alongside verified test sentences and matched patterns.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| off the skeleton | STILL THERE | cmd/nova-self-talk/main.go:190 implements custom runStdin dispatch and argument parsing rather than tool.Tool |
| an 88-line banner | CHANGED | cmd/nova-self-talk/main.go:30 usage banner is 85 lines long, remaining an extensive multi-paragraph treatise |
| comments in capitals | STILL THERE | internal/selftalk/installation.go:8 retains all-caps headings like WHAT OCCASIONED IT and WHY THE FIRST CLASS CANNOT SIMPLY BE WIDENED |
| a class name nobody glosses | STILL THERE | cmd/nova-self-talk/main.go:61 and docs/CLI.md:275 use INSTALLATION without explaining the term's meaning |
| jargon and a semantic overstatement | STILL THERE | internal/selftalk/selftalk.go:21 claims widening patterns flags half of any file |
