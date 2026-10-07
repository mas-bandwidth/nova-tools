---------------------- MODULE MCTableFirstContact ----------------------
\* The TLC instances: two processes of two verbs each. MCAll turns on every
\* outside event; MCNone is a fresh store and nothing else.
EXTENDS TableFirstContact
MCProcs == {"p", "q"}
MCVerbs == {1, 2}
MCAll == {"lost", "fail", "deploy", "older"}
MCNone == {}
=============================================================================
