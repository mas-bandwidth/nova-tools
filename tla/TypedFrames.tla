---------------------------- MODULE TypedFrames ----------------------------
\* Fleet readers and writers exchanging typed frames in legacy and table forms,
\* with damaged, clamped and absent-field frames injected on the wire.
\*
\* Rules modeled:
\* 1. FrameRefusedBeforeTransition: a reader validates a frame before acting on
\*    it; refused frames move no state.
\* 2. DualReadNoLoss: during the dual-read window no valid frame of either form
\*    is refused or lost.
\* 3. ReadersBeforeWriters: writers switch to the table form only after every
\*    reader has adopted dual read.
\* 4. NoClampAccepted: a clamped value on load is refused and never acted on.
\* 5. AbsentIsNotZero: an absent required field is refused and never read as
\*    zero.
\* 6. WriterRefusesWhatReaderRefuses: a writer validates before writing and
\*    refuses every frame shape its strict reader refuses.

EXTENDS Naturals, FiniteSets

CONSTANTS Readers, Writers, MaxFrames, Broken

VARIABLES
  issued,
  wire,
  readerMode,
  writerForm,
  acted,
  refused,
  stateMoved,
  readVal,
  formCount

vars == <<issued, wire, readerMode, writerForm, acted, refused, stateMoved, readVal, formCount>>

Forms == {"legacy", "table"}
ReaderModes == {"legacy", "dual", "table_only"}
Shapes == {"valid", "damaged", "clamped", "absent"}
ReadVals == {"unset", "zero", "valid"}

Frame == [
  id : 1..MaxFrames,
  reader : Readers,
  form : Forms,
  shape : Shapes,
  fromWriter : BOOLEAN
]

FormAccepted(mode, form) ==
  \/ (mode = "legacy" /\ form = "legacy")
  \/ (mode = "dual" /\ form \in Forms)
  \/ (mode = "table_only" /\ form = "table")

StrictReaderShapeOK(sh) ==
  sh = "valid"

ReaderShapeOK(sh) ==
  \/ StrictReaderShapeOK(sh)
  \/ (Broken = "clamp-accepted" /\ sh = "clamped")
  \/ (Broken = "absent-is-zero" /\ sh = "absent")

WriterShapeOK(sh) ==
  \/ StrictReaderShapeOK(sh)
  \/ (Broken = "writer-looser" /\ sh \in {"clamped", "absent"})

Init ==
  /\ issued = 0
  /\ wire = {}
  /\ readerMode = [r \in Readers |-> "legacy"]
  /\ writerForm = [w \in Writers |-> "legacy"]
  /\ acted = {}
  /\ refused = {}
  /\ stateMoved = [r \in Readers |-> 0]
  /\ readVal = [r \in Readers |-> "unset"]
  /\ formCount = [r \in Readers |-> [form \in Forms |-> 0]]

AdoptDualRead(r) ==
  /\ readerMode[r] = "legacy"
  /\ readerMode' = [readerMode EXCEPT ![r] = "dual"]
  /\ UNCHANGED <<issued, wire, writerForm, acted, refused, stateMoved, readVal, formCount>>

SwitchWriterToTable(w) ==
  /\ writerForm[w] = "legacy"
  /\ Broken = "writers-first" \/ (\A r \in Readers : readerMode[r] /= "legacy")
  /\ writerForm' = [writerForm EXCEPT ![w] = "table"]
  /\ UNCHANGED <<issued, wire, readerMode, acted, refused, stateMoved, readVal, formCount>>

RetireLegacyParser(r) ==
  /\ readerMode[r] = "dual"
  /\ \A w \in Writers : writerForm[w] = "table"
  /\ \A f \in wire : f.reader = r => f.form = "table"
  /\ readerMode' = [readerMode EXCEPT ![r] = "table_only"]
  /\ UNCHANGED <<issued, wire, writerForm, acted, refused, stateMoved, readVal, formCount>>

WriterEmit(w, r, sh) ==
  /\ issued < MaxFrames
  /\ WriterShapeOK(sh)
  /\ LET f == [id |-> issued + 1, reader |-> r, form |-> writerForm[w], shape |-> sh, fromWriter |-> TRUE]
     IN /\ issued' = issued + 1
        /\ wire' = wire \cup {f}
        /\ UNCHANGED <<readerMode, writerForm, acted, refused, stateMoved, readVal, formCount>>

InjectFrame(r, form, sh) ==
  /\ issued < MaxFrames
  /\ sh \in {"damaged", "clamped", "absent"}
  /\ LET f == [id |-> issued + 1, reader |-> r, form |-> form, shape |-> sh, fromWriter |-> FALSE]
     IN /\ issued' = issued + 1
        /\ wire' = wire \cup {f}
        /\ UNCHANGED <<readerMode, writerForm, acted, refused, stateMoved, readVal, formCount>>

ReadFrame(f) ==
  /\ f \in wire
  /\ LET r == f.reader
         formOK ==
           IF Broken = "dual-read-loss" /\ readerMode[r] = "dual" /\ f.form = "legacy"
           THEN FALSE
           ELSE FormAccepted(readerMode[r], f.form)
         shapeOK == ReaderShapeOK(f.shape)
         accepted == formOK /\ shapeOK
     IN /\ wire' = wire \ {f}
        /\ IF accepted
           THEN /\ acted' = acted \cup {f}
                /\ refused' = refused
                /\ stateMoved' = [stateMoved EXCEPT ![r] = @ + 1]
                /\ readVal' = [readVal EXCEPT ![r] = IF f.shape = "absent" THEN "zero" ELSE "valid"]
                /\ formCount' = [formCount EXCEPT ![r][f.form] = @ + 1]
           ELSE /\ acted' = acted
                /\ refused' = refused \cup {f}
                /\ stateMoved' =
                     IF Broken = "refused-before-transition"
                     THEN [stateMoved EXCEPT ![r] = @ + 1]
                     ELSE stateMoved
                /\ readVal' = readVal
                /\ formCount' = formCount
        /\ UNCHANGED <<issued, readerMode, writerForm>>

Next ==
  \/ \E r \in Readers : AdoptDualRead(r)
  \/ \E w \in Writers : SwitchWriterToTable(w)
  \/ \E r \in Readers : RetireLegacyParser(r)
  \/ \E w \in Writers, r \in Readers, sh \in Shapes : WriterEmit(w, r, sh)
  \/ \E r \in Readers, form \in Forms, sh \in Shapes : InjectFrame(r, form, sh)
  \/ \E f \in wire : ReadFrame(f)

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ issued \in 0..MaxFrames
  /\ wire \subseteq Frame
  /\ readerMode \in [Readers -> ReaderModes]
  /\ writerForm \in [Writers -> Forms]
  /\ acted \subseteq Frame
  /\ refused \subseteq Frame
  /\ stateMoved \in [Readers -> 0..MaxFrames]
  /\ readVal \in [Readers -> ReadVals]
  /\ formCount \in [Readers -> [Forms -> 0..MaxFrames]]

FrameRefusedBeforeTransition ==
  /\ acted \cap refused = {}
  /\ \A f \in acted : f.shape /= "damaged"
  /\ \A r \in Readers :
       stateMoved[r] = Cardinality({f \in acted : f.reader = r})

DualReadNoLoss ==
  \A f \in refused : f.shape /= "valid"

ReadersBeforeWriters ==
  (\E w \in Writers : writerForm[w] = "table") =>
    (\A r \in Readers : readerMode[r] /= "legacy")

NoClampAccepted ==
  \A f \in acted : f.shape /= "clamped"

AbsentIsNotZero ==
  /\ \A f \in acted : f.shape /= "absent"
  /\ \A r \in Readers : readVal[r] /= "zero"

WriterRefusesWhatReaderRefuses ==
  \A f \in wire \cup acted \cup refused :
    f.fromWriter => StrictReaderShapeOK(f.shape)
=============================================================================
