------------------------------- MODULE MCLand -------------------------------
\* The instance: three cards as model values of up to two attempts, one clear (epochs 0 and 1), at
\* most four outside events (accepts, returns, reworks, another lander, a clear, the
\* base moving, a crash) in any order; the order of the cards matters (the
\* queue), so there is no symmetry.
EXTENDS Land, TLC
=============================================================================
