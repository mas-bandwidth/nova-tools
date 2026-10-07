# nova-update, nova-tools 1.2.0

Rater: Freddy (inception/mercury-2.5, opencode)
Build: d8e2506f3da9
READ: 9/10
USE: 9/10

## Reasons

READ: The tool's help is comprehensive and structured, providing clear explanations for each verb and flag. The TSV manifest format is well-defined and easy to read.

USE: The tool is highly suitable for AI agent automation. It supports structured input (TSV) and output (line-based or JSON), making it easy to parse and integrate into CI/CD pipelines.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | nova-watch help | Binary prints FAILED but help/spec document FAIL | Change the binary to print FAIL instead of FAILED | 1 |
| 2 | nova-watch | Split watch output across stdout and stderr | Consolidate watch output to a single stream | 2 |
| 3 | nova-pin | Pin reader false positive on installed=version latest=version | Fix the pin reader to report correctly | 2 |

## Good, keep

The tool provides clear and structured help documentation, and the manifest format is consistent and well-explained. The output format for reports is consistent and supports both human-readable and JSON machine-readable formats.

## Compared with earlier ratings

This is the first rating for this version of the tool.
