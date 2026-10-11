----------------------------- MODULE MCConfigApply ----------------------------
EXTENDS ConfigApply
\* Each friend's beat host, chosen by TLC: both friends beat to m1.
MCBeat == [f \in Friends |-> m1]
=============================================================================
