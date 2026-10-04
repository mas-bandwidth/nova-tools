----------------------------- MODULE SecretsSeat -----------------------------
\* nova-secrets' seats, as state: the keys, the store's rules and seat files,
\* the verbs that write them, the gate that lets a change reach the store's
\* HEAD, and the placements of a value on a machine. It models
\* internal/secrets/keygen.go (RunKeygen), seat.go (RunSeatAdd,
\* seatAddValidate, seatAddRuleIsFree), seal.go (RunSeal, sealDecrypt,
\* sealCarry), seatinject.go (RunSeatInject, seatInjectTarget,
\* seatInjectCompose), place.go (RunPlace, writeReceipt) and gate.go
\* (RunGate), and docs/SPEC-SECRETS.md: "The model", "gate", "place and
\* placed", "seal", "seat add" and "seat inject".
\*
\* THE STATE.
\*   keyHome    per seat, the bench whose keygen made its key, or None. One
\*              key per seat (SPEC-SECRETS "The model": one age keypair per
\*              AI per bench); keygen refuses a key file that exists.
\*   keyAt      per key, where its private half is: a bench, Console (the
\*              recovery key, off every bench) or Store (written into the
\*              store, where every clone holds it). The public half is not
\*              state: it travels in keygen's receipt and is printed freely.
\*   wc, head   the store's working copy and its HEAD, each a tree: rule[s],
\*              the recipients of the .sops.yaml rule for <s>.yaml ({} for
\*              no rule); file[s], the seat file; other, a changed file that
\*              is neither .sops.yaml, README.md nor a seat file. A file is
\*              present or not, its recipients (its sops metadata), its
\*              values by name (every value of a file is sealed under one
\*              data key that only its recipients recover), the names held
\*              in the clear, the names whose entry an outside edit wrote,
\*              and the blob id of its bytes.
\*   pr         the tree a pull request proposes, or NoPR.
\*   objs       the git objects of every seat file version written, so a
\*              blob id names its bytes.
\*   unnamed    every <<key, blob>> a verb decrypted with a key the file
\*              does not name.
\*   lost       every <<seat, name>> whose value a verb's write dropped.
\*   receipts   per target machine and name, the receipt place wrote: the
\*              seat file and its identity (the blob id of the bytes place
\*              read), with three ghosts: the value placed (at its placed
\*              path, the one place a value rests in the clear by design,
\*              written over ssh at mode 0600), whether the seat file
\*              differed from HEAD's when place read it, and whether HEAD
\*              held the same blob then. The
\*              OK line place prints carries the receipt's fields and no
\*              others, so the receipt stands for that line too.
\* A value in the clear otherwise exists only on a child's stdin (the value
\* read for seal, the plaintext sops hands to the encrypt, the ssh child's
\* stdin): never argv, a file or a line. The model carries no such channel;
\* a verb's write is one step.
\*
\* THE ACTIONS. The verbs:
\*   Keygen      a new seat's key on its bench (keygen.go RunKeygen).
\*   SeatAdd     a NEW seat's first values, re-sealed out of a seat this
\*               key opens, with the seat's rule written first; it refuses
\*               an existing seat file (seat.go:104), an existing rule
\*               (seat.go:110), --from naming the seat itself (seat.go:177),
\*               a private key as --pub (seat.go:183), the recovery key as
\*               --pub (seat.go:97), a source this key cannot open
\*               (seat.go:118) and a name the source lacks. It commits
\*               nothing: the working copy holds the change.
\*   Seal        one value into a seat file through a pull request, on a
\*               clean store (sealCarry.preflight). An existing file is
\*               decrypted first, so the key is one of its recipients; an
\*               absent file starts from nothing (seal.go:553-558), so a
\*               rule is all it needs. sops encrypts to the rule.
\*   SeatInject  values into an EXISTING seat from a source this key opens,
\*               through a pull request: every name the target holds is
\*               re-sealed from the source and a name the source lacks is
\*               refused (seatinject.go:28-34, seatInjectCompose); the
\*               target's recipients must equal its rule (seatinject.go:267)
\*               and hold no value in the clear (seatinject.go:290).
\*   Place       one value to a machine over the ssh child's stdin, and a
\*               receipt naming the blob of the bytes read (place.go:32-41,
\*               :235, :268, :290).
\*   Propose     the working copy's change committed and proposed.
\*   Gate        the gate's verdict over the proposal against HEAD
\*               (GateVerdict); on APPROVE the proposal merges and HEAD and
\*               the working copy move to it (gate.go RunGate; the squash
\*               merge and the pull). A verdict is <<"APPROVE", 0>> or
\*               <<"REFUSE", r>>, r the number of the check that refused
\*               (gate.go numbers them 1 to 5 in its comments); the printed
\*               line's rule=<n> is the position of the .sops.yaml rule the
\*               refusal is about, 0 when it is about none.
\* The outside events:
\*   EditPlain   a hand writes a value in the clear into a seat file.
\*   Remove      a hand removes a seat file.
\*   EditOther   a hand changes another file of the store.
\*   HandSeal    a hand runs a sops pipe with a key that opens the file
\*               (only when the constant HandSeal is TRUE).
\*   Revert      the working copy goes back to HEAD.
\* The gate's check 4 (a recipient a pull request adds is vouched for by the
\* machines registry) is dormant: no registry is given, as the APPROVE line
\* says with machines=-.
\*
\* THE INVARIANTS AND LIVENESS.
\*   ValueNeverInTheClearAtRest  no seat file at HEAD holds a value in the
\*     clear, and no receipt holds anything derived from a value
\*     (seal.go SealOptions, place.go:32-41; SPEC-SECRETS "Nothing derived
\*     from a value").
\*   SeatFileNeverReplaced  no verb's write drops a value a seat file held
\*     (seat.go:104: "a rewrite of a seat file drops every value it holds").
\*   OnlyRecipientsOpen  a key decrypts a file only when the file names it, so
\*     inject never copies a target's other values unread
\*     (seatinject.go:28-34).
\*   PrivateKeyStaysHome  a seat's private key is only on the bench whose
\*     keygen made it, the recovery key only on the console (seat.go:38-40,
\*     :183, keygen.go:60; SPEC-SECRETS "The model").
\*   ReceiptNamesCommittedBlob  a receipt's blob names the bytes whose value
\*     was placed, and is the blob HEAD held whenever the seat file held no
\*     uncommitted change (place.go:73-78; SPEC-SECRETS "place and placed":
\*     head "is no claim that this commit holds blob").
\*   GateRefusesEveryRuleBreak  a proposal that breaks a check is REFUSE
\*     naming one it breaks, and APPROVE only when none is broken; Gate moves
\*     HEAD only on APPROVE (gate.go:25-31).
\*   NoHandRoad  no entry of a seat file at HEAD was written by an outside
\*     edit: every value reaches the store by seat add, seal or inject
\*     (seatinject.go:17-22; SPEC-SECRETS rule 13).
\*   EveryProposalJudged  a proposal is judged (liveness, under weak fairness
\*     of Gate; checked on the one-name instance MCSecretsSeatLive, the
\*     invariants on the two-name one).
\*   ReceiptBlobAtHead  the stricter claim that a receipt's blob is always
\*     HEAD's; place does not make it, and the reach case shows where.
\*
\* Broken = "none" is the design. The reversed witnesses:
\*   "seataddreplaces"     seat add treats an existing seat as new: it writes
\*                         over the file and its rule (SeatFileNeverReplaced)
\*   "seataddfilecheck"    seat add without seat.go:104's file check only:
\*                         the rule check alone refuses, since every seat
\*                         file has a rule (passes)
\*   "injectcopiesunread"  inject keeps the target's other values by
\*                         decrypting the target (OnlyRecipientsOpen)
\*   "receiptbyvalue"      a receipt names the placement by a hash of the
\*                         value (ValueNeverInTheClearAtRest)
\*   "pubtakesprivate"     seat add takes a private key as --pub and writes
\*                         it into the store (PrivateKeyStaysHome)
\* The reach cases, each a property the code does not keep, run on the design:
\*   HandSeal = TRUE       a hand sops pipe writes a sealed entry the gate
\*                         cannot tell from a verb's (NoHandRoad fails)
\*   ReceiptBlobAtHead     place from a seat file with an uncommitted change
\*                         (after seat add) records a blob HEAD lacks

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Seats, Machines, Names, Vals, Recovery, SrcSeat, SrcHome,
          MaxWrites, Targets, Broken, HandSeal, None, NoPR

Console == "console"
Store == "store"
Keys == Seats \cup {Recovery}
Places == Machines \cup {Console, Store}
Checks == {1, 2, 3, 5}
Approve == <<"APPROVE", 0>>

VARIABLES keyHome, keyAt, wc, head, pr, objs, unnamed, lost, receipts
vars == <<keyHome, keyAt, wc, head, pr, objs, unnamed, lost, receipts>>

NoFile == [present |-> FALSE, recips |-> {}, val |-> [n \in Names |-> None],
           plain |-> {}, hand |-> {}, blob |-> 0]
Files == [present : BOOLEAN, recips : SUBSET Keys, val : [Names -> Vals \cup {None}],
          plain : SUBSET Names, hand : SUBSET Names, blob : 0..MaxWrites]
Trees == [rule : [Seats -> SUBSET Keys], file : [Seats -> Files], other : BOOLEAN]
Receipts == [file : Seats, id : ({"blob"} \X (1..MaxWrites)) \cup ({"digest"} \X Vals),
             val : Vals, dirty : BOOLEAN, atHead : BOOLEAN]

TypeOK ==
  /\ keyHome \in [Seats -> Machines \cup {None}]
  /\ keyAt \in [Keys -> SUBSET Places]
  /\ wc \in Trees /\ head \in Trees
  /\ pr \in Trees \cup {NoPR}
  /\ objs \in Seq(Files) /\ Len(objs) <= MaxWrites
  /\ unnamed \subseteq Keys \X (1..MaxWrites)
  /\ lost \subseteq Seats \X Names
  /\ receipts \in [Targets -> [Names -> Receipts \cup {None}]]

\* A rule's recipients as the gate and seat add hold them (invariants.go
\* ruleRecipientsProblem): two keys, one the recovery key.
RuleShape(rs) == Cardinality(rs) = 2 /\ Recovery \in rs

\* A key is on a bench, where a verb runs with it.
OnBench(k) == keyAt[k] \cap Machines # {}

CanWrite == Len(objs) < MaxWrites
NewBlob == Len(objs) + 1
Clean == wc = head /\ pr = NoPR

\* A decrypt of blob b with key k, as the ghost unnamed records it: nothing
\* when the file names k.
Unnamed(k, b) == IF k \in objs[b].recips THEN {} ELSE {<<k, b>>}

\* The names a write from old to new drops.
Drops(s, old, new) == {<<s, n>> : n \in {x \in Names : old.val[x] # None /\ new.val[x] = None}}

Init ==
  LET f == [present |-> TRUE, recips |-> {SrcSeat, Recovery},
            val |-> [n \in Names |-> CHOOSE v \in Vals : TRUE],
            plain |-> {}, hand |-> {}, blob |-> 1]
      t == [rule |-> [s \in Seats |-> IF s = SrcSeat THEN {SrcSeat, Recovery} ELSE {}],
            file |-> [s \in Seats |-> IF s = SrcSeat THEN f ELSE NoFile],
            other |-> FALSE]
  IN /\ keyHome = [s \in Seats |-> IF s = SrcSeat THEN SrcHome ELSE None]
     /\ keyAt = [k \in Keys |-> IF k = SrcSeat THEN {SrcHome}
                                ELSE IF k = Recovery THEN {Console} ELSE {}]
     /\ wc = t /\ head = t /\ pr = NoPR
     /\ objs = <<f>> /\ unnamed = {} /\ lost = {}
     /\ receipts = [m \in Targets |-> [n \in Names |-> None]]

-----------------------------------------------------------------------------
\* The verbs.

Keygen(s, m) ==
  /\ keyHome[s] = None
  /\ keyHome' = [keyHome EXCEPT ![s] = m]
  /\ keyAt' = [keyAt EXCEPT ![s] = {m}]
  /\ UNCHANGED <<wc, head, pr, objs, unnamed, lost, receipts>>

\* arg is what --pub is handed: <<"pub", x>> or <<"priv", x>> for key x.
SeatAdd(k, s, from, arg, ns) ==
  /\ pr = NoPR /\ OnBench(k) /\ CanWrite
  /\ s # from
  /\ arg[1] = "pub" \/ Broken = "pubtakesprivate"
  /\ arg[2] # Recovery
  /\ keyHome[arg[2]] # None
  /\ ~wc.file[s].present \/ Broken \in {"seataddreplaces", "seataddfilecheck"}
  /\ wc.rule[s] = {} \/ Broken = "seataddreplaces"
  /\ wc.file[from].present
  /\ k \in wc.file[from].recips
  /\ ns # {} /\ \A n \in ns : wc.file[from].val[n] # None
  /\ LET src == wc.file[from]
         rs == {arg[2], Recovery}
         f == [present |-> TRUE, recips |-> rs,
               val |-> [n \in Names |-> IF n \in ns THEN src.val[n] ELSE None],
               plain |-> {}, hand |-> {}, blob |-> NewBlob]
     IN /\ wc' = [wc EXCEPT !.rule[s] = rs, !.file[s] = f]
        /\ objs' = Append(objs, f)
        /\ unnamed' = unnamed \cup Unnamed(k, src.blob)
        /\ lost' = lost \cup Drops(s, wc.file[s], f)
        /\ keyAt' = IF arg[1] = "priv" THEN [keyAt EXCEPT ![arg[2]] = @ \cup {Store}] ELSE keyAt
  /\ UNCHANGED <<keyHome, head, pr, receipts>>

Seal(k, s, n, v) ==
  /\ Clean /\ OnBench(k) /\ CanWrite
  /\ head.rule[s] # {}
  /\ head.file[s].present => k \in head.file[s].recips
  /\ LET old == head.file[s]
         f == [present |-> TRUE, recips |-> head.rule[s],
               val |-> [old.val EXCEPT ![n] = v],
               plain |-> {}, hand |-> old.hand \ {n}, blob |-> NewBlob]
     IN /\ pr' = [head EXCEPT !.file[s] = f]
        /\ objs' = Append(objs, f)
        /\ unnamed' = IF old.present THEN unnamed \cup Unnamed(k, old.blob) ELSE unnamed
        /\ lost' = lost \cup Drops(s, old, f)
  /\ UNCHANGED <<keyHome, keyAt, wc, head, receipts>>

SeatInject(k, s, from, ns) ==
  /\ Clean /\ OnBench(k) /\ CanWrite
  /\ s # from
  /\ head.file[s].present /\ head.file[from].present
  /\ RuleShape(head.rule[s]) /\ head.file[s].recips = head.rule[s]
  /\ head.file[s].plain = {}
  /\ k \in head.file[from].recips
  /\ ns # {}
  /\ LET tgt == head.file[s]
         src == head.file[from]
         held == {x \in Names : tgt.val[x] # None}
         carry == IF Broken = "injectcopiesunread" THEN ns ELSE held \cup ns
         unread == held \ carry
         f == [present |-> TRUE, recips |-> tgt.recips,
               val |-> [x \in Names |-> IF x \in carry THEN src.val[x] ELSE tgt.val[x]],
               plain |-> {}, hand |-> tgt.hand \ carry, blob |-> NewBlob]
     IN /\ \A x \in carry : src.val[x] # None
        /\ pr' = [head EXCEPT !.file[s] = f]
        /\ objs' = Append(objs, f)
        /\ unnamed' = unnamed \cup Unnamed(k, src.blob)
                             \cup (IF unread # {} THEN Unnamed(k, tgt.blob) ELSE {})
        /\ lost' = lost \cup Drops(s, tgt, f)
  /\ UNCHANGED <<keyHome, keyAt, wc, head, receipts>>

Place(k, s, n, t) ==
  /\ OnBench(k) /\ t \in Targets
  /\ wc.file[s].present /\ k \in wc.file[s].recips /\ wc.file[s].val[n] # None
  /\ LET f == wc.file[s]
         r == [file |-> s,
               id |-> IF Broken = "receiptbyvalue" THEN <<"digest", f.val[n]>> ELSE <<"blob", f.blob>>,
               val |-> f.val[n], dirty |-> f # head.file[s], atHead |-> f.blob = head.file[s].blob]
     IN /\ receipts' = [receipts EXCEPT ![t][n] = r]
        /\ unnamed' = unnamed \cup Unnamed(k, f.blob)
  /\ UNCHANGED <<keyHome, keyAt, wc, head, pr, objs, lost>>

Propose ==
  /\ pr = NoPR /\ wc # head
  /\ pr' = wc
  /\ UNCHANGED <<keyHome, keyAt, wc, head, objs, unnamed, lost, receipts>>

\* The gate's checks over base B and head H, each as gate.go states it.
Breaks(r, B, H) ==
  CASE r = 3 -> H.other
    [] r = 1 -> /\ H.rule # B.rule
                /\ \E s \in Seats : H.rule[s] # {} /\ (~RuleShape(H.rule[s]) \/ ~H.file[s].present)
    [] r = 5 -> \E s \in Seats : B.file[s].present /\ ~H.file[s].present
    [] r = 2 -> \E s \in Seats : /\ H.file[s] # B.file[s] /\ H.file[s].present
                                 /\ \/ H.rule[s] = {}
                                    \/ H.file[s].recips # H.rule[s]
                                    \/ H.file[s].plain # {}
BrokenChecks(B, H) == {r \in Checks : Breaks(r, B, H)}

\* The verdict in the order RunGate runs its checks: 3, 1, 5, 2.
GateVerdict(B, H) ==
  IF Breaks(3, B, H) THEN <<"REFUSE", 3>>
  ELSE IF Breaks(1, B, H) THEN <<"REFUSE", 1>>
  ELSE IF Breaks(5, B, H) THEN <<"REFUSE", 5>>
  ELSE IF Breaks(2, B, H) THEN <<"REFUSE", 2>>
  ELSE Approve

Gate ==
  /\ pr # NoPR
  /\ IF GateVerdict(head, pr) = Approve
       THEN head' = pr /\ wc' = pr
       ELSE UNCHANGED <<head, wc>>
  /\ pr' = NoPR
  /\ UNCHANGED <<keyHome, keyAt, objs, unnamed, lost, receipts>>

-----------------------------------------------------------------------------
\* The outside events. EditPlain writes the entry n in the clear; which value
\* it writes is immaterial to every check, so it keeps the value there or
\* takes the first.

EditPlain(s, n) ==
  /\ pr = NoPR /\ CanWrite /\ wc.file[s].present
  /\ LET old == wc.file[s]
         v == IF old.val[n] = None THEN CHOOSE x \in Vals : TRUE ELSE old.val[n]
         f == [old EXCEPT !.val[n] = v, !.plain = @ \cup {n}, !.hand = @ \cup {n}, !.blob = NewBlob]
     IN /\ wc' = [wc EXCEPT !.file[s] = f]
        /\ objs' = Append(objs, f)
  /\ UNCHANGED <<keyHome, keyAt, head, pr, unnamed, lost, receipts>>

Remove(s) ==
  /\ pr = NoPR /\ wc.file[s].present
  /\ wc' = [wc EXCEPT !.file[s] = NoFile]
  /\ UNCHANGED <<keyHome, keyAt, head, pr, objs, unnamed, lost, receipts>>

EditOther ==
  /\ pr = NoPR /\ ~wc.other
  /\ wc' = [wc EXCEPT !.other = TRUE]
  /\ UNCHANGED <<keyHome, keyAt, head, pr, objs, unnamed, lost, receipts>>

HandSealing(k, s, n, v) ==
  /\ HandSeal
  /\ pr = NoPR /\ OnBench(k) /\ CanWrite
  /\ wc.rule[s] # {}
  /\ wc.file[s].present => k \in wc.file[s].recips
  /\ LET old == wc.file[s]
         f == [present |-> TRUE, recips |-> wc.rule[s],
               val |-> [old.val EXCEPT ![n] = v],
               plain |-> {}, hand |-> old.hand \cup {n}, blob |-> NewBlob]
     IN /\ wc' = [wc EXCEPT !.file[s] = f]
        /\ objs' = Append(objs, f)
        /\ unnamed' = IF old.present THEN unnamed \cup Unnamed(k, old.blob) ELSE unnamed
  /\ UNCHANGED <<keyHome, keyAt, head, pr, lost, receipts>>

Revert ==
  /\ pr = NoPR /\ wc # head
  /\ wc' = head
  /\ UNCHANGED <<keyHome, keyAt, head, pr, objs, unnamed, lost, receipts>>

-----------------------------------------------------------------------------

PubArgs == {<<"pub", x>> : x \in Keys} \cup {<<"priv", x>> : x \in Keys}
NameSets == (SUBSET Names) \ {{}}

Next ==
  \/ \E s \in Seats, m \in Machines : Keygen(s, m)
  \/ \E k \in Keys, s, from \in Seats, arg \in PubArgs, ns \in NameSets : SeatAdd(k, s, from, arg, ns)
  \/ \E k \in Keys, s \in Seats, n \in Names, v \in Vals : Seal(k, s, n, v)
  \/ \E k \in Keys, s, from \in Seats, ns \in NameSets : SeatInject(k, s, from, ns)
  \/ \E k \in Keys, s \in Seats, n \in Names, t \in Machines : Place(k, s, n, t)
  \/ Propose
  \/ Gate
  \/ \E s \in Seats, n \in Names : EditPlain(s, n)
  \/ \E s \in Seats : Remove(s)
  \/ EditOther
  \/ \E k \in Keys, s \in Seats, n \in Names, v \in Vals : HandSealing(k, s, n, v)
  \/ Revert

Spec == Init /\ [][Next]_vars /\ WF_vars(Gate)

-----------------------------------------------------------------------------
\* The invariants.

ValueNeverInTheClearAtRest ==
  /\ \A s \in Seats : head.file[s].plain = {}
  /\ \A m \in Targets, n \in Names :
       receipts[m][n] # None => receipts[m][n].id[1] = "blob"

SeatFileNeverReplaced == lost = {}

OnlyRecipientsOpen == unnamed = {}

PrivateKeyStaysHome ==
  /\ \A s \in Seats : keyAt[s] \subseteq (IF keyHome[s] = None THEN {} ELSE {keyHome[s]})
  /\ keyAt[Recovery] = {Console}

ReceiptNamesCommittedBlob ==
  \A m \in Targets, n \in Names :
    LET r == receipts[m][n] IN
    r # None =>
      /\ r.id[1] = "blob"
      /\ objs[r.id[2]].val[n] = r.val
      /\ ~r.dirty => r.atHead

GateRefusesEveryRuleBreak ==
  /\ pr # NoPR =>
       LET v == GateVerdict(head, pr) IN
       /\ (v = Approve) <=> (BrokenChecks(head, pr) = {})
       /\ v # Approve => v[2] \in BrokenChecks(head, pr)

NoHandRoad == \A s \in Seats : head.file[s].hand = {}

EveryProposalJudged == [](pr # NoPR => <>(pr = NoPR))

\* The reach case's probe: place from a seat file with an uncommitted change
\* writes a receipt whose blob HEAD does not hold.
ReceiptBlobAtHead ==
  \A m \in Targets, n \in Names :
    receipts[m][n] # None => receipts[m][n].atHead

=============================================================================
