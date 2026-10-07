----------------------- MODULE MCEpochMemberTable -----------------------
EXTENDS EpochMemberTable
MCDimensions == {"t1","t2"}
MCEpochs == {1,2}
MCPhysicalTables == MCDimensions \X MCEpochs
MCRows == {"r1","r2"}
MCColumns == {"c1","c2","c3"}
MCMembers == {"m1","m2","m3"}
MCWriters == {"w1","w2"}
MCScores == {1,2}
MCMemberEpoch == [m \in MCMembers |-> IF m="m2" THEN 2 ELSE 1]
MCExternal == <<<<"external",0>>,"external","external">>
MCNoPlace == <<<<"none",0>>,"none","none">>
MCSeed == {<<<<<<"t1",1>>,"r1","c1">>,"m1",1>>, <<MCExternal,"m2",2>>}
MCOneDimension == {"t1"}
MCSmallTables == MCOneDimension \X MCEpochs
MCOneRow == {"r1"}
MCTwoColumns == {"c1","c2"}
MCTwoMembers == {"m1","m2"}
MCSmallEpoch == [m \in MCTwoMembers |-> IF m="m2" THEN 2 ELSE 1]
=============================================================================
