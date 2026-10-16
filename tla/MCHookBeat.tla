---------------------------- MODULE MCHookBeat ----------------------------
EXTENDS HookBeat
\* Quiet cases use store-time activity and role-specific silence; legacy
\* cadence cases overapproximate issued-nonce compatibility and delivery.
\* The clock is unbounded; every age and count is bounded.
=============================================================================
