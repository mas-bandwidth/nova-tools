# Unified v1.2 stop and refill contracts

`StopCancelsRecovery` and `FriendReadyTakeModes` retain the original canonical
model actions and add the reviewed private v1.2 states. The original canonical
StopCancels and FriendReadyTake modules, configurations and eleven historical
receipts remain byte-identical. These companions select the combined contract
for current runtime coverage; two separate models are not a composition proof.

The STOP companion adds live native groups and verified identity separate from
lane memory, restart retention, qualified exit before hand-back, identity loss
and per-attempt cancellation/return counts. The existing six canonical property
cases and five new native/repeat-return cases are declared against this unified
machine. Eventual return assumes fair End/Quiesce/Return and verified identity;
identity loss deliberately defeats that liveness claim. No universal native
exit or progress under unlimited restart/urgent arrivals is claimed.

The refill companion adds one-shot width rather than batch twice-width room,
preserves backlog at a mode change, and witnesses finish refilling one card
while another remains active. Five canonical cases and two new one-shot cases
are declared against the combined machine. Each child is one abstract slot;
work/read role, concrete packet selection and disk/store atomicity are outside
this model.

All eighteen cases are unmeasured at this source revision. New named wrappers
and rows avoid importing private receipts for changed inputs. Existing canonical
rows are historical coverage of their original bounded machines. The companions
use exact reviewed model semantics from private donor
`4af7db035f2a0bdbad40d5d5f401ba242daaaf70`; only module names and case keys change.

Source mapping remains the StopCancels recovery verbs in stop.go/lanes.go and
FriendReadyTake deal/take/finish verbs. Native primary/auxiliary identity,
protected durable RUN, repeated generation wire hand-back, concrete one-shot
refill and group-quiet-before-capacity release need executable source tests.
This model does not assert parser, filesystem, kernel or runtime alignment.
