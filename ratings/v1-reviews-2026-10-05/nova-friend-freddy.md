# nova-friend review, 2026-10-05

Rater: inception/mercury-2.5
Build: 3baf154
Verdict: NOT YET for an AI to use
Score: 3/10

## Reasons

nova-friend is a deprecated tool that no longer exists in the main nova-tools repository. It has been moved to the deprecated/ directory with no clear replacement or migration path documented. For a friend harness to use this tool cold, it would fail immediately as there is no nova-friend binary available in cmd/.

First confusion: The brief asks to review nova-friend, but the tool only exists in deprecated/cmd/nova-friend. There is no cmd/nova-friend in the main cmd/ directory, no help documentation in docs/CLI.md, and no SPEC-FRIEND.md spec file. The documentation exists only under deprecated/docs/nova-friend/README.md and deprecated/docs/CLI.md.

First doubted claim: The documentation says "nova-friend is the one tool for what a friend, or a coordinator, does about a friend" but there is no active implementation to review or run. The deprecated directory's PACKAGES file lists it as deprecated, and the tool's functionality appears to have been absorbed or replaced by other components (internal/nsprint/friend package exists but has no cmd interface).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/ directory | nova-friend binary does not exist in main cmd/; only in deprecated/cmd/nova-friend. An AI worker cannot discover or use this tool. | Either remove from deprecated and delete docs, or add back to cmd/ with proper build targets | L |
| 2 | docs/CLI.md | No section on nova-friend in main docs/CLI.md. The deprecated/docs/CLI.md has it but that file is not part of the main documentation. | Move documentation to main docs/CLI.md if tool should be active, or remove all docs if deprecated | M |
| 3 | docs/ | No SPEC-FRIEND.md file exists. The tool has no spec document for behavior contracts. | Either add spec to docs/ or remove all spec references | M |

## Good, keep

- The deprecated docs/nova-friend/README.md has clear examples and structured documentation
- The tool design separates configuration (nova-config) from runtime (nova-friend) cleanly
- Refusal messages follow the nova standard (names what was wrong, suggests next step)
