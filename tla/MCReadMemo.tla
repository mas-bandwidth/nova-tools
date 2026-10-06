------------------------------ MODULE MCReadMemo ------------------------------
\* The instances of ReadMemo.tla (tla/CASES.tsv, group readmemo): two heads, two
\* bases, three readers, a pro card (two ok reads), three attempts: enough for a
\* twin at its old head, a rework at an unchanged head, a new base under the
\* same head, and readers that disagree at a head.
EXTENDS ReadMemo

MCHeads == {"h1", "h2"}
MCBases == {"b1", "b2"}
MCReaders == {"r1", "r2", "r3"}
=============================================================================
