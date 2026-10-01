------------------------------- MODULE MCLand -------------------------------
\* The instance: three cards as model values, one clear (epochs 0 and 1), at
\* most four outside events (accepts, returns, another lander, a clear, the
\* base moving, a crash) in any order; the order of the cards matters (the
\* queue), so there is no symmetry.
EXTENDS Land, TLC
=============================================================================
