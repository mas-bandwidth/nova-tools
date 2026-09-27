--------------------------- MODULE MCTableEdit ---------------------------
EXTENDS TableEdit
MCCols == {"a","p","n"}
MCMembers == {"m1","m2"}
MCOneMember == {"m1"}
MCTexts == {"x"}
MCNames == {"t","u","v"}
MCTaken == {"v"}
MCInitDef == [c \in MCCols |-> IF c = "n" THEN "text" ELSE IF c = "p" THEN "formula" ELSE "set"]
=============================================================================
