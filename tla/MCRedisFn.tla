----------------------------- MODULE MCRedisFn -----------------------------
\* The TLC instances. Constants that are functions cannot be written in the
\* .cfg; they are defined here and substituted.
\*
\* The migration (MCRedisFn, MCRedisFnDeleteThenLoad, MCRedisFnHolderGone):
\* library old is on the store at build 1, which registers f and g. Loader a
\* carries old at build 2, which registers g alone; loader b carries new at
\* build 1, which registers f. b is refused for f until a has loaded.
\*
\* The rivals (MCRedisFnTwoDeployers, MCRedisFnOneDeployer, MCRedisFnLoadMissing,
\* MCRedisFnMissReplaces): a carries old at build 1 and b carries old at build
\* 2. In the last two a deploys and b runs LoadMissing once, on a store that
\* starts empty, so b can read the name free and find a's build there when it
\* loads.
EXTENDS RedisFn
MCLibs == {"old", "new"}
MCBuilds == {1, 2}
MCFuncs == {"f", "g"}
MCReg == [l \in MCLibs |-> [b \in MCBuilds |->
           IF l = "old" THEN (IF b = 1 THEN {"f", "g"} ELSE {"g"}) ELSE {"f"}]]
MCLoaders == {"a", "b"}
MCOnlyA == {"a"}
MCMigration == [p \in MCLoaders |->
                 IF p = "a" THEN [lib |-> "old", build |-> 2] ELSE [lib |-> "new", build |-> 1]]
MCRivals == [p \in MCLoaders |->
              IF p = "a" THEN [lib |-> "old", build |-> 1] ELSE [lib |-> "old", build |-> 2]]
MCInitStore == [l \in MCLibs |-> IF l = "old" THEN 1 ELSE NONE]
MCNoOne == {}
MCOnlyB == {"b"}
MCEmptyStore == [l \in MCLibs |-> NONE]

\* The deployer's build is where the store comes to rest (MCRedisFnLoadMissing:
\* a deploys old at build 1, b runs LoadMissing once with build 2).
MCDeployed == <>[](store["old"] = 1)

\* The migration ends with each library at the build its deployer carries.
MCMigrated == <>[](store = [l \in MCLibs |-> IF l = "old" THEN 2 ELSE 1])
=============================================================================
