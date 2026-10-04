---------------------------- MODULE SecretsSeat ----------------------------
\* The store's secrets seats: key generation, seat add, seal, seat inject,
\* placement receipts, and gate rule enforcement.
\* Models internal/secrets (seat.go:1-25, :81-175; seal.go:1-12, :101-190;
\* place.go:1-20, :32-40, :73-94; gate.go:25-31, :106-206; seatinject.go:1-20,
\* :108-130, :302-363; keygen.go:40-105; invariants.go:38-133) and
\* docs/SPEC-SECRETS.md ("Nothing derived from a value", rules 1 to 8).
\*
\* THE STATE.
\*   seats             the store's seats (a seat file, its recipients: the seat's
\*                     own key and the declared recovery key)
\*   keys              the keys and which machine holds each (a private key never
\*                     leaves the bench that made it; seat.go:1-25)
\*   sealed            the values each file seals (names, sealed under one data key)
\*   clear             where a value is in the clear (only a process's stdin, never
\*                     argv, a file or a line: seal.go:1-12)
\*   placements        placements (a receipt naming the sealed blob at the store's
\*                     HEAD and the machine: place.go:1-20)
\*   gateVerdict       the gate's verdict over a diff (gate.go:25-31: APPROVE or
\*                     REFUSE rule=<n>)
\*   keyHome           ghost: the machine where each seat's private key was generated
\*   headBlob          ghost: the git blob id of each seat file at the store's HEAD
\*   committedBlobs    ghost: all committed blob ids generated for each seat
\*   seatValueOrigin   ghost: origin verb of each value in each seat
\*   diffRuleBreak     ghost: pending diff rule break (0 if clean, 1..5 if violated)
\*   prevSeats         ghost: store seats before the last transition
\*   prevSealed        ghost: sealed values before the last transition
\*   last              ghost: last transition tag and arguments
\*
\* THE ACTIONS.
\*   Keygen            a new seat's key on its bench; the public half travels,
\*                     the private never (keygen.go:40-105, seat.go:38-40)
\*   SeatAdd           a NEW seat's first values out of a seat this machine opens;
\*                     refuses an existing seat file, a --from naming the seat itself,
\*                     the recovery key as a seat key (seat.go:81-175)
\*   Seal              one value into a seat file: needs the target's own key
\*                     (seal.go:101-190)
\*   SeatInject        an EXISTING seat gets values from a source this key opens,
\*                     re-sealed from the source; refuses a name the source lacks
\*                     (seatinject.go:1-20, :302-363)
\*   Place             copies one value to a machine over ssh stdin, writes a receipt
\*                     by blob id (place.go:1-20, :32-40)
\*   Gate              approve or refuse a store diff by rule (gate.go:25-31, :106-206)
\*   OutsideEdit       a hand edit of a seat file, the store's HEAD moving
\*
\* THE INVARIANTS AND LIVENESS.
\*   ValueNeverInTheClearAtRest: no value exists unsealed in any file or printed
\*     line (seal.go:1-12, place.go:1-6).
\*   SeatFileNeverReplaced: seat add never writes over an existing file
\*     (seat.go:102).
\*   OnlyRecipientsOpen: a file's values are readable only by its recipients' keys,
\*     so inject can never copy a target's other values unread
\*     (seatinject.go:14-20).
\*   PrivateKeyStaysHome: a private key is never on a machine other than the one
\*     that made it (seat.go:38-40).
\*   ReceiptNamesCommittedBlob: a placement receipt names a blob the store's HEAD
\*     holds (place.go:10-17).
\*   GateRefusesEveryRuleBreak: a diff that breaks a rule is REFUSE with the rule
\*     number, APPROVE only when none is broken (gate.go:25-31).
\*   NoHandRoad: every value that reaches a seat does so by a verb (seat add, seal,
\*     inject), never by an outside edit the model admits as a legal path
\*     (seat.go:1-25, seatinject.go:17-22).
\*
\* REVERSED WITNESSES.
\*   BrokenSeatAddReplaces: seat add writes over an existing seat file
\*     (SeatFileNeverReplaced fails).
\*   BrokenInjectCopiesUnread: seat inject copies target's existing sealed values
\*     unread without target's key (OnlyRecipientsOpen fails).
\*   BrokenReceiptByValue: placement receipt records a hash of the value instead
\*     of the committed blob id (ReceiptNamesCommittedBlob fails).

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Machines, Seats, InitSeats, Names, Values, Broken

None == "none"

PrivKey(s) == [type |-> "priv", seat |-> s]
PubKey(s) == [type |-> "pub", seat |-> s]
RecoveryKey == [type |-> "pub", seat |-> "recovery"]

PrivKeys == {PrivKey(s) : s \in Seats}
PubKeys == {PubKey(s) : s \in Seats}
AllKeys == PubKeys \cup {RecoveryKey}

Blobs == {<<s, v>> : s \in Seats, v \in 1..5}
ValueHash(val) == [type |-> "valhash", val |-> val]

VARIABLES
  seats,
  keys,
  sealed,
  clear,
  placements,
  gateVerdict,
  keyHome,
  headBlob,
  committedBlobs,
  seatValueOrigin,
  diffRuleBreak,
  prevSeats,
  prevSealed,
  last

vars == <<seats, keys, sealed, clear, placements, gateVerdict,
          keyHome, headBlob, committedBlobs, seatValueOrigin,
          diffRuleBreak, prevSeats, prevSealed, last>>

TypeOK ==
  /\ seats \in [Seats -> [exists: BOOLEAN, recipients: SUBSET AllKeys]]
  /\ keys \in [Machines -> SUBSET PrivKeys]
  /\ keyHome \in [Seats -> Machines \cup {None}]
  /\ sealed \in [Seats -> [Names -> Values \cup {None}]]
  /\ clear \in SUBSET (Values \times {"stdin", "argv", "file", "line"})
  /\ headBlob \in [Seats -> Blobs \cup {None}]
  /\ committedBlobs \in [Seats -> SUBSET Blobs]
  /\ seatValueOrigin \in [Seats -> [Names -> {"Init", "SeatAdd", "Seal", "SeatInject", "OutsideEdit", None}]]
  /\ diffRuleBreak \in 0..5
  /\ gateVerdict \in [status: {"APPROVE", "REFUSE", "NONE"}, rule: 0..5]

RootMachine == CHOOSE m \in Machines : TRUE
FirstName == CHOOSE n \in Names : TRUE
FirstValue == CHOOSE v \in Values : TRUE

Init ==
  /\ seats = [s \in Seats |->
       IF s \in InitSeats
       THEN [exists |-> TRUE, recipients |-> {PubKey(s), RecoveryKey}]
       ELSE [exists |-> FALSE, recipients |-> {}]]
  /\ keys = [m \in Machines |->
       IF m = RootMachine
       THEN {PrivKey(s) : s \in InitSeats}
       ELSE {}]
  /\ keyHome = [s \in Seats |-> IF s \in InitSeats THEN RootMachine ELSE None]
  /\ sealed = [s \in Seats |-> [n \in Names |->
       IF s \in InitSeats /\ n = FirstName THEN FirstValue ELSE None]]
  /\ clear = {}
  /\ headBlob = [s \in Seats |-> IF s \in InitSeats THEN <<s, 1>> ELSE None]
  /\ committedBlobs = [s \in Seats |-> IF s \in InitSeats THEN {<<s, 1>>} ELSE {}]
  /\ seatValueOrigin = [s \in Seats |-> [n \in Names |->
       IF s \in InitSeats /\ n = FirstName THEN "Init" ELSE None]]
  /\ diffRuleBreak = 0
  /\ gateVerdict = [status |-> "APPROVE", rule |-> 0]
  /\ placements = {}
  /\ prevSeats = seats
  /\ prevSealed = sealed
  /\ last = <<"Init">>

Keygen(m, s) ==
  /\ keyHome[s] = None
  /\ ~seats[s].exists
  /\ keys' = [keys EXCEPT ![m] = @ \cup {PrivKey(s)}]
  /\ keyHome' = [keyHome EXCEPT ![s] = m]
  /\ prevSeats' = seats
  /\ prevSealed' = sealed
  /\ last' = <<"Keygen", m, s>>
  /\ UNCHANGED <<seats, sealed, clear, placements, gateVerdict,
                 headBlob, committedBlobs, seatValueOrigin, diffRuleBreak>>

SeatAdd(m, as, from, names, pub) ==
  /\ names # {}
  /\ from # as
  /\ pub # RecoveryKey
  /\ pub = PubKey(as)
  /\ seats[from].exists
  /\ PrivKey(from) \in keys[m]
  /\ \A n \in names : sealed[from][n] # None
  /\ (~seats[as].exists \/ Broken = "BrokenSeatAddReplaces")
  /\ seats' = [seats EXCEPT ![as] = [exists |-> TRUE, recipients |-> {pub, RecoveryKey}]]
  /\ sealed' = [sealed EXCEPT ![as] = [n \in Names |-> IF n \in names THEN sealed[from][n] ELSE None]]
  /\ clear' = clear \cup {<<sealed[from][n], "stdin">> : n \in names}
  /\ LET nextBlob == <<as, 2>> IN
     /\ headBlob' = [headBlob EXCEPT ![as] = nextBlob]
     /\ committedBlobs' = [committedBlobs EXCEPT ![as] = @ \cup {nextBlob}]
  /\ seatValueOrigin' = [seatValueOrigin EXCEPT ![as] = [n \in Names |-> IF n \in names THEN "SeatAdd" ELSE None]]
  /\ diffRuleBreak' = 0
  /\ prevSeats' = seats
  /\ prevSealed' = sealed
  /\ last' = <<"SeatAdd", as, from, names>>
  /\ UNCHANGED <<keys, keyHome, placements, gateVerdict>>

Seal(m, s, name, val) ==
  /\ seats[s].exists
  /\ PrivKey(s) \in keys[m]
  /\ sealed' = [sealed EXCEPT ![s][name] = val]
  /\ clear' = clear \cup {<<val, "stdin">>}
  /\ LET nextBlob == <<s, 3>> IN
     /\ headBlob' = [headBlob EXCEPT ![s] = nextBlob]
     /\ committedBlobs' = [committedBlobs EXCEPT ![s] = @ \cup {nextBlob}]
  /\ seatValueOrigin' = [seatValueOrigin EXCEPT ![s][name] = "Seal"]
  /\ diffRuleBreak' = 0
  /\ prevSeats' = seats
  /\ prevSealed' = sealed
  /\ last' = <<"Seal", s, name>>
  /\ UNCHANGED <<seats, keys, keyHome, placements, gateVerdict>>

SeatInject(m, as, from, names) ==
  /\ names # {}
  /\ as # from
  /\ seats[as].exists
  /\ seats[from].exists
  /\ PrivKey(from) \in keys[m]
  /\ \A n \in names : sealed[from][n] # None
  /\ (\A k \in Names : sealed[as][k] # None => sealed[from][k] # None)
     \/ (PrivKey(as) \in keys[m])
     \/ (Broken = "BrokenInjectCopiesUnread")
  /\ LET newSealed == [k \in Names |->
           IF k \in names THEN sealed[from][k]
           ELSE IF sealed[from][k] # None THEN sealed[from][k]
           ELSE IF Broken = "BrokenInjectCopiesUnread" THEN sealed[as][k]
           ELSE None]
     IN
     /\ sealed' = [sealed EXCEPT ![as] = newSealed]
     /\ seatValueOrigin' = [seatValueOrigin EXCEPT ![as] = [k \in Names |->
          IF newSealed[k] # None THEN "SeatInject" ELSE None]]
  /\ clear' = clear \cup {<<sealed[from][n], "stdin">> : n \in names}
  /\ LET nextBlob == <<as, 4>> IN
     /\ headBlob' = [headBlob EXCEPT ![as] = nextBlob]
     /\ committedBlobs' = [committedBlobs EXCEPT ![as] = @ \cup {nextBlob}]
  /\ diffRuleBreak' = 0
  /\ prevSeats' = seats
  /\ prevSealed' = sealed
  /\ last' = <<"SeatInject", m, as, names, from>>
  /\ UNCHANGED <<seats, keys, keyHome, placements, gateVerdict>>

Place(m, dest, s, name) ==
  /\ seats[s].exists
  /\ PrivKey(s) \in keys[m]
  /\ sealed[s][name] # None
  /\ IF Broken = "BrokenReceiptByValue"
     THEN /\ clear' = clear \cup {<<sealed[s][name], "stdin">>, <<sealed[s][name], "file">>}
          /\ LET rBlob == ValueHash(sealed[s][name]) IN
             placements' = placements \cup {[machine |-> dest, seat |-> s, secret |-> name, blob |-> rBlob]}
     ELSE /\ clear' = clear \cup {<<sealed[s][name], "stdin">>}
          /\ LET rBlob == headBlob[s] IN
             placements' = placements \cup {[machine |-> dest, seat |-> s, secret |-> name, blob |-> rBlob]}
  /\ prevSeats' = seats
  /\ prevSealed' = sealed
  /\ last' = <<"Place", dest, s, name>>
  /\ UNCHANGED <<seats, keys, keyHome, sealed, headBlob, committedBlobs, seatValueOrigin, diffRuleBreak, gateVerdict>>

OutsideEdit(s, name, val, ruleBreak) ==
  /\ seats[s].exists
  /\ ruleBreak \in 1..5
  /\ IF ruleBreak = 1
     THEN seats' = [seats EXCEPT ![s].recipients = {PubKey(s)}]
     ELSE IF ruleBreak = 5
     THEN seats' = [seats EXCEPT ![s].exists = FALSE]
     ELSE seats' = seats
  /\ IF ruleBreak = 2
     THEN /\ clear' = clear \cup {<<val, "file">>}
          /\ sealed' = [sealed EXCEPT ![s][name] = val]
     ELSE /\ clear' = clear
          /\ sealed' = [sealed EXCEPT ![s][name] = val]
  /\ seatValueOrigin' = [seatValueOrigin EXCEPT ![s][name] = "OutsideEdit"]
  /\ LET nextBlob == <<s, 5>> IN
     /\ headBlob' = [headBlob EXCEPT ![s] = nextBlob]
     /\ committedBlobs' = [committedBlobs EXCEPT ![s] = @ \cup {nextBlob}]
  /\ diffRuleBreak' = ruleBreak
  /\ gateVerdict' = [status |-> "NONE", rule |-> 0]
  /\ prevSeats' = seats
  /\ prevSealed' = sealed
  /\ last' = <<"OutsideEdit", s, name>>
  /\ UNCHANGED <<keys, keyHome, placements>>

Gate ==
  /\ gateVerdict.status = "NONE" \/ diffRuleBreak # 0
  /\ IF diffRuleBreak = 0
     THEN gateVerdict' = [status |-> "APPROVE", rule |-> 0]
     ELSE gateVerdict' = [status |-> "REFUSE", rule |-> diffRuleBreak]
  /\ prevSeats' = seats
  /\ prevSealed' = sealed
  /\ last' = <<"Gate", gateVerdict'.status, gateVerdict'.rule>>
  /\ UNCHANGED <<seats, keys, keyHome, sealed, clear, placements, headBlob, committedBlobs, seatValueOrigin, diffRuleBreak>>

Next ==
  \/ \E m \in Machines, s \in Seats : Keygen(m, s)
  \/ \E m \in Machines, as, from \in Seats, names \in SUBSET Names, pub \in PubKeys :
        SeatAdd(m, as, from, names, pub)
  \/ \E m \in Machines, s \in Seats, name \in Names, val \in Values :
        Seal(m, s, name, val)
  \/ \E m \in Machines, as, from \in Seats, names \in SUBSET Names :
        SeatInject(m, as, from, names)
  \/ \E m, dest \in Machines, s \in Seats, name \in Names :
        Place(m, dest, s, name)
  \/ Gate
  \/ \E s \in Seats, name \in Names, val \in Values, ruleBreak \in {1, 2, 5} :
        OutsideEdit(s, name, val, ruleBreak)

Spec == Init /\ [][Next]_vars

ValueNeverInTheClearAtRest ==
  \A item \in clear : item[2] = "stdin"

SeatFileNeverReplaced ==
  last[1] = "SeatAdd" => ~prevSeats[last[2]].exists

OnlyRecipientsOpen ==
  last[1] = "SeatInject" =>
    LET m == last[2]
        as == last[3]
        names == last[4]
        from == last[5]
    IN \A k \in Names :
         (sealed[as][k] # None /\ prevSealed[as][k] # None /\ k \notin names) =>
           (prevSealed[from][k] # None \/ PrivKey(as) \in keys[m])

PrivateKeyStaysHome ==
  \A m \in Machines, s \in Seats :
    PrivKey(s) \in keys[m] => keyHome[s] = m

ReceiptNamesCommittedBlob ==
  \A p \in placements : p.blob \in committedBlobs[p.seat]

GateRefusesEveryRuleBreak ==
  gateVerdict.status # "NONE" =>
    /\ (gateVerdict.status = "APPROVE" <=> diffRuleBreak = 0)
    /\ (gateVerdict.status = "REFUSE" <=> diffRuleBreak # 0)
    /\ (diffRuleBreak # 0 => gateVerdict.rule = diffRuleBreak)

NoHandRoad ==
  \A s \in Seats, n \in Names :
    (seats[s].exists /\ gateVerdict.status = "APPROVE" /\ sealed[s][n] # None) =>
      seatValueOrigin[s][n] \in {"Init", "SeatAdd", "Seal", "SeatInject"}

=============================================================================
