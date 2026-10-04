---------------------------- MODULE MCRouteIndex ----------------------------
\* The instance: two tiers of three entries each (pro names a route twice), seven
\* cards as model values (four flash, two pro, one flash card that pins a model;
\* the control makes one pro card a read, the reader tier pro), one redeal each;
\* the work cards of a tier are interchangeable (the symmetry).
EXTENDS RouteIndex, TLC

CONSTANTS ProCards

MCArr == [t \in {"flash", "pro"} |-> IF t = "flash" THEN <<"a", "b", "c">> ELSE <<"d", "e", "e">>]
MCTierOf == [c \in Cards |-> IF c \in ProCards THEN "pro" ELSE "flash"]
\* the mask (docs/SPEC-SPRINT.md, route-applies-to): the pro route "d" runs on
\* friends only, so a fleet-class pro card skips it; every other route applies
\* to every class. Every card's executor class is fleet.
MCMask == [r \in Routes |-> IF r = "d" THEN {"friends"} ELSE Executors]
MCClassOf == [c \in Cards |-> "fleet"]
\* a read card is no work card's twin: the reads are left out of the symmetry
MCSym == Permutations(Cards \ (ProCards \cup Pinned \cup Reads)) \cup Permutations(ProCards \ Reads)
=============================================================================
