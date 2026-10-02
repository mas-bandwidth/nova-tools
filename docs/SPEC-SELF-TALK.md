# SPEC-SELF-TALK: the self-talk register and self-check reconciliation

## 1. Overview

`nova-self-talk` operates at the level of prose and identity integrity. It serves two functions:
1. **Advisory classifier**: scans prose for standing capability denials and self-verdicts (installations).
2. **Reconciliation gate**: `nova-self-talk reconcile <answer-file>` mechanically reconciles self-check answer files against the baseline question set (`identity/self-check.md`), refusing commits when they do not account.

## 2. Invocation

```
nova-self-talk version
nova-self-talk help
nova-self-talk reconcile [--questions <path>] <answer-file>...
nova-self-talk [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] <file>...
```

Exit codes:
- `0`: OK (no findings for scan; answers account for questions for reconcile).
- `1`: Refused / findings (standing/installation findings; answers and questions do not account).
- `2`: Usage / could not run (bad invocation, unreadable file, unmeasurable question set).

## 3. Reconcile Semantics

### The Accounting Invariant
Reconciliation verifies that an answer file accounts for the exact size of the question set measured in `identity/self-check.md`:
- `Total = stems + authored - dup`
- `Unassisted`: count of answers written cold (matching `^\d+\.\s+\*\*` or `^-\s+\*\*`).
- `Gaps`: count of missing questions named as `SPOILED`, `SKIPPED`, or `NO ANSWER`.
- `Matched`: `Unassisted + Gaps`.

The gate passes (`exit 0`) when:
```
Matched >= Expected
```
And refuses (`exit 1`) when:
- `Unassisted < Expected` and unaccounted gaps remain.
- `Unassisted > Expected` (over-answered).

### Leaks Nothing
`reconcile` reads the baseline solely for a **count** and never prints question text on stderr or stdout. The cold read survives the gate.

### Output Grammar
Event output follows standard `TOKEN OK|FAIL key=value`:
```
RECONCILE OK file=<file> matched=<m> unassisted=<u> gaps=<g> expected=<e>
RECONCILE FAIL file=<file> matched=<m> unassisted=<u> gaps=<g> expected=<e> missing=<k>
```
