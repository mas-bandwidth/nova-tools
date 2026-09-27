------------------------ MODULE MCTableMachine ------------------------
EXTENDS TableMachine
MCTables == {"t1","t2"}
MCRows == {"r1","r2"}
MCColumns == {"c1","c2","c3"}
MCMembers == {"m1","m2","m3"}
MCWriters == {"w1","w2"}
MCScores == {1,2}
MCExternal == <<"external","external","external">>
MCSeed == {<<<<"t1","r1","c1">>,"m1",1>>, <<MCExternal,"m2",2>>}
MCOneTable == {"t1"}
MCOneRow == {"r1"}
MCTwoCols == {"c1","c2"}
MCOneMember == {"m1"}
MCSmallSeed == {<<<<"t1","r1","c1">>,"m1",1>>, <<MCExternal,"m1",2>>}
=============================================================================
