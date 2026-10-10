Delivery order is the local protocol in `../delivery.go` and `../stage.go`.
The two positive cases check safety and eventual delivery. Their terminal state
is every card delivered with its lane running; `CHECK_DEADLOCK FALSE` accepts
that intended terminal state while keeping temporal checks enabled.

The two reversed cases must fail exactly on `LaneMeetsCheckout` (brief first)
and `NoDoubleStage` (no ownership claim). `DeliverOrder.RUNS.tsv` records the
model, configuration, and TLC jar fingerprints from the isolated Linux run.
The command is `java -cp tla2tools.jar tlc2.TLC -workers 2 -config <case>.cfg
DeliverOrder`; the worker report retains all four complete logs and exit receipts.
These nested models are separate from the central `tla/CASES.tsv` registry.
