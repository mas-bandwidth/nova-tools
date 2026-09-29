----------------------- MODULE MCBatchMemberTable -----------------------
EXTENDS BatchMemberTable
MCDimensions == {"t1"}
MCEpochs == {1,2}
MCTables == MCDimensions \X MCEpochs
MCRows == {"r1","r2"}
MCColumns == {"c1","c2"}
MCMembers == {"m1","m2","m3"}
MCWriters == {"w1"}
MCScores == {1,2}
MCMaxRevision2 == 2
MCMemberEpoch == [m \in MCMembers |-> 1]
MCExternal == <<<<"external",0>>,"external","external">>
MCNoPlace == <<<<"none",0>>,"none","none">>
MCBatchTable == <<"t1",1>>
MCFrom == <<MCBatchTable,"r1","c1">>
MCMove == <<MCBatchTable,"r2","c1">>
MCCreate == <<MCBatchTable,"r2","c2">>
MCGuard == <<MCBatchTable,"r1","c2">>
MCSeed == {<<MCFrom,"m1",1>>,<<MCGuard,"m3",2>>}
MCSecondMemberEpoch == [m \in MCMembers |-> IF m="m3" THEN 2 ELSE 1]
MCSecondSeed == {<<MCFrom,"m1",1>>}
=============================================================================
