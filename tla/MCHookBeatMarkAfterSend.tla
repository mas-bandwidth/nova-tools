--------------------- MODULE MCHookBeatMarkAfterSend ---------------------
EXTENDS HookBeat
\* The negative Tell/Crash/Mark witness keeps two names and one server,
\* but only the initial hook epoch. Both names can go silent and unhook,
\* producing two changes without a rehook cycle. A sent tell lost before
\* its separate mark reuses its once-key, suppressing the later change.
\* The property and LiveSpec fairness are unchanged. Positive safety and
\* liveness cases still use the unconstrained HookBeat instance.
SingleEpoch == \A n \in Names : hooks[n] = 1
=============================================================================
