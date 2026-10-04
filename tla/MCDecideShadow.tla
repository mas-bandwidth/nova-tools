--------------------------- MODULE MCDecideShadow ----------------------------
EXTENDS DecideShadow
Identities == [i \in Items |-> IF i = "i1" \/ i = "i2" THEN "tuple-a" ELSE "tuple-b"]
=============================================================================
