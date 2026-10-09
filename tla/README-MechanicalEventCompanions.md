# Friend redeal and file-dependent beat companions

The isolated official reconciliation retains the original canonical
SprintEvents, MCSprintEvents, Friend, MCFriend configurations and their measured
receipts. These versioned full companions add the exact reviewed incoming
model semantics without replacing canonical files or claiming old receipts for
new inputs. Five new cases remain unmeasured pending independent cold review.

`SprintEventsFriendRedeal` copies the additive model from public source
6b1c2656574e35fdaa5809d156c728f32c9b0fec (PR5511), with a renamed header and
instance. Two proposed configurations are retained; a third uses the original
canonical main configuration as a combined-semantics control. FriendRedeal
starts from a card withdrawn on a named friend row and selects another up friend
with room. FriendNotLeft constrains only the owner, including the wrong-owner
mutation. The source fleet child is Ready; the model uses Working and changes
its primary column too. Only the member identity/old-row removal is a proposed
refinement. Native start, exact Ready/Working phase, generator behavior and shared
read capacity are not established by this property.

The source fixture TestAFriendRowRedealtIsNotLeftOnTheOldFriend observes the same
card ID, changed generation, old owner withdrawal and subsequent different
owner. It is supplied by the public donor and not imported or run by this model
port. The test is source evidence only after its executable gate; no mechanically
validated TLA execution projection is claimed here.

`FriendFileBeat` copies the additive model from public source
551ae0d9654da05c30b144f5f7336d6b91c4326d (PR5512), with a renamed header/instance.
WriteFile, FolderCheck and BeatOnFile add monotone file/visible/dependent-beat
state; all old actions retain these fields and old fairness clauses remain.
The positive configuration uses the same small bounds as the proposed broken
configuration and checks its existing communication invariants/properties plus
FileBeforeBeat. The reverse emits a file-dependent beat while no file exists;
it is an abstract invented-observation diagnostic. It is not an ordinary
zero-time emptywalk beat or by itself proof of an Activity/Beat source reorder
mutation. A real emptywalk still beats with zero and is outside this property.

The source fixture TestTheFolderCheckReadsTheFileBeforeTheBeatThatCarriesIt
observes actual NewestWrite plus daemon Activity/Beat callbacks, using a fake
clock and a temporary file. That source trace is not imported or executed here.
One monotone file excludes deletion and changing modification times; bounded
walking, failed Info, historical cached timestamps, multiple paths and exact
real callback ordering remain executable source obligations. No broad filesystem
presence or activity freshness guarantee is asserted.

Both companions are bounded contract additions. No universal progress, whole
runtime composition, kernel/disk/RPC or source implementation proof follows
from their future model outcomes. Libraries considered: the original TLA
Naturals, FiniteSets, Sequences and TLC dependencies; no new runner or runtime
implementation is introduced.
