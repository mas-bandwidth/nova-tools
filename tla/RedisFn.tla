------------------------------ MODULE RedisFn ------------------------------
(* The function libraries of one Redis under several loaders: nova-tools    *)
(* internal/redisfn (Check, Load, Ensure, LoadMissing).                     *)
(*                                                                          *)
(* The store holds, under each library name, one build of the library or    *)
(* none. A build registers a set of function names, and a function name     *)
(* belongs to one library: the store refuses a library that registers a     *)
(* name another library holds. A loader carries one build of one library.   *)
(*                                                                          *)
(* A loader outside Missers runs Ensure: it reads the store (Check), and    *)
(* when the store does not hold its build it loads it (Load). When the      *)
(* store refuses the load for a name another library holds, the loader      *)
(* reads the store again to name the holder. A deployer runs Ensure on      *)
(* every pass, for ever; any other Ensure loader runs it once.              *)
(*                                                                          *)
(* A loader in Missers runs LoadMissing, once: it reads the store, and only *)
(* when the store holds no build of its library it sends FUNCTION LOAD,     *)
(* without REPLACE, which the store refuses when a build of the library is  *)
(* there by then; a refusal for a name another library holds is an answer, *)
(* not a failure, and it looks for no holder. MissReplaces = TRUE is the    *)
(* witness of nova-tools #3620: its load is FUNCTION LOAD REPLACE.          *)
(*                                                                          *)
(* Atomic = TRUE is the code: FUNCTION LOAD REPLACE, one command, after     *)
(* which the store holds the new build whole or is as it was. Atomic =      *)
(* FALSE is the witness: FUNCTION DELETE and then FUNCTION LOAD.            *)
(*                                                                          *)
(* Left out: FUNCTION DELETE, FLUSH and RESTORE by hand; a restart of a     *)
(* store that persists nothing; a load whose reply is lost (the loader      *)
(* then knows neither outcome, and the store holds one of the two, whole);  *)
(* the text of a library (a build here is a name for it, and the functional *)
(* tests of internal/redisfn hold the text).                                *)
(*                                                                          *)
(* Written 2026-09-27 under Glenn's rule: TLA+ for every state machine.     *)
EXTENDS Naturals, FiniteSets

CONSTANTS
  Libs,        \* the library names
  Builds,      \* the builds a library can be at
  Funcs,       \* the function names
  Reg,         \* Reg[l][b]: the function names build b of library l registers
  Loaders,     \* the processes that load
  Carries,     \* Carries[p] = [lib |-> l, build |-> b]: what loader p was built with
  Deployers,   \* the loaders that run Ensure on every pass
  Missers,     \* the loaders that run LoadMissing, once
  InitStore,   \* the store at the start: InitStore[l] is a build, or NONE
  Atomic,      \* TRUE: FUNCTION LOAD REPLACE. FALSE: FUNCTION DELETE, then FUNCTION LOAD
  MissReplaces, \* TRUE: the witness, a LoadMissing whose load is FUNCTION LOAD REPLACE
  NONE         \* a model value: no build, no function, no library, nothing read

ASSUME Deployers \subseteq Loaders
ASSUME Missers \subseteq Loaders /\ Missers \cap Deployers = {}
ASSUME Atomic \in BOOLEAN /\ MissReplaces \in BOOLEAN

VARIABLES
  store,    \* store[l]: the build of library l the store holds, or NONE
  pc,       \* pc[p]: where loader p is in its Ensure or LoadMissing
  saw,      \* saw[p]: what p's read found ("same", "different", "absent"), NONE before it
  named,    \* named[p]: the function the store named when it refused p's load, or NONE
  holder,   \* holder[p]: the library p then found holding that function, or NONE
  ever      \* ever[l]: library l has been on the store (a ghost, for NoGap)

vars == <<store, pc, saw, named, holder, ever>>

Lib(p) == Carries[p].lib
Build(p) == Carries[p].build

(* The function names library l holds on the store s. *)
Holds(s, l) == IF s[l] = NONE THEN {} ELSE Reg[l][s[l]]

(* The function names of p's build that another library holds on the store. *)
Taken(p) == {f \in Reg[Lib(p)][Build(p)] : \E o \in Libs \ {Lib(p)} : f \in Holds(store, o)}

(* The libraries, other than p's, that hold function f on the store. *)
Holders(p, f) == {o \in Libs \ {Lib(p)} : f \in Holds(store, o)}

Init ==
  /\ store = InitStore
  /\ pc = [p \in Loaders |-> "idle"]
  /\ saw = [p \in Loaders |-> NONE]
  /\ named = [p \in Loaders |-> NONE]
  /\ holder = [p \in Loaders |-> NONE]
  /\ ever = [l \in Libs |-> InitStore[l] # NONE]

(* The first command of Ensure and of LoadMissing: FUNCTION LIST, one      *)
(* command. Ensure goes on to load unless the store holds its build;       *)
(* LoadMissing only when the store holds no build of its library.          *)
Read(p) ==
  /\ pc[p] = "idle"
  /\ saw' = [saw EXCEPT ![p] = IF store[Lib(p)] = NONE THEN "absent"
                               ELSE IF store[Lib(p)] = Build(p) THEN "same"
                               ELSE "different"]
  /\ pc' = [pc EXCEPT ![p] = IF p \in Missers
                               THEN (IF store[Lib(p)] = NONE THEN "load" ELSE "done")
                               ELSE (IF store[Lib(p)] = Build(p) THEN "done" ELSE "load")]
  /\ named' = [named EXCEPT ![p] = NONE]
  /\ holder' = [holder EXCEPT ![p] = NONE]
  /\ UNCHANGED <<store, ever>>

(* FUNCTION LOAD of p's build into the store as it is now: the build whole, *)
(* or a refusal that names one function and writes nothing.                *)
Put(p) ==
  /\ IF Taken(p) = {}
       THEN /\ store' = [store EXCEPT ![Lib(p)] = Build(p)]
            /\ ever' = [ever EXCEPT ![Lib(p)] = TRUE]
            /\ pc' = [pc EXCEPT ![p] = "done"]
            /\ UNCHANGED named
       ELSE /\ \E f \in Taken(p) : named' = [named EXCEPT ![p] = f]
            /\ pc' = [pc EXCEPT ![p] = "refused"]
            /\ UNCHANGED <<store, ever>>
  /\ UNCHANGED <<saw, holder>>

(* Load, the code: FUNCTION LOAD REPLACE. *)
Load(p) ==
  /\ pc[p] = "load"
  /\ p \notin Missers
  /\ Atomic
  /\ Put(p)

(* LoadMissing's load: FUNCTION LOAD, which the store refuses, writing      *)
(* nothing, when a build of the library is there by now (another loader    *)
(* put it there after the read: LoadMissing's Unchanged). A refusal for a  *)
(* name another library holds ends it as well (Skipped). The witness       *)
(* (MissReplaces) sends FUNCTION LOAD REPLACE, which takes the name.       *)
Miss(p) ==
  /\ pc[p] = "load"
  /\ p \in Missers
  /\ IF store[Lib(p)] # NONE /\ ~MissReplaces
       THEN /\ pc' = [pc EXCEPT ![p] = "done"]
            /\ UNCHANGED <<store, ever>>
       ELSE IF Taken(p) = {}
         THEN /\ store' = [store EXCEPT ![Lib(p)] = Build(p)]
              /\ ever' = [ever EXCEPT ![Lib(p)] = TRUE]
              /\ pc' = [pc EXCEPT ![p] = "done"]
         ELSE /\ pc' = [pc EXCEPT ![p] = "done"]
              /\ UNCHANGED <<store, ever>>
  /\ UNCHANGED <<saw, named, holder>>

(* The witness: FUNCTION DELETE first, and FUNCTION LOAD as a second command. *)
Delete(p) ==
  /\ pc[p] = "load"
  /\ p \notin Missers
  /\ ~Atomic
  /\ store' = [store EXCEPT ![Lib(p)] = NONE]
  /\ pc' = [pc EXCEPT ![p] = "deleted"]
  /\ UNCHANGED <<saw, named, holder, ever>>

LoadAfterDelete(p) ==
  /\ pc[p] = "deleted"
  /\ Put(p)

(* After a refusal: FUNCTION LIST, to name the library that holds the       *)
(* function. It is a command of its own, so the holder may be gone by then. *)
Find(p) ==
  /\ pc[p] = "refused"
  /\ IF Holders(p, named[p]) = {}
       THEN holder' = [holder EXCEPT ![p] = NONE]
       ELSE \E o \in Holders(p, named[p]) : holder' = [holder EXCEPT ![p] = o]
  /\ pc' = [pc EXCEPT ![p] = "failed"]
  /\ UNCHANGED <<store, saw, named, ever>>

(* A deployer's next pass. *)
Again(p) ==
  /\ p \in Deployers
  /\ pc[p] \in {"done", "failed"}
  /\ pc' = [pc EXCEPT ![p] = "idle"]
  /\ UNCHANGED <<store, saw, named, holder, ever>>

(* Every loader has ended and none runs again: the terminal stutter. *)
Rest ==
  /\ \A p \in Loaders : pc[p] \in {"done", "failed"} /\ p \notin Deployers
  /\ UNCHANGED vars

Step(p) == Read(p) \/ Load(p) \/ Miss(p) \/ Delete(p) \/ LoadAfterDelete(p) \/ Find(p) \/ Again(p)

Next == (\E p \in Loaders : Step(p)) \/ Rest

Spec == Init /\ [][Next]_vars /\ \A p \in Loaders : WF_vars(Step(p))

----------------------------------------------------------------------------
(* Invariants *)

(* NONE is compared and never put in a set with a build or a name: a model *)
(* value is unequal to every other value, and that is all that is asked.   *)
TypeOK ==
  /\ DOMAIN store = Libs
  /\ \A l \in Libs : store[l] = NONE \/ store[l] \in Builds
  /\ pc \in [Loaders -> {"idle", "load", "deleted", "refused", "done", "failed"}]
  /\ DOMAIN saw = Loaders /\ DOMAIN named = Loaders /\ DOMAIN holder = Loaders
  /\ \A p \in Loaders : saw[p] = NONE \/ saw[p] \in {"same", "different", "absent"}
  /\ \A p \in Loaders : named[p] = NONE \/ named[p] \in Funcs
  /\ \A p \in Loaders : holder[p] = NONE \/ holder[p] \in Libs
  /\ ever \in [Libs -> BOOLEAN]

(* A function name is held by one library at most. *)
OneHolder == \A f \in Funcs : Cardinality({l \in Libs : f \in Holds(store, l)}) <= 1

(* A library that has been on the store is on the store: there is no moment *)
(* at which a call of one of its functions finds no function. The code      *)
(* holds it because Load is one command; the witness (Atomic = FALSE) does  *)
(* not.                                                                     *)
NoGap == \A l \in Libs : ever[l] => store[l] # NONE

(* A witness, violated on purpose (MCRedisFnHolderGone): a refused loader   *)
(* always finds the holder. It does not, when another loader takes the      *)
(* function out of the holder between the refusal and the look, so the code *)
(* has an error for a function with no holder.                              *)
HolderFound == \A p \in Loaders : pc[p] = "failed" => holder[p] # NONE

----------------------------------------------------------------------------
(* Properties of steps *)

(* A refusal writes nothing. *)
RefusalWritesNothing ==
  [][\A p \in Loaders : (pc[p] # "refused" /\ pc'[p] = "refused") => store' = store]_vars

(* The holder a refused loader names held the function when it looked. *)
HolderHeld ==
  [][\A p \in Loaders : (pc[p] = "refused" /\ pc'[p] = "failed" /\ holder'[p] # NONE)
        => named[p] \in Holds(store, holder'[p])]_vars

(* LoadMissing never replaces: its load leaves a build of its library that  *)
(* is on the store as it is (nova-tools #3620). The witness, MissReplaces,  *)
(* violates it (MCRedisFnMissReplaces).                                      *)
MissNeverReplaces ==
  [][\A p \in Missers : (pc[p] = "load" /\ pc'[p] = "done" /\ store[Lib(p)] # NONE)
        => store'[Lib(p)] = store[Lib(p)]]_vars

----------------------------------------------------------------------------
(* Liveness *)

(* Every library comes to rest at one build, or at none. It holds with one  *)
(* deployer to a library (MCRedisFn, MCRedisFnOneDeployer) and fails with   *)
(* two that carry different builds of one library (MCRedisFnTwoDeployers):  *)
(* each replaces the other's build on every pass, for ever.                 *)
Settles == \A l \in Libs : (\E b \in Builds : <>[](store[l] = b)) \/ <>[](store[l] = NONE)

=============================================================================
