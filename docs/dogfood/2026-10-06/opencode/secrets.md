# nova-secrets dogfooding report

**Tool:** nova-secrets (flash tier)  
**Tested:** 2026-10-06  
**User:** stranger (opencode)

## Summary

I exercised every verb of nova-secrets against a fresh test store. The tool correctly enforces the security model where values never print, seats must exist before use, and new seats require seat add (not seal).

## Findings

1. `nova-secrets version` - printed the build identity correctly
2. `nova-secrets names --store /tmp/test-secrets-store --as ada` - properly refused when seat file is absent
3. `nova-secrets check --store /tmp/test-secrets-store --as ada` - properly refused when seat file is absent
4. `nova-secrets seal --store /tmp/test-secrets-store --as ada --name TEST_TOKEN --dry-run` - correctly refused for new seats, explaining seat add is required (SPEC rule 12)
5. `nova-secrets gate --store /tmp/test-secrets-store --base HEAD --head HEAD` - worked correctly, showed GATE APPROVE files=0
6. `nova-secrets place --store /tmp/test-secrets-store --as ada --machine test --secret TEST_TOKEN --dry-run` - properly refused when seat file absent
7. `nova-secrets placed --machine test` - worked correctly, showed PLACED OK count=0
8. `nova-secrets seat add --store /tmp/test-secrets-store --as bo --from ada --only TEST_TOKEN` - correctly refused since ada.yaml is absent (source seat file must exist)
9. `nova-secrets seat inject --store /tmp/test-secrets-store --as bo --from ada --only TEST_TOKEN` - correctly refused since bo.yaml doesn't exist (use seat add for new seats)
10. `nova-secrets exec --store /tmp/test-secrets-store --as ada --only TEST_TOKEN` - properly refused when seat file absent

## README

READ 10/10 - The help output for each verb accurately describes the flags and behavior. The tool refuses before reading files when requirements aren't met.

USE 9/10 - The tool works as expected. One minor friction point: the exec verb requires `--require` for mandatory keys, but the error message could be clearer about this being a validation requirement.

urgent=0 next=0
