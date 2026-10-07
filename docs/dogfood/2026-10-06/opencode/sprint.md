# Dogfood Report: nova-sprint
Date: 2026-10-06
Friend: opencode

## Testing Summary

Tested nova-sprint against the in-memory twin (mem:sprint.twin) without full push setup.
All read-only verbs work correctly. Write verbs that require coordinator push setup
return a clear refusal message.

### Commands Tested

1. nova-sprint -h
   First 3 lines: to members (machines with a width), sends finished work to readers
   Expected: Help text with all commands
   Grade: NEXT (very long help output, but complete)

2. nova-sprint help add
   First 3 lines: usage: nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id>
   Expected: Detailed help for add verb with examples and flags
   Grade: NEXT (help is comprehensive but verbose)

3. nova-sprint help land
   First 3 lines: usage: nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>]
   Expected: Detailed help with examples
   Grade: NEXT

4. nova-sprint help brief
   First 3 lines: usage: nova-sprint brief <id> (--brief <text> | --brief-file <path>)
   Expected: Detailed help
   Grade: NEXT

5. nova-sprint help fleet
   First 3 lines: usage:
   nova-sprint fleet beat <member> [--load <percent>]
   Expected: Detailed help
   Grade: NEXT

6. nova-sprint init --readers reader-a,reader-b --members m1
   First 3 lines: INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-a,reader-b
   Expected: Successfully initializes sprint store
   Grade: NEXT

7. nova-sprint add --stream s1 --count 3 --brief "tier: flash\nbrief for s1"
   First 3 lines: nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded:
   Expected: Add card to stream
   Grade: URGENT (verb fails when push setup incomplete, but this may be by design)

8. nova-sprint where
   First 3 lines: SPRINT TABLE  coordinator boss
   Expected: Shows work table state
   Grade: NEXT

9. nova-sprint inbox
   First 3 lines: INBOX OK judgments=0 happened=0 cursor=-
   Expected: Shows inbox state
   Grade: NEXT

10. nova-sprint streams
    First 3 lines: STREAMS OK streams=0 cards=0
    Expected: Lists streams
    Grade: NEXT

11. nova-sprint card s1-1 --brief
    First 3 lines: nova-sprint card: no primary s1-1; run: nova-sprint where
    Expected: Error for non-existent card
    Grade: NEXT (good error message)

12. nova-sprint needs
    First 3 lines: NEEDS OK cards=0 dropped-or-absent=0
    Expected: Shows card needs
    Grade: NEXT

13. nova-sprint held
    First 3 lines: HELD OK cards=0 held=0 behind=0
    Expected: Shows held cards
    Grade: NEXT

14. nova-sprint set --read-tier pro
    First 3 lines: nova-sprint set REFUSED: PUSH DOWN: boss has no push target recorded:
    Expected: Set sprint configuration
    Grade: URGENT (same push setup issue)

15. nova-sprint fleet beat m1
    First 3 lines: FLEET-BEAT OK m1 at=2026-10-07T14:20:31Z load=55.3%
    Expected: Update member beat
    Grade: NEXT

16. nova-sprint tick
    First 3 lines: TICK OK state=STOPPED nothing done; run: nova-sprint start
    Expected: Run one tick
    Grade: NEXT

17. nova-sprint queue --stream s1
    First 3 lines: QUEUE OK cards=0 epoch=0
    Expected: Show queue
    Grade: NEXT

18. nova-sprint where --all
    First 3 lines: SPRINT TABLE  coordinator boss
    Expected: Show full tables including merge and readers
    Grade: NEXT

19. nova-sprint start
    First 3 lines: nova-sprint start REFUSED: PUSH DOWN: boss has no push target recorded:
    Expected: Start the sprint machine
    Grade: URGENT

20. nova-sprint land --stream s1 --dry-run
    First 3 lines: nova-sprint land REFUSED: PUSH DOWN: boss has no push target recorded:
    Expected: Dry run land
    Grade: URGENT

## Readability (READ)
6/10 - The help text is comprehensive but verbose. New users may be overwhelmed by the
length of the output. The examples help but are sparse. Documentation in SPEC-SPRINT.md
is very long (634KB).

## Usability (USE)
5/10 - Many important verbs (start, set, add, land) refuse to work without full coordinator
setup, which is documented but requires additional steps. Error messages are clear but
require reading the help to understand the solution.

## Summary
urgent=4 next=16

The push setup requirement is documented but makes basic testing difficult. The in-memory
twin works for read operations and simple initialization, but write operations that
require coordinator communication are blocked.
