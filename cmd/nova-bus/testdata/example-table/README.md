# An example table

A four-note table in the shape `nova-bus` leaves one in, so that a mind arriving
cold can see the whole form at once rather than assembling it from a spec: a
roster with three participants (two who write, one who is written to), two lanes,
a thread made of `Re:` lines, a `RECEIPTS` file, a lane `INDEX`, and one reader's
`CURSOR` and `OPEN`.

It is not a fixture with a trick in it. `nova-bus check --table . --full` passes
over it with `warn=0`, and the tests in this directory assert exactly that, so
this example cannot drift from the tool without a test going red.

The one thing it cannot show is git, which is the transport. A real table is a
**private** repository whose ROOT is a directory shaped like this one — not a
subdirectory of a larger repository. `nova-bus` refuses a `--table` that is not
its repository's root for every verb that reads git, because git reports changed
paths relative to the repository root, so a table one directory down would report
an empty change set over unread notes.

That is why this copy lives under `testdata/` and is read with `--full`: it is a
directory inside *this* repository, which is a repository about tools and not a
table. To try it, copy it out and give it a repository of its own:

```
cp -R cmd/nova-bus/testdata/example-table ~/my-table
cd ~/my-table && git init -b main && git add -A && git commit -m 'the table'
nova-bus check --table ~/my-table --full
```
