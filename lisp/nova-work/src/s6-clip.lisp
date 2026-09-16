;;;; s6-clip.lisp --- the clip: one long operation, the CAS guard, the boundary
;;;; record, rotation, and the overflow refusal.
;;;;
;;;; docs/SPEC-WORK.md:379-419 — a clip names a local event boundary, fetches the
;;;; upstream revision, refuses `CLIP RACED` by the one base predicate
;;;; (`tip == base`), pushes a deterministic snapshot by a compare-and-swap
;;;; (`--force-with-lease`; never a rewrite), appends one boundary record to the
;;;; journal carrying the commit sha and the clipped revision, and may rotate the
;;;; journal. A failed push leaves accepted local work locally durable and not
;;;; shared; a semantic conflict is never retried blindly — `--attempts` is the
;;;; *network's* budget inside `--git-timeout`, never a durability retry.
;;;;
;;;; docs/SPEC-WORK.md:650-662 — a clip whose snapshot would exceed the session's
;;;; own `--max-bytes` refuses, and the refusal prints all four numbers and names
;;;; a remedy that can actually move them.

(in-package #:nova-work)

(defun start-clip (&key (id "op-1") (request "-") (stamp "2026-09-16T10:00:00Z"))
  "The clip is one long operation: `clip` prints `OPERATION OK … op=clip` at once
and exits; the terminal `CLIP OK` line is what `operation wait` prints."
  (make-operation :id id :op :clip :request request :stamp stamp))

(defun clip-cas-guard (base tip)
  "The one base predicate of rule 2: the tip the session fetched is the base it
wrote. A moved tip means a hand edit or a new owner's take; never a merge."
  (string= base tip))

(defun clip-terminal-line (session operation-id boundary events base commit pushed attempts emitted)
  "`CLIP OK session=<path> operation=<id> boundary=<request-id> events=<n>
base=<sha> commit=<sha> pushed=<rev> attempts=<n> emitted=<bytes>` — printed by
`operation wait` when the transport settles."
  (format nil "CLIP OK session=~A operation=~A boundary=~A events=~D base=~A commit=~A pushed=~D attempts=~D emitted=~D"
          session operation-id boundary events base commit pushed attempts emitted))

(defun clip-raced-line (session operation-id boundary generation expected found)
  "`CLIP RACED … expected=<base sha> found=<tip sha>` — the base predicate refused
the push; exit 1, nothing pushed."
  (format nil "CLIP RACED session=~A operation=~A boundary=~A generation=~A expected=~A found=~A"
          session operation-id boundary generation expected found))

(defun clip-fail-line (session operation-id boundary base &key (events 0) (pushed "-")
                                                              (attempts 0)
                                                              (reason "locally durable, not shared"))
  "`CLIP FAIL …`: a refused push leaves accepted local work and the pending clip
intact and reports *locally durable, not shared*."
  (format nil "CLIP FAIL session=~A operation=~A boundary=~A events=~D base=~A pushed=~A attempts=~D: ~A"
          session operation-id boundary events base pushed attempts reason))

(defun clip-boundary-record (commit base clipped-rev local-boundary)
  "The one boundary record a clip appends to the journal: the commit sha this
clip pushed, its base sha, the clipped revision, and the local event boundary it
named — so the journal alone says which of its events a clip carried."
  (list :kind :boundary :commit commit :base base :clipped-rev clipped-rev
        :event-boundary local-boundary))

(defun clip-rotate (boundary-record)
  "A rotation opens a fresh segment whose first record is a copy of the boundary
record, so a new file is not a new journal and the chain continues unbroken."
  (copy-tree boundary-record))

(defun clip-overflow-p (snapshot retained index closed-index max-bytes)
  "True when any file the clip would write exceeds the session's own --max-bytes."
  (or (> snapshot max-bytes) (> retained max-bytes)
      (> index max-bytes) (> closed-index max-bytes)))

(defun clip-overflow-line (snapshot retained index closed-index max-bytes)
  "`CLIP FAIL … : snapshot=<bytes> retained=<bytes> index=<bytes> closed-index=<bytes>
past --max-bytes=<n>, lower --retain or raise --max-bytes`. All four numbers are
printed so the operator sizes the bench by every file the clip writes, and the
remedy names a flag that moves the part that overflowed."
  (format nil "CLIP FAIL : snapshot=~D retained=~D index=~D closed-index=~D past --max-bytes=~D, lower --retain or raise --max-bytes"
          snapshot retained index closed-index max-bytes))
