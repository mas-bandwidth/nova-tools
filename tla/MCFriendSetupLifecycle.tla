---------------------- MODULE MCFriendSetupLifecycle ----------------------
EXTENDS FriendSetupLifecycle
MCSelection == [p \in Actors |-> CHOOSE s \in Selections : TRUE]
MCCommands == [s \in 1..Steps |-> s]
MCDifferentSelection == [p \in Actors |-> p]
=============================================================================
