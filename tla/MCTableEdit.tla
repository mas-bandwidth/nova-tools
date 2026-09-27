--------------------------- MODULE MCTableEdit ---------------------------
EXTENDS TableEdit
MCCols == {"a","b","n"}
MCMembers == {"m1","m2"}
MCTexts == {"x"}
MCNames == {"t","u","v"}
MCTaken == {"v"}
MCInitDef == [c \in MCCols |-> IF c = "n" THEN "text" ELSE "set"]
=============================================================================
