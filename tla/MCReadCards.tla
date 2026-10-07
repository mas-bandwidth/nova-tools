---------------------------- MODULE MCReadCards ----------------------------
\* The instance of ReadCards.tla TLC runs: the module as it is, its constants set by the
\* cfg (MCReadCards.cfg holds every invariant; MCReadCardsBrokenIgnoreSpent.cfg and
\* MCReadCardsBrokenReturnSpends.cfg are the reversed witnesses, tla/CASES.tsv).
EXTENDS ReadCards
=============================================================================
