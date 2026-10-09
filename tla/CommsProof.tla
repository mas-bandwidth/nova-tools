--------------------------- MODULE CommsProof ---------------------------
\* SPEC-FRIEND.md, Bounded active-session bus proof. The actual root caller
\* owns Output/Reply; UUID and bus sender authentication remain caller contracts.
\* Existing ToolResultBinding covers durable transport and output-before-ACK.
EXTENDS Naturals
CONSTANTS MaxTime, MaxWrites, Broken
VARIABLES now, active, lease, until, serial, nonce, output, answered, busOwner,
          busVersion, localVersion, uncertain, owned, up, badProof, clobbered
vars == <<now, active, lease, until, serial, nonce, output, answered, busOwner,
          busVersion, localVersion, uncertain, owned, up, badProof, clobbered>>
Live == lease = "root" /\ now < until
Init == /\ now=0 /\ active=FALSE /\ lease="none" /\ until=0
        /\ serial=0 /\ nonce=0 /\ output=0 /\ answered=0
        /\ busOwner="other" /\ busVersion=1 /\ localVersion=0
        /\ uncertain=0 /\ owned=FALSE /\ up=TRUE /\ badProof=FALSE /\ clobbered=FALSE
Arm == /\ now < MaxTime /\ lease'="root" /\ until'=now+2
       /\ UNCHANGED <<now,active,serial,nonce,output,answered,busOwner,busVersion,
                      localVersion,uncertain,owned,up,badProof,clobbered>>
Start == /\ ~active /\ Live /\ active'=TRUE
         /\ localVersion'=busVersion
         /\ nonce'=0 /\ output'=0 /\ answered'=0 /\ uncertain'=0 /\ owned'=FALSE
         /\ UNCHANGED <<now,lease,until,serial,busOwner,
                        busVersion,up,badProof,clobbered>>
Ask == /\ active /\ Live /\ serial<2 /\ (nonce=0 \/ answered=nonce)
       /\ serial'=serial+1 /\ nonce'=serial+1
       /\ UNCHANGED <<now,active,lease,until,output,answered,busOwner,busVersion,
                      localVersion,uncertain,owned,up,badProof,clobbered>>
Output == /\ active /\ Live /\ nonce>0 /\ output'=nonce
          /\ UNCHANGED <<now,active,lease,until,serial,nonce,answered,busOwner,
                         busVersion,localVersion,uncertain,owned,up,badProof,clobbered>>
Reply(n,who) == /\ active /\ n \in 1..2 /\ who \in {"root","other"}
               /\ ((Live /\ who="root" /\ n=nonce /\ output=n)
                    \/ "wrong-reply" \in Broken)
               /\ answered'=n
               /\ badProof'=(badProof \/ who#"root" \/ n#nonce \/ output#n)
               /\ UNCHANGED <<now,active,lease,until,serial,nonce,output,busOwner,
                              busVersion,localVersion,uncertain,owned,up,clobbered>>
Write(lost) == /\ active /\ answered>0 /\ busVersion<MaxWrites
               /\ (Live \/ "no-lease" \in Broken)
               /\ busVersion=localVersion
               /\ busOwner'="root" /\ busVersion'=busVersion+1 /\ up'=TRUE
               /\ localVersion'=IF lost THEN localVersion ELSE busVersion+1
               /\ uncertain'=IF lost THEN busVersion+1 ELSE 0
               /\ owned'=IF lost THEN owned ELSE TRUE
               /\ badProof'=(badProof \/ ~Live)
               /\ UNCHANGED <<now,active,lease,until,serial,nonce,output,answered,clobbered>>
OtherWrite == /\ busVersion<MaxWrites
              /\ busOwner'="other" /\ busVersion'=busVersion+1 /\ up'=TRUE
              /\ UNCHANGED <<now,active,lease,until,serial,nonce,output,answered,
                             localVersion,uncertain,owned,badProof,clobbered>>
\* On a lost response the exact attempted version is also a cleanup candidate.
Own == busOwner="root" /\ ((owned /\ busVersion=localVersion) \/ busVersion=uncertain)
Stop == /\ active /\ active'=FALSE
        /\ up'=IF Own \/ "no-cas" \in Broken THEN FALSE ELSE up
        /\ clobbered'=(clobbered \/ (~Own /\ "no-cas" \in Broken /\ up))
        /\ UNCHANGED <<now,lease,until,serial,nonce,output,answered,busOwner,busVersion,
                       localVersion,uncertain,owned,badProof>>
LoseLease == /\ lease'="other" /\ until'=now+2
             /\ UNCHANGED <<now,active,serial,nonce,output,answered,busOwner,busVersion,
                            localVersion,uncertain,owned,up,badProof,clobbered>>
Tick == /\ now<MaxTime /\ now'=now+1
        /\ UNCHANGED <<active,lease,until,serial,nonce,output,answered,busOwner,
                       busVersion,localVersion,uncertain,owned,up,badProof,clobbered>>
Next == Arm \/ Start \/ Ask \/ Output \/ OtherWrite \/ Stop \/ LoseLease \/ Tick
        \/ (\E n \in 1..2, who \in {"root","other"}: Reply(n,who))
        \/ Write(TRUE) \/ Write(FALSE)
\* The finite process deadline eventually invokes Stop; no idle wake is modelled.
Spec == Init /\ [][Next]_vars /\ WF_vars(Stop)
TypeOK == /\ now \in 0..MaxTime /\ lease \in {"none","root","other"}
          /\ serial \in 0..2 /\ nonce \in 0..2 /\ output \in 0..2 /\ answered \in 0..2
          /\ busOwner \in {"root","other"} /\ busVersion \in 1..MaxWrites
          /\ localVersion \in 0..MaxWrites /\ uncertain \in 0..MaxWrites
          /\ owned \in BOOLEAN
FreshRootAndLeaseRequired == ~badProof
ReplacementPreserved == ~clobbered
ObserverEventuallyStops == [] (active ~> ~active)
NoRootProof == ~(busOwner="root" /\ up)
=============================================================================
