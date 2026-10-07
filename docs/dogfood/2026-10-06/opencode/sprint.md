# Dogfood Report: nova-sprint
Date: 2026-10-07
Friend: opencode

## Testing Summary

Tested nova-sprint against the in-memory twin (mem:sprint.twin) without full push setup.
Read-only verbs work correctly. Write verbs that require coordinator push setup return
a clear refusal message.

### Commands Tested

1. nova-sprint -h
   First 3 lines: nova-sprint: a sprint of work cards, dealt to a fleet of workers and read before they land
   Expected: Help text with all commands
   Grade: NEXT (verbose but complete)

2. nova-sprint help add
   First 3 lines: usage: nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id>
   Expected: Detailed help for add verb with flags
   Grade: NEXT (comprehensive)

3. nova-sprint help land
   First 3 lines: usage: nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>]
   Expected: Detailed help with examples
   Grade: NEXT

4. nova-sprint help brief
   First 3 lines: usage: nova-sprint brief <id> (--brief <text> | --brief-file <path>)
   Expected: Detailed help
   Grade: NEXT

5. nova-sprint help fleet
   First 3 lines: usage: nova-sprint fleet beat <member> [--load <percent>]
   Expected: Detailed help
   Grade: NEXT

6. nova-sprint init --readers reader-a,reader-b --members m1:64
   First 3 lines: INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-a,reader-b
   Expected: Successfully initializes sprint store
   Grade: NEXT

7. nova-sprint where
   First 3 lines: SPRINT TABLE  coordinator boss
   Expected: Shows work table state
   Grade: NEXT

8. nova-sprint inbox
   First 3 lines: INBOX OK judgments=0 happened=0 cursor=-
   Expected: Shows inbox state
   Grade: NEXT

9. nova-sprint streams
   First 3 lines: STREAMS OK streams=0 cards=0
   Expected: Lists streams
   Grade: NEXT

10. nova-sprint needs
    First 3 lines: NEEDS OK cards=0 dropped-or-absent=0
    Expected: Shows card needs
    Grade: NEXT

11. nova-sprint held
    First 3 lines: HELD OK cards=0 held=0 behind=0
    Expected: Shows held cards
    Grade: NEXT

12. nova-sprint sentinels
    First 3 lines: SENTINELS OK sentinels=0
    Expected: Shows sentinels
    Grade: NEXT

13. nova-sprint bases
    First 3 lines: BASES OK bases=0 cards=0
    Expected: Shows bases
    Grade: NEXT

14. nova-sprint log
    First 3 lines: 15:17:37  ctl-m1: m1 changed by boss...
    Expected: Shows log entries
    Grade: NEXT

15. nova-sprint check
    First 3 lines: CHECK OK violations=0 pending=-
    Expected: Shows check state
    Grade: NEXT

16. nova-sprint machinery
    First 3 lines: MACHINERY server DOWN addr=none why="NOVA_SPRINT_SERVER is not set"
    Expected: Shows machinery state
    Grade: NEXT (requires server setup)

17. nova-sprint handover
    First 3 lines: HANDOVER seat=boss since=init
    Expected: Shows handover state
    Grade: NEXT

18. nova-sprint routes
    First 3 lines: ROUTES OK routes=0
    Expected: Shows routes
    Grade: NEXT

19. nova-sprint rules
    First 3 lines: IDLE fleet=0/0 waiting=0 since=- roots=-
    Expected: Shows rules
    Grade: NEXT

20. nova-sprint stats
    First 3 lines: stages              | seconds (median max n)
    Expected: Shows statistics
    Grade: NEXT

21. nova-sprint live
    First 3 lines: LIVE SERVER binary=/home/glenn/.local/bin/nova-sprint inode=5019160
    Expected: Shows live server status
    Grade: NEXT

22. nova-sprint where --all
    First 3 lines: SPRINT TABLE  coordinator boss
    Expected: Shows full tables
    Grade: NEXT

23. nova-sprint tick
    First 3 lines: TICK OK state=STOPPED nothing done; run: nova-sprint start
    Expected: Run one tick
    Grade: NEXT

24. nova-sprint queue
    First 3 lines: nova-sprint queue REFUSED: wants one of --as <reader|member>, --stream <s>
    Expected: Shows queue
    Grade: NEXT (requires arguments)

25. nova-sprint fleet
    First 3 lines: nova-sprint fleet REFUSED: fleet wants one of its verbs
    Expected: Fleet commands
    Grade: NEXT (group verb)

26. nova-sprint friend
    First 3 lines: nova-sprint friend REFUSED: friend wants one of its verbs
    Expected: Friend commands
    Grade: NEXT (group verb)

27. nova-sprint reader
    First 3 lines: nova-sprint reader REFUSED: reader wants one of its verbs
    Expected: Reader commands
    Grade: NEXT (group verb)

28. nova-sprint stream
    First 3 lines: nova-sprint stream REFUSED: stream wants one of its verbs
    Expected: Stream commands
    Grade: NEXT (group verb)

29. nova-sprint lane
    First 3 lines: nova-sprint lane REFUSED: lane wants one of its verbs
    Expected: Lane commands
    Grade: NEXT (group verb)

30. nova-sprint goal
    First 3 lines: nova-sprint goal REFUSED: goal wants one of its verbs
    Expected: Goal commands
    Grade: NEXT (group verb)

31. nova-sprint merge-window
    First 3 lines: nova-sprint merge-window REFUSED: merge-window wants one of its verbs
    Expected: Merge-window commands
    Grade: NEXT (group verb)

32. nova-sprint seat
    First 3 lines: SEAT holder=boss epoch=0 generation=1
    Expected: Shows seat state
    Grade: NEXT

33. nova-sprint server
    First 3 lines: nova-sprint server REFUSED: server wants one of its verbs
    Expected: Server commands
    Grade: NEXT (group verb)

34. nova-sprint stats tidy
    First 3 lines: nova-sprint stats tidy REFUSED: names nothing to tidy
    Expected: Tidy stats
    Grade: NEXT (requires arguments)

35. nova-sprint help
    First 3 lines: nova-sprint: a sprint of work cards...
    Expected: Help text
    Grade: NEXT

36. nova-sprint version
    First 3 lines: nova-sprint devel linux/amd64 go1.26.6
    Expected: Version info
    Grade: NEXT

37. nova-sprint selftest
    First 3 lines: SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN
    Expected: Run self-tests
    Grade: URGENT (fails without push setup)

38. nova-sprint card
    First 3 lines: nova-sprint card REFUSED: wants one primary id, found none
    Expected: Show card details
    Grade: NEXT (requires arguments)

39. nova-sprint accept
    First 3 lines: nova-sprint accept REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Accept cards
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

40. nova-sprint ask
    First 3 lines: nova-sprint ask REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Ask for answers
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

41. nova-sprint ci
    First 3 lines: nova-sprint ci REFUSED: wants ids and one of --red, --green
    Expected: Record CI status
    Grade: NEXT (requires arguments)

42. nova-sprint collect
    First 3 lines: nova-sprint collect REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Collect friend data
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

43. nova-sprint cost
    First 3 lines: nova-sprint cost REFUSED: cost wants one of its verbs
    Expected: Cost commands
    Grade: NEXT (group verb)

44. nova-sprint demo
    First 3 lines: nova-sprint demo REFUSED: demo wants one of its verbs
    Expected: Demo commands
    Grade: NEXT (group verb)

45. nova-sprint funded
    First 3 lines: nova-sprint funded REFUSED: wants one word, the provider
    Expected: Record funding
    Grade: NEXT (requires arguments)

46. nova-sprint promoted
    First 3 lines: nova-sprint promoted REFUSED: wants --sha <merge sha>
    Expected: Record promotion
    Grade: NEXT (requires arguments)

47. nova-sprint answer
    First 3 lines: ANSWER OK rows=0 applied=0 would_apply=0
    Expected: Show answers
    Grade: NEXT

48. nova-sprint remind
    First 3 lines: nova-sprint remind REFUSED: wants exactly one form
    Expected: Set reminders
    Grade: NEXT (requires arguments)

49. nova-sprint resolve
    First 3 lines: nova-sprint resolve REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Resolve cards
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

50. nova-sprint wait
    First 3 lines: nova-sprint wait REFUSED: wants notification ids
    Expected: Wait for notifications
    Grade: NEXT (requires arguments)

51. nova-sprint adopt
    First 3 lines: nova-sprint adopt REFUSED: wants one <version|path>
    Expected: Adopt software
    Grade: NEXT (requires arguments)

52. nova-sprint backup
    First 3 lines: nova-sprint backup REFUSED: wants --out <dir> or --file <path>
    Expected: Create backup
    Grade: NEXT (requires arguments)

53. nova-sprint snapshot
    First 3 lines: nova-sprint snapshot REFUSED: wants --dir <dir>
    Expected: Create snapshot
    Grade: NEXT (requires arguments)

54. nova-sprint coordinator
    First 3 lines: nova-sprint coordinator REFUSED: wants one name
    Expected: Change coordinator
    Grade: NEXT (requires arguments)

55. nova-sprint fsck
    First 3 lines: nova-sprint fsck REFUSED: fsck wants one of its verbs
    Expected: File system check
    Grade: NEXT (group verb)

56. nova-sprint view
    First 3 lines: nova-sprint view REFUSED: view wants one of its verbs
    Expected: View commands
    Grade: NEXT (group verb)

57. nova-sprint unpin
    First 3 lines: nova-sprint unpin REFUSED: wants <id>...
    Expected: Unpin cards
    Grade: NEXT (requires arguments)

58. nova-sprint rank
    First 3 lines: nova-sprint rank REFUSED: wants ids and one of --score <n>
    Expected: Rank cards
    Grade: NEXT (requires arguments)

59. nova-sprint rebase
    First 3 lines: nova-sprint rebase REFUSED: wants --from <branch> and --to <branch>
    Expected: Rebase cards
    Grade: NEXT (requires arguments)

60. nova-sprint recut
    First 3 lines: nova-sprint recut REFUSED: wants one primary and --tier
    Expected: Recut cards
    Grade: NEXT (requires arguments)

61. nova-sprint relink
    First 3 lines: nova-sprint relink REFUSED: wants <old-id>... <new-id>
    Expected: Relink cards
    Grade: NEXT (requires arguments)

62. nova-sprint brief
    First 3 lines: nova-sprint brief REFUSED: wants one primary
    Expected: Edit brief
    Grade: NEXT (requires arguments)

63. nova-sprint move
    First 3 lines: nova-sprint move REFUSED: wants ids and --stream <s>
    Expected: Move cards
    Grade: NEXT (requires arguments)

64. nova-sprint merge
    First 3 lines: nova-sprint merge REFUSED: wants --stream <s>
    Expected: Merge cards
    Grade: NEXT (requires arguments)

65. nova-sprint land
    First 3 lines: nova-sprint land REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Land cards
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

66. nova-sprint verify-landed
    First 3 lines: VERIFY-UNRECORDED checked=0 unrecorded=0 unchecked=0
    Expected: Verify landed cards
    Grade: NEXT

67. nova-sprint landed
    First 3 lines: nova-sprint landed REFUSED: wants <card>... --sha <commit>
    Expected: Record landed card
    Grade: NEXT (requires arguments)

68. nova-sprint promote
    First 3 lines: nova-sprint promote: git symbolic-ref: fatal: not a git repository
    Expected: Promote branch
    Grade: URGENT (requires git repo)

69. nova-sprint resume
    First 3 lines: nova-sprint resume REFUSED: wants --stream <s>
    Expected: Resume stream
    Grade: NEXT (requires arguments)

70. nova-sprint hold
    First 3 lines: nova-sprint hold REFUSED: wants at least one name
    Expected: Hold entity
    Grade: NEXT (requires arguments)

71. nova-sprint unhold
    First 3 lines: nova-sprint unhold REFUSED: wants at least one name
    Expected: Unhold entity
    Grade: NEXT (requires arguments)

72. nova-sprint set
    First 3 lines: nova-sprint set REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Set configuration
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

73. nova-sprint start
    First 3 lines: nova-sprint start REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Start machine
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

74. nova-sprint stop
    First 3 lines: nova-sprint stop REFUSED: wants --reason <text> and --until
    Expected: Stop machine
    Grade: NEXT (requires arguments)

75. nova-sprint run
    First 3 lines: nova-sprint run REFUSED: a mem twin has no machine running between commands
    Expected: Run daemon
    Grade: NEXT (requires server setup)

76. nova-sprint take
    First 3 lines: nova-sprint take REFUSED: wants --as <member>
    Expected: Take cards
    Grade: NEXT (requires arguments)

77. nova-sprint finish
    First 3 lines: nova-sprint finish REFUSED: wants --as <member>
    Expected: Finish card
    Grade: NEXT (requires arguments)

78. nova-sprint progress
    First 3 lines: nova-sprint progress REFUSED: wants --as <worker>
    Expected: Record progress
    Grade: NEXT (requires arguments)

79. nova-sprint read
    First 3 lines: nova-sprint read REFUSED: wants --as <reader>
    Expected: Read cards
    Grade: NEXT (requires arguments)

80. nova-sprint rework
    First 3 lines: nova-sprint rework REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Rework cards
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

81. nova-sprint return
    First 3 lines: nova-sprint return REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Return cards
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

82. nova-sprint redo
    First 3 lines: nova-sprint redo REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Redo card
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

83. nova-sprint drop
    First 3 lines: nova-sprint drop REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Drop cards
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

84. nova-sprint priority
    First 3 lines: nova-sprint priority REFUSED: PUSH DOWN: boss has no push target recorded
    Expected: Set priority
    Grade: NEXT (requires coordinator setup; run: nova-sprint seat install --actor boss)

85. nova-sprint release
    First 3 lines: nova-sprint release REFUSED: wants sentinels or held cards and --reason
    Expected: Release cards
    Grade: NEXT (requires arguments)

86. nova-sprint preflight
    First 3 lines: nova-sprint preflight REFUSED: wants --brief-dir <dir>
    Expected: Preflight check
    Grade: NEXT (requires arguments)

87. nova-sprint quack
    First 3 lines: nova-sprint quack REFUSED: wants --streams, --count, --repo
    Expected: Quack cards
    Grade: NEXT (requires arguments)

## Readability (READ)
7/10 - Help text is comprehensive but verbose. Many commands refuse without arguments
with clear remediation messages. The --help output provides good guidance.

## Usability (USE)
6/10 - Read-only verbs work well with the in-memory twin. Write operations requiring
coordinator setup refuse with clear messages. Many commands require specific arguments
which are properly documented in help text.

## Summary
urgent=2 next=85

Commands that require coordinator push setup (and refuse with clear remediation): add, set,
start, land, accept, ask, collect, resolve, rework, return, redo, drop, priority, selftest.
Most other verbs require specific arguments but refuse with clear guidance.
