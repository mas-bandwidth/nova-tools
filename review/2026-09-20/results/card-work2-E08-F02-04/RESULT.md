RESULT work2-E08-F02-04 sha=a3abdd4ad6dd — nova-work E08-F02 acceptance criterion, criterion E08-F02-04 (docs/roadmaps/nova-work.sexp): Use bounded typed JSON over a local Unix socket, exact integer/time encoding and durable asynchronous operation IDs; reconcile cross-platform endpoint requirements before lock
DONE
CRITERION E08-F02-04 STATE unmet
BRANCH rowan/work2-E08-F02-04
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/request-line.lisp

SPEC-WORK line found:
  docs/SPEC-WORK.md:2661 — "The wire is a versioned, bounded, length-prefixed UTF-8 JSON protocol, and this paragraph pins it"
  (clauses of the criterion map to :2646-2648 the one local Unix socket / Windows named pipe under its
   platform's spelling and never a second transport, and :2669-2674 every protocol integer a JSON string
   of decimal digits never a JSON number, every timestamp RFC 3339 UTC.)

TEST: TestE08F02UseBoundedTypedJSONOver (lisp/nova-work/tests/request-line.lisp, section 5)

Suite runs (./run-tests.sh, whole, non-interactive SBCL):
  run 1: NOVA-WORK SLICE1 total=408 pass=399 fail=9
  run 2: NOVA-WORK SLICE1 total=408 pass=399 fail=9

VERDICT for the roadmap row:
  The test is RED, but the immediate RED in this sandbox is environmental, not the
  criterion. The new test (and all 7 pre-existing request-line socket tests) fail with
  "Can't create directory /tmp/…" because this Landlock sandbox blocks /tmp (and every
  short-path socket location: /var/tmp, /dev/shm, /home/glenn) while the only writable
  directories (TMPDIR=…/.nova-sandbox-tmp, HOME=…/data, CWD) are longer than the 108-byte
  sun_path limit, so no Unix socket can be bound at all. Baseline was total=407 pass=399
  fail=8, the 8 pre-existing failures being endpoint-is-local-and-private (bind EACCES) and
  7 request-line tests, all from the same /tmp block.

  The criterion is nonetheless UNMET on the code, by unambiguous evidence:

  * src/request-line.lisp:4-32 header — "THE SEAM WAS NEVER FILLED" and "the framed one
    replaces this one line when it lands"; :147 `(setf *session-request-handler*
    #'serve-request-line)` binds the S1 TEXT line wire, not the framed JSON wire.
  * src/transport.lisp:887-889 — `*session-request-handler*` docstring "A later wire slice
    binds the framed protocol here"; :896-911 `%serve-connection` runs read-line/write-line
    (text), never frame read/write.
  * internal/workclient/frame.go:13-22 — the Go client already speaks the framed v1 wire "so
    the client half exists when the session attaches its own" — i.e. the session has NOT
    attached it. The session's socket serves the S1 line wire; the bounded, length-prefixed
    typed JSON codec exists in both halves (transport.lisp, operations.lisp) and is already
    pinned at the codec level (tests wire-is-length-prefixed-utf8-json, wire-integers-are-strings,
    protocol-version-negotiated-or-refused — all pass), but it is not wired to the socket.

  That this subfeature is the one of nine under E08-F02 not already verified is consistent
  with the roadmap's 8/9 count: the codec clauses are the 8, and "over a local Unix socket"
  (the combination the criterion names) is the missing one.

git status --short:
  M lisp/nova-work/tests/request-line.lisp

head a2f43dfa62f12464242e69767d1dc9ae7bbefa66

Left owed:
  The test asserts the criterion's behaviour and is RED (in this sandbox an environment
  failure that masks the real one; in a writable-/tmp environment it would fail with
  "the socket did not answer the v1 hello with a bounded 4-byte-length-prefixed JSON frame;
  it answered SESSION FAIL …"). To go green the resident session must attach transport.lisp's
  framed wire to `*session-request-handler*`/`%serve-connection` (a later wire slice), at
  which point this card's criterion and the roadmap row can be ticked.
