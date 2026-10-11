---------------------------- MODULE MCHook ----------------------------
EXTENDS Hook
MCPushes == {"bus", "sprint"}
MCHolders == {"session", "sprintd"}
MCHolderOf == [p \in MCPushes |-> IF p = "bus" THEN "session" ELSE "sprintd"]
MCFriendPushes == {"bus", "work"}
MCFriendHolders == {"daemon"}
MCFriendHolderOf == [p \in MCFriendPushes |-> "daemon"]
=============================================================================
