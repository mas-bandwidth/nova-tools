---------------------------- MODULE MCServerLine ----------------------------
\* The instance: three batches, each two verbs (a write then a beat, a beat then a
\* read, a write then a read), a bound of two ticks of the server's clock, one tick
\* of the run loop and one landing's step on the line.
EXTENDS ServerLine

MCBatches == [b \in {"b1", "b2", "b3"} |->
                CASE b = "b1" -> <<"write", "beat">>
                  [] b = "b2" -> <<"beat", "read">>
                  [] b = "b3" -> <<"write", "read">>]
=============================================================================
