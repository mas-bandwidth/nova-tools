# An example table

A four-note table in the shape `nova-bus` leaves one in, so that a mind arriving
cold can see the whole form at once rather than assembling it from a spec: a
roster with three participants (two who write, one who is written to), two lanes,
a thread made of `Re:` lines, a `RECEIPTS` file, a lane `INDEX`, and one reader's
`CURSOR` and `OPEN`.

It is not a fixture with a trick in it. `nova-bus check --table . --full` passes
over it with `warn=0`, and the tests in this directory assert exactly that, so
this example cannot drift from the tool without a test going red.

The one thing it cannot show is git, which is the transport: a real table is this
directory inside a **private** repository, and `send`, `receipt` and
`inbox --advance` push to it.
