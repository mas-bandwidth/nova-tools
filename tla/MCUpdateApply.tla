---------------------------- MODULE MCUpdateApply ----------------------------
\* The instance of UpdateApply. The two updaters of a concurrent
\* configuration are interchangeable (the symmetry): every constant of the
\* model treats them alike, so TLC explores each behavior once up to their
\* permutation.
EXTENDS UpdateApply, TLC

UpdatersSym == Permutations(Updaters)
=============================================================================
