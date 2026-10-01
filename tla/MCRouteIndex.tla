---------------------------- MODULE MCRouteIndex ----------------------------
\* The instance: two tiers of three entries each (pro names a route twice), seven
\* cards as model values (four flash, two pro, one flash card that pins a model),
\* one redeal each; the cards of a tier are interchangeable (the symmetry).
EXTENDS RouteIndex, TLC

CONSTANTS ProCards

MCArr == [t \in {"flash", "pro"} |-> IF t = "flash" THEN <<"a", "b", "c">> ELSE <<"d", "e", "e">>]
MCTierOf == [c \in Cards |-> IF c \in ProCards THEN "pro" ELSE "flash"]
MCSym == Permutations(Cards \ (ProCards \cup Pinned)) \cup Permutations(ProCards)
=============================================================================
