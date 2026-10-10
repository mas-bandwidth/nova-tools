---------------------------- MODULE MCRouteIndex ----------------------------
\* The instance: two tiers of three entries each (pro names a route twice), seven
\* cards as model values (four flash, two pro, one flash card that pins a model;
\* the control makes one pro card a read, the reader tier pro), one redeal each;
\* the work cards of a tier are interchangeable (the symmetry).
EXTENDS RouteIndex, TLC

CONSTANTS ProCards

MCArr == [t \in {"flash", "pro"} |-> IF t = "flash" THEN <<"a", "b", "c">> ELSE <<"d", "e", "e">>]
MCRestricted == [c \in Cards |-> Routes \ {"a", "d"}]
MCCapable == [c \in Cards |-> Routes]
MCTierOf == [c \in Cards |-> IF c \in ProCards THEN "pro" ELSE "flash"]
\* a read card is no work card's twin: the reads are left out of the symmetry
MCSym == Permutations(Cards \ (ProCards \cup Pinned \cup Reads)) \cup Permutations(ProCards \ Reads)
=============================================================================
