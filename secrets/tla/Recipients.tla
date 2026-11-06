------------------------------ MODULE Recipients ------------------------------
\* Who can open what nova-secrets grant writes. A key is named by its seat; Rec
\* is the recovery key (offline: no action uses it); Stranger is a key someone
\* with push access to the store wants to reach a value, and it never acts.
\*
\* In the store, writable by any pusher:
\*   rule[s]     every recipient sops would use for <s>.yaml (age, key_groups,
\*               pgp, anchors resolved: the code refuses all but plain age)
\*   ruleKey[s]  the first key of that rule, the seat's key as the store says
\*   storeRec    the store's recovery.pub
\*   link[s]     which seat's file <s>.grants.yaml really is (a symlink)
\*   gnames[s]   the names <s>.grants.yaml holds, read from its plaintext keys
\* On the control node only:
\*   pin[s]      the seat's key, read from the machine over SSH ("none" before)
\*   ledger[s]   the names this node granted to s
\* What grant and set wrote:
\*   gfile[s]    <s>.grants.yaml: its recipients, whether it holds the fleet value, and the names grant sealed into it
\*   ofile[s]    <s>.yaml: its recipients, once the seat has set a value
\* Ghosts: crossread (a seat read another seat's file).
\*
\* Broken = "none" is the design. Reversed witnesses, one per finding:
\*   "opreader"  the granting key is a recipient too (OperatorNeverRecipient)
\*   "rulekeys"  grant seals to what the rule names (NoGrantLeak)
\*   "nopin"     grant trusts the rule's seat key instead of the pin (NoGrantLeak)
\*   "storerec"  grant seals to the store's recovery.pub (NoGrantLeak)
\*   "follow"    a seat file that is a symlink is read through it (NoCrossRead); a write renames over the link
\*   "heldfromstore" grant keeps the names the store's file holds, not the ledger's (OnlyGrantedNames)
\*   "setrule"   set seals the seat's own file to what the rule names (NoOwnLeak)
EXTENDS FiniteSets

CONSTANTS Seats, Op, Rec, Stranger, Broken

ASSUME Op \in Seats /\ Rec \notin Seats /\ Stranger \notin Seats

Keys == Seats \cup {Rec, Stranger}
Machines == Seats \ {Op}

Names == {"fleet", "planted"}   \* the fleet value, and a name a pusher plants in a grants file

VARIABLES rule, ruleKey, storeRec, link, gnames, pin, ledger, gfile, ofile, crossread

vars == <<rule, ruleKey, storeRec, link, gnames, pin, ledger, gfile, ofile, crossread>>
store == <<rule, ruleKey, storeRec, link, gnames>>

TypeOK ==
  /\ rule \in [Machines -> SUBSET Keys]
  /\ ruleKey \in [Machines -> Keys]
  /\ storeRec \in Keys
  /\ link \in [Machines -> Machines]
  /\ gnames \in [Machines -> SUBSET Names]
  /\ pin \in [Machines -> Keys \cup {"none"}]
  /\ ledger \in [Machines -> SUBSET Names]
  /\ gfile \in [Machines -> [exists : BOOLEAN, recips : SUBSET Keys, fleet : BOOLEAN, names : SUBSET Names]]
  /\ ofile \in [Machines -> [exists : BOOLEAN, recips : SUBSET Keys]]
  /\ crossread \in BOOLEAN

\* Every machine has its seat (seat add); nothing is pinned or granted yet.
Init ==
  /\ rule = [s \in Machines |-> {s, Rec}]
  /\ ruleKey = [s \in Machines |-> s]
  /\ storeRec = Rec
  /\ link = [s \in Machines |-> s]
  /\ gnames = [s \in Machines |-> {}]
  /\ pin = [s \in Machines |-> "none"]
  /\ ledger = [s \in Machines |-> {}]
  /\ gfile = [s \in Machines |-> [exists |-> FALSE, recips |-> {}, fleet |-> FALSE, names |-> {}]]
  /\ ofile = [s \in Machines |-> [exists |-> FALSE, recips |-> {}]]
  /\ crossread = FALSE

Untouched(s) == rule[s] = {s, Rec} /\ ruleKey[s] = s /\ storeRec = Rec /\ link[s] = s /\ gnames[s] = ledger[s]

\* A pusher edits the store: a key in the rule (or the operator's), the rule's seat key, recovery.pub, a symlink.
Node == <<pin, ledger, gfile, ofile, crossread>>
TamperRule(s) == \E k \in {Stranger, Op} : rule' = [rule EXCEPT ![s] = @ \cup {k}] /\ UNCHANGED <<ruleKey, storeRec, link, gnames, Node>>
TamperKey(s) == ruleKey' = [ruleKey EXCEPT ![s] = Stranger] /\ rule' = [rule EXCEPT ![s] = (@ \ {s}) \cup {Stranger}]
                /\ UNCHANGED <<storeRec, link, gnames, Node>>
TamperRec == storeRec' = Stranger /\ UNCHANGED <<rule, ruleKey, link, gnames, Node>>
TamperLink(s) == \E t \in Machines \ {s} : link' = [link EXCEPT ![s] = t] /\ UNCHANGED <<rule, ruleKey, storeRec, gnames, Node>>
\* A pusher adds a plaintext key to a grants file, hoping the next grant fills it.
TamperNames(s) == gnames' = [gnames EXCEPT ![s] = @ \cup {"planted"}] /\ UNCHANGED <<rule, ruleKey, storeRec, link, Node>>

\* join.yml reads the machine's public key over SSH, before any tamper can reach it, and grant --pub pins it.
Pin(s) == pin[s] = "none" /\ pin' = [pin EXCEPT ![s] = s] /\ UNCHANGED <<store, ledger, gfile, ofile, crossread>>

\* nova-secrets set on machine s: its own file, sealed with sops --config /dev/null to its own key (age-keygen -y
\* of --key) and the recovery pin; the rule must say the same, so a replaced rule key refuses.
Set(s) ==
  /\ Broken = "setrule" \/ (ruleKey[s] = s /\ storeRec = Rec)
  /\ ofile' = [ofile EXCEPT ![s] = [exists |-> TRUE, recips |-> IF Broken = "setrule" THEN rule[s] ELSE {s, Rec}]]
  /\ UNCHANGED <<store, pin, ledger, gfile, crossread>>

\* nova-secrets grant fleet --to t: pins checked against the store, the file read as itself, sealed with
\* sops --config /dev/null --age <pin>,<recovery pin>.
Grant(t) ==
  /\ pin[t] # "none" /\ ~gfile[t].fleet
  /\ Broken = "nopin" \/ ruleKey[t] = pin[t]
  /\ Broken = "storerec" \/ storeRec = Rec
  /\ Broken = "follow" \/ link[t] = t
  /\ Broken = "heldfromstore" \/ gnames[t] = ledger[t]
  /\ LET seat == IF Broken = "nopin" THEN ruleKey[t] ELSE pin[t]
         rec == IF Broken = "storerec" THEN storeRec ELSE Rec
         to == IF Broken = "rulekeys" THEN rule[t] \cup {seat} ELSE {seat, rec} \cup (IF Broken = "opreader" THEN {Op} ELSE {})
         names == {"fleet"} \cup (IF Broken = "heldfromstore" THEN gnames[t] ELSE ledger[t])
     IN /\ gfile' = [gfile EXCEPT ![t] = [exists |-> TRUE, recips |-> to, fleet |-> TRUE, names |-> names]]  \* a rename over a link replaces the link
        /\ gnames' = [gnames EXCEPT ![t] = names]
        /\ ledger' = [ledger EXCEPT ![t] = names]
  /\ UNCHANGED <<rule, ruleKey, storeRec, link, pin, ofile, crossread>>

\* nova-secrets exec on machine s: its own grants file, read only as itself.
Read(s) ==
  /\ Broken = "follow" \/ link[s] = s
  /\ crossread' = (crossread \/ link[s] # s)
  /\ UNCHANGED <<store, pin, ledger, gfile, ofile>>

Next ==
  \/ TamperRec
  \/ \E s \in Machines : TamperRule(s) \/ TamperKey(s) \/ TamperLink(s) \/ TamperNames(s) \/ Pin(s) \/ Set(s) \/ Grant(s) \/ Read(s)

Spec == Init /\ [][Next]_vars /\ \A s \in Machines : WF_vars(Pin(s)) /\ WF_vars(Grant(s))

\* A grant reaches only the machine's own key and the recovery key, whatever the store says.
NoGrantLeak == \A s \in Machines : gfile[s].exists => gfile[s].recips \subseteq {s, Rec}

\* The recovery key opens every grants file.
RecoveryInGrants == \A s \in Machines : gfile[s].exists => Rec \in gfile[s].recips

\* The control node's key opens no machine's grants: a compromised control node reads only its own seat.
OperatorNeverRecipient == \A s \in Machines : Op \notin gfile[s].recips

\* A seat reads only its own files.
NoCrossRead == ~crossread

\* grant seals values only under names the control node was asked for: a key a pusher plants in the file is never filled.
OnlyGrantedNames == \A s \in Machines : gfile[s].names \subseteq {"fleet"}

\* A seat's own file reaches only its own key and the recovery key, whatever the rule says.
NoOwnLeak == \A s \in Machines : ofile[s].exists => ofile[s].recips = {s, Rec}

\* Every machine whose store entries are untouched comes to hold the fleet value.
Filled == \A s \in Machines : Untouched(s) ~> (gfile[s].fleet \/ ~Untouched(s))
=============================================================================
