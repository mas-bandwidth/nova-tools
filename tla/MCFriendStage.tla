---------------------------- MODULE MCFriendStage ----------------------------
EXTENDS FriendStage
\* Two cards on one repository (one judgment for both while it stands) and a
\* third on another.
MCRepoOf == [c \in Cards |-> IF c = "c3" THEN "r2" ELSE "r1"]
=============================================================================
