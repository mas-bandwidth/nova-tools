----------------------------- MODULE MCLoadCache -----------------------------
\* The small instances of LoadCache: one table of two records (each table is its
\* own: one lock, one stream), two loads at once, four writes (a record's or a
\* display cell's) and one loss of the last writes; each configuration names the
\* rule it breaks (Broken; "none" for none).
EXTENDS LoadCache
=============================================================================
