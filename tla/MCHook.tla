---------------------------- MODULE MCHook ----------------------------
EXTENDS Hook
\* The seat's instance: a session's hook holds the bus push as coordinator, a
\* daemon (nova-sprint hook) the sprint push, and the seat's own friend daemon
\* holds the bus and work pushes as friend.
MCPushes == {"bus", "sprint", "work"}
MCRoles == {"coordinator", "friend"}
MCNeedOf == [r \in MCRoles |-> IF r = "coordinator" THEN {"bus", "sprint"} ELSE {"bus", "work"}]
MCHolders == {"session", "sprintd", "daemon"}
MCRoleOf == [h \in MCHolders |-> IF h = "daemon" THEN "friend" ELSE "coordinator"]
MCHeldOf == [h \in MCHolders |-> CASE h = "session" -> {"bus"}
                                   [] h = "sprintd" -> {"sprint"}
                                   [] OTHER -> {"bus", "work"}]
MCSessionHolders == {"session"}
\* A friend's instance: one daemon holds both her pushes.
MCFriendPushes == {"bus", "work"}
MCFriendRoles == {"friend"}
MCFriendNeedOf == [r \in MCFriendRoles |-> {"bus", "work"}]
MCFriendHolders == {"daemon"}
MCFriendRoleOf == [h \in MCFriendHolders |-> "friend"]
MCFriendHeldOf == [h \in MCFriendHolders |-> {"bus", "work"}]
=============================================================================
