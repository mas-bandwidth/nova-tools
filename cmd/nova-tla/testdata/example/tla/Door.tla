---- MODULE Door ----
\* A door that opens and closes: the first run's example model.
VARIABLE open
Init == open = FALSE
Next == open' = ~open
DoorCloses == open = FALSE
====
