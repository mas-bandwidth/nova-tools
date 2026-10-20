---------------------- MODULE MCTableRetention ----------------------
\* The TLC instance: three epochs, three operation ids, two request digests,
\* two records per epoch and three change events.
EXTENDS TableRetention
MCEpochs == {0, 1, 2}
MCOpIDs == {"a", "b", "c"}
MCDigests == {"d1", "d2"}
MCBound == 2
MCStreamBound == 3
=============================================================================
