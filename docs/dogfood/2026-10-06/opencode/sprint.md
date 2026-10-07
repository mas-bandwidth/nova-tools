# nova-sprint dogfood report

Tested against mem:sprint.twin with actor=freddy

1. nova-sprint init --readers reader-a,reader-b --members m1
   OK tables=work,readers,merge,fleet view=sprint readers=reader-a,reader-b
   MOVED m1 added, down until it beats
   URGENT

2. nova-sprint where
   SPRINT TABLE coordinator freddy
   STOPPED
   work | waiting | ready | working | review | merging | landed | cost | per landed
   URGENT

3. nova-sprint help
   OK prints all verb groups with flags
   URGENT

4. nova-sprint help ack
   REFUSED wants notification ids and --reason <text>
   URGENT

5. nova-sprint help preflight
   REFUSED wants --brief-dir <dir>
   URGENT

6. nova-sprint help resolve
   REFUSED wants [<id>...]
   URGENT

7. nova-sprint help release
   REFUSED wants (<sentinel or held card>... | <selector> [--dry-run]) --reason <text>
   URGENT

8. nova-sprint help release check
   REFUSED wants [--json] [--streams <glob>] [--check <name>]...
   URGENT

9. nova-sprint help start
   REFUSED PUSH DOWN: freddy has no push target recorded
   URGENT

10. nova-sprint help stop
    REFUSED wants --reason <text> --until <time or duration>
    URGENT

11. nova-sprint help run
    REFUSED wants [--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]
    URGENT

12. nova-sprint help tick
    REFUSED wants [--answer-rules] [--idle-alarm] [--shadow]
    URGENT

13. nova-sprint selftest --dir /tmp
    FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN
    URGENT

14. nova-sprint selftest land
    REFUSED wants --binary <path>
    URGENT

15. nova-sprint help goal
    REFUSED unknown verb goal; run: nova-sprint goal -h
    URGENT

16. nova-sprint help goal show
    OK prints goal status
    URGENT

17. nova-sprint goal drop
    REFUSED wants <name>
    URGENT

18. nova-sprint help goal set
    REFUSED wants <name> [--file <path>] [--to file:<path>]
    URGENT

19. nova-sprint help take
    REFUSED wants --as <member> [<card>@<gen>...] [--epoch <n>] [--max <n>]
    URGENT

20. nova-sprint help finish
    REFUSED wants --as <member> <card>@<gen>... --epoch <n> (--head <commit> | --failed)
    URGENT

21. nova-sprint help progress
    REFUSED wants --as <worker> <card>[@<gen>]... --epoch <n>
    URGENT

22. nova-sprint help ask
    REFUSED wants [<id>... | --group <id> [--expect <n>]] [--stream <s>]
    URGENT

23. nova-sprint help queue
    REFUSED wants --as <reader|member> | --stream <s>
    URGENT

24. nova-sprint help read
    REFUSED wants --as <reader> (--begin | --ok | --broken) [<card>...] --epoch <n>
    URGENT

25. nova-sprint help accept
    REFUSED wants (<id>... [--heavy --evidence <path> --reason <text>] | --stream <s> | --read-ok | --group <id> [--expect <n>])
    URGENT

26. nova-sprint help rework
    REFUSED wants (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--fix <text>]
    URGENT

27. nova-sprint help return
    REFUSED wants (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--reason <text>]
    URGENT

28. nova-sprint help redo
    REFUSED wants <card>... [--stream <s>]
    URGENT

29. nova-sprint help drop
    REFUSED wants (<id>... | --stream <s> --col <state> | --group <id> [--expect <n>] | <selector> [--dry-run]) --reason <text>
    URGENT

30. nova-sprint help priority
    REFUSED wants <id>... | (<id>... | --stream <s>) (--blocker | --critical | --high | --normal | --low) --reason <text>
    URGENT

31. nova-sprint help unpin
    REFUSED wants (<id>... | --stream <s>) --reason <text> [--dry-run]
    URGENT

32. nova-sprint help rebase
    REFUSED wants --from <branch> --to <branch> [--repo-dir <clone>] [--dry-run]
    URGENT

33. nova-sprint help rank
    REFUSED wants (<id>... | <selector> [--dry-run]) (--score <n> | --first | --before <id>)
    URGENT

34. nova-sprint help relink
    REFUSED wants <old-id>[,<old-id>...] <new-id> [--reason <text>]
    URGENT

35. nova-sprint help recut
    REFUSED wants <id> (--tier <flash|pro|heavy|frontier> | --brief-file <path> [--rules <file>])
    URGENT

36. nova-sprint help brief
    REFUSED wants <id> (--brief <text> | --brief-file <path>) [--rules <file>]
    URGENT

37. nova-sprint help move
    REFUSED wants <id>... --stream <s>
    URGENT

38. nova-sprint help merge
    REFUSED wants --stream <s> [--batch <n>]
    URGENT

39. nova-sprint help land
    REFUSED wants --stream <s>... [--repo-dir <clone>] [--base <branch>] [--check <command>]
    URGENT

40. nova-sprint help verify-landed
    REFUSED wants --stream <s>... [--repo-dir <clone>] [--base <branch>]
    URGENT

41. nova-sprint help landed
    REFUSED wants <id>... --sha <commit> --reason <text>
    URGENT

42. nova-sprint help snapshot
    REFUSED wants (--dir <dir> [--keep <n>] [--every <duration>] | --restore-drill <file>)
    URGENT

43. nova-sprint help backup
    REFUSED wants (--out <dir> [--part-bytes <n>] | --file <path> [--dry-run])
    URGENT

44. nova-sprint help demo
    REFUSED unknown verb demo; run: nova-sprint demo -h
    URGENT

45. nova-sprint help demo load
    REFUSED wants <backup.xz part>... [--sha256 <hex>]
    URGENT

46. nova-sprint help demo stop
    REFUSED wants [--dir <dir>]
    URGENT

47. nova-sprint help promote
    REFUSED wants [--every <duration>] [--landings <n>] [--branch <name>] [--repo-dir <clone>]
    URGENT

48. nova-sprint help resume
    REFUSED wants --stream <s> [--did <text>] [--answers <note>]
    URGENT

49. nova-sprint help hold
    REFUSED wants <member|reader|friend|stream>... --reason <text> [--return] [--dry-run]
    URGENT

50. nova-sprint help unhold
    REFUSED wants <member|reader|friend|stream>... [--reason <text>] [--dry-run]
    URGENT

51. nova-sprint help fleet
    REFUSED unknown verb fleet; run: nova-sprint fleet -h
    URGENT

52. nova-sprint help fleet beat
    REFUSED wants <member> [--load <percent>]
    URGENT

53. nova-sprint help fleet up
    REFUSED wants <member> [--width <n> | --width 0]
    URGENT

54. nova-sprint help fleet down
    REFUSED wants <member>
    URGENT

55. nova-sprint help fleet sync
    REFUSED wants [--check] [--pg <dsn>]
    URGENT

56. nova-sprint help fleet level
    REFUSED: freddy has no push target recorded
    URGENT

57. nova-sprint help friend
    REFUSED unknown verb friend; run: nova-sprint friend -h
    URGENT

58. nova-sprint help friend sync
    REFUSED wants [--pg <dsn>] [--root <dir>]
    URGENT

59. nova-sprint help collect
    REFUSED: freddy has no push target recorded
    URGENT

60. nova-sprint help friend beat
    REFUSED wants <friend> [--working <n>] [--queue <n>]
    URGENT

61. nova-sprint help friend down
    REFUSED wants <friend> [--reason <text>] [--until <RFC3339>]
    URGENT

62. nova-sprint help friend up
    REFUSED wants <friend> [--width <n>]
    URGENT

63. nova-sprint help friend cards
    REFUSED wants <friend> [--json]
    URGENT

64. nova-sprint help friend take
    REFUSED wants <friend> (<id>... | --all-unstarted) [--reason <text>]
    URGENT

65. nova-sprint help friend give
    REFUSED wants <friend> <id>... [--reason <text>]
    URGENT

66. nova-sprint help friend level
    REFUSED: freddy has no push target recorded
    URGENT

67. nova-sprint help friend health
    REFUSED wants <friend> (--state up|asleep|down ...)
    URGENT

68. nova-sprint help friend clean
    REFUSED wants [--pg <dsn> | --file <path>] [--root <dir>]
    URGENT

69. nova-sprint help friend reconcile
    REFUSED wants <friend> [--root <dir>]
    URGENT

70. nova-sprint help lane
    REFUSED unknown verb lane; run: nova-sprint lane -h
    URGENT

71. nova-sprint help lane take
    REFUSED wants <kind> --machine <m> --as <worker>
    URGENT

72. nova-sprint help lane give
    REFUSED wants <kind> --machine <m> --as <worker>
    URGENT

73. nova-sprint help lane list
    OK lists machines with holders and queues
    URGENT

74. nova-sprint help reader
    REFUSED unknown verb reader; run: nova-sprint reader -h
    URGENT

75. nova-sprint help reader add
    REFUSED wants <reader>... [--tiers <flash[,pro,heavy,frontier]|all|default>]
    URGENT

76. nova-sprint help reader set
    REFUSED wants <reader>... --tiers <flash[,pro,heavy,frontier]|all|default>
    URGENT

77. nova-sprint help reader away
    REFUSED wants <reader>...
    URGENT

78. nova-sprint help reader up
    REFUSED wants <reader>...
    URGENT

79. nova-sprint help reader remove
    REFUSED wants <reader>...
    URGENT

80. nova-sprint help reader retire
    REFUSED wants <reader>...
    URGENT

81. nova-sprint help stream
    REFUSED unknown verb stream; run: nova-sprint stream -h
    URGENT

82. nova-sprint help stream remove
    REFUSED wants <stream>...
    URGENT

83. nova-sprint help stream archive
    REFUSED wants <stream>...
    URGENT

84. nova-sprint help stream unarchive
    REFUSED wants <stream>...
    URGENT

85. nova-sprint help stream set
    REFUSED wants <stream>... [--read-tier <flash|pro|heavy|default>]
    URGENT

86. nova-sprint help set
    REFUSED: freddy has no push target recorded
    URGENT

87. nova-sprint help promoted
    REFUSED wants --sha <merge sha> [--answers <note>]
    URGENT

88. nova-sprint help merge-window
    REFUSED unknown verb merge-window; run: nova-sprint merge-window -h
    URGENT

89. nova-sprint help merge-window open
    REFUSED: freddy has no push target recorded
    URGENT

90. nova-sprint help funded
    REFUSED wants <provider> --reason <text>
    URGENT

91. nova-sprint help cost
    REFUSED unknown verb cost; run: nova-sprint cost -h
    URGENT

92. nova-sprint help cost reconcile
    REFUSED wants [--dry-run] [--json]
    URGENT

93. nova-sprint help cost reprice
    REFUSED wants [--route <r>]... [--since <RFC3339>] [--dry-run]
    URGENT

94. nova-sprint help ci
    REFUSED wants <id>... (--red | --green) --epoch <n>
    URGENT

95. nova-sprint help wait
    REFUSED wants (<note>[,<note>]... | --group <id> [--expect <n>])
    URGENT

96. nova-sprint help remind
    REFUSED wants (--in <duration> | --at <time>) --note <text>
    URGENT

97. nova-sprint help ack
    REFUSED wants <note>[,<note>]... --reason <text>
    URGENT

98. nova-sprint help answer
    REFUSED wants [--dry-run] [--bar <p>] [--every <duration>]
    URGENT

99. nova-sprint help inbox
    OK prints inbox status
    URGENT

100. nova-sprint help card
     REFUSED wants <id> [--brief | --fields] [--at-epoch <n>]
     URGENT

101. nova-sprint help needs
     OK prints cards needing work
     URGENT

102. nova-sprint help streams
     OK prints stream count
     URGENT

103. nova-sprint help held
     OK prints held cards count
     URGENT

104. nova-sprint help sentinels
     OK prints sentinels count
     URGENT

105. nova-sprint help sentinel
     REFUSED unknown verb sentinel; run: nova-sprint sentinel -h
     URGENT

106. nova-sprint help sentinel set
     REFUSED wants <id> --needs <a,b>
     URGENT

107. nova-sprint help bases
     OK prints base count
     URGENT

108. nova-sprint help log
     OK prints log entries for member
     URGENT

109. nova-sprint help check
     OK prints violations
     URGENT

110. nova-sprint help repair
     REFUSED: freddy has no push target recorded
     URGENT

111. nova-sprint help watch
     REFUSED wants --wake [--every <duration>] [--state <file>]
     URGENT

112. nova-sprint help seat
     REFUSED unknown verb seat; run: nova-sprint seat -h
     URGENT

113. nova-sprint help seat check
     REFUSED: freddy has no push target recorded
     URGENT

114. nova-sprint help machinery
     OK prints machine status
     URGENT

115. nova-sprint help where
     OK prints sprint table
     URGENT

116. nova-sprint help dashboard
     REFUSED wants [--listen <address:port>[,<address:port>...] | none]
     URGENT

117. nova-sprint help handover
     OK prints handover status
     URGENT

118. nova-sprint help view
     REFUSED unknown verb view; run: nova-sprint view -h
     URGENT

119. nova-sprint help view coordinator
     OK prints coordinator view
     URGENT

120. nova-sprint help view cards
     OK prints cards in view
     URGENT

121. nova-sprint help view worker
     REFUSED wants --as <member|friend>
     URGENT

122. nova-sprint help fsck
     REFUSED unknown verb fsck; run: nova-sprint fsck -h
     URGENT

123. nova-sprint help fsck seat
     REFUSED wants [--pg <host:port or postgres:// URI>]
     URGENT

124. nova-sprint help routes
     OK prints route count
     URGENT

125. nova-sprint help rules
     OK prints rules status
     URGENT

126. nova-sprint help stats
     OK prints stats
     URGENT

127. nova-sprint help play
     REFUSED: freddy has no push target recorded
     URGENT

128. nova-sprint help clear
     REFUSED wants --confirm sprint
     URGENT

129. nova-sprint help teardown
     REFUSED wants --confirm sprint
     URGENT

130. nova-sprint help live
     OK prints server info
     URGENT

131. nova-sprint help adopt
     REFUSED wants <version|path> --source <checkout> --inventory <file>
     URGENT

132. nova-sprint help server
     REFUSED unknown verb server; run: nova-sprint server -h
     URGENT

133. nova-sprint help server switch
     REFUSED wants [<binary>] [--rollback] [--window <duration>]
     URGENT

134. nova-sprint help install
     REFUSED wants <server|member|seat-push|friend-sync|table>
     URGENT

135. nova-sprint help uninstall
     REFUSED wants <server|member|seat-push|friend-sync|table>
     URGENT

136. nova-sprint help units
     REFUSED wants --check [--dir <dir>]
     URGENT

137. nova-sprint help coordinator
     REFUSED wants <name> --reason <text>
     URGENT

138. nova-sprint version
     OK prints version
     URGENT

---
Verdict: LAND
Head: eceefa563cba4edabbdc0d123a396c3f3d9106eb