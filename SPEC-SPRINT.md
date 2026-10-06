# Sprint Specification

This document describes the nova-sprint command and its verbs.

## stream set --base

### Purpose

The `stream set --base` verb re-points cards in a stream to a new base branch. This is useful when the original base branch has been merged, deleted, or is no longer available.

### Syntax

```
nova-sprint stream set <stream>... --base <branch>
```

### Behavior

For each stream specified:
- Cards that have not been dealt yet (queued) have their BASE line rewritten to the new branch
- Cards that are queued to merge have their BASE line rewritten to the new branch
- Cards that are already dealt or working keep their original base and are listed in the output

### Validation

The command refuses to make changes in the following cases:
1. The specified branch does not exist in origin
2. A card's PATHS are absent at its tip

In these cases, the card is skipped and an error message is reported.

### Example

```
nova-sprint stream set sprint-next --base rowan/bus-rename
```

This would re-point all eligible cards in the sprint-next stream to use rowan/bus-rename as their base branch.

### Attribution

stream-set-base-b.w2.g9.e15: Added stream set --base verb to re-point stream cards to a live base when the original base is gone or merged.
