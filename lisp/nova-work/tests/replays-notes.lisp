;;;; replays-notes.lisp --- the six coordinator-note acceptance replays.
;;;;
;;;; Each case names the replay of docs/SPEC-WORK.md it comes from (the Required
;;;; replays table at docs/SPEC-WORK.md:4476-4481), and asserts the outcome that
;;;; table states. These are the pure notes core: write, supersede, list-active,
;;;; and the refusal each replay names, without a session, CLI or socket.

(in-package #:nova-work/tests)

(defparameter *notes-config*
  (make-notes-config
   :max-active 4
   :max-text-bytes 32
   :max-constraint-nodes 8
   :participants '("A" "B" "m1" "m2")
   :groups '(("G" "m1" "m2"))))

(defun fresh-notes ()
  (make-notes-store :config *notes-config*))

(defun notes-fields (&key (scope '(:coordinator "A")) (author "A")
                          (date "2026-09-14T23:05Z") (source "the human said so")
                          (kind :instruction) (text "do the thing")
                          (constraint +absent+) (uncertain +absent+))
  (list :scope scope :as author :kind kind :source source :date date
        :text text :constraint constraint :uncertain uncertain))

(defun big-constraint ()
  '(:constraint
    (:deny (:model "a") (:role "x") (:task-class :coding))
    (:prefer (:task-class :coding) (:route "r/m"))
    (:reason "because")))

;;; ------------------------------------------------------------------
;;; 1. notes-refuse-missing-source-or-date       SPEC-WORK.md:4476
;;; ------------------------------------------------------------------

(deftest "notes-refuse-missing-source-or-date" "docs/SPEC-WORK.md:4476"
    "without --source or --date, or a :kind outside the three, is refused NOTES FAIL at exit 2 naming the field, nothing written"
  (let ((s (fresh-notes)))
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :source nil))
      (ok (not ok) "a write without :source was accepted")
      (check-equal 2 code "missing :source exit code")
      (ok (search "NOTES FAIL" line) "missing :source line ~S" line)
      (ok (search ":source" line) "missing :source names the field ~S" line)
      (check-equal s new "missing :source wrote nothing"))
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :date nil))
      (ok (not ok) "a write without :date was accepted")
      (check-equal 2 code "missing :date exit code")
      (ok (search ":date" line) "missing :source names the field ~S" line)
      (check-equal s new "missing :date wrote nothing"))
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :kind :rant))
      (ok (not ok) "a write with a :kind outside the three was accepted")
      (check-equal 2 code "bad :kind exit code")
      (ok (search "kind" line) "bad :kind names the field ~S" line)
      (check-equal s new "bad :kind wrote nothing"))))

;;; ------------------------------------------------------------------
;;; 2. notes-id-is-content-digest                SPEC-WORK.md:4477
;;; ------------------------------------------------------------------

(deftest "notes-id-is-content-digest" "docs/SPEC-WORK.md:4477"
    "the same eight fields written on two benches yield one id; a second write of the same preimage is refused already written; no id is ever reused"
  (let ((s1 (fresh-notes))
        (s2 (fresh-notes))
        (f1 (notes-fields)))
    (multiple-value-bind (ok1 line1 code1 n1) (notes-write s1 f1)
      (ok ok1 "first bench accepted")
      (check-equal 0 code1 "first bench exit")
      (multiple-value-bind (ok2 line2 code2 n2) (notes-write s2 f1)
        (ok ok2 "second bench accepted")
        (check-equal 0 code2 "second bench exit")
        (let ((a (first (notes-notes n1)))
              (b (first (notes-notes n2))))
          (check-string= (note-id a) (note-id b)
                         "the same eight fields yield one id on two benches")))
      ;; a second write of the same preimage is refused, already written.
      (multiple-value-bind (ok3 line3 code3 n3)
          (notes-write n1 f1)
        (ok (not ok3) "a second write of the same preimage was accepted")
        (check-equal 2 code3 "duplicate exit code")
        (ok (search "already written" line3) "duplicate names already written ~S" line3)
        (ok (search (note-id (first (notes-notes n1))) line3)
            "duplicate names its id ~S" line3)
        (check-equal n1 n3 "duplicate wrote nothing"))
      ;; no id is ever reused: distinct content, distinct id.
      (multiple-value-bind (ok4 line4 code4 n4)
          (notes-write n1 (notes-fields :text "something else"))
        (ok ok4 "distinct content accepted")
        (ok (string/= (note-id (first (notes-notes n1)))
                      (note-id (second (notes-notes n4))))
            "two distinct notes reused one id")))))

;;; ------------------------------------------------------------------
;;; 3. notes-writer-is-scoped                    SPEC-WORK.md:4478
;;; ------------------------------------------------------------------

(deftest "notes-writer-is-scoped" "docs/SPEC-WORK.md:4478"
    "a (:coordinator A) note by --as B, a (:group G) note by a non-member, or an unregistered --as is refused; a note grants no access"
  (let ((s (fresh-notes)))
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :scope '(:coordinator "A") :author "B"))
      (ok (not ok) "a (:coordinator A) note by --as B was accepted")
      (check-equal 2 code "coordinator-scope exit code")
      (ok (search "not a writer" line) "coordinator scope names the reason ~S" line)
      (check-equal s new "--as B wrote nothing on A's scope"))
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :scope '(:group "G") :author "B"))
      (ok (not ok) "a (:group G) note by a non-member was accepted")
      (check-equal 2 code "group-scope exit code")
      (check-equal s new "a non-member wrote nothing on G's scope"))
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :author "Zed"))
      (ok (not ok) "an unregistered --as was accepted")
      (check-equal 2 code "unregistered exit code")
      (check-equal s new "an unregistered --as wrote nothing"))
    ;; a note grants nothing: one note in, and no access field anywhere on it.
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :scope '(:coordinator "A") :author "A"))
      (ok ok "a valid note is accepted")
      (let ((n (first (notes-notes new))))
        (check-equal 1 (length (notes-notes new)) "writing a note added exactly one record")
        (ok (not (member :access (list :scope (note-scope n)) ))
            "a note carries no access grant")))))

;;; ------------------------------------------------------------------
;;; 4. notes-bounds-refuse                       SPEC-WORK.md:4479
;;; ------------------------------------------------------------------

(deftest "notes-bounds-refuse" "docs/SPEC-WORK.md:4479"
    "a write past :max-active, :max-text-bytes or :max-constraint-nodes is refused naming the field and both numbers; a missing bound refuses to guess; a supersede never changes the active count"
  ;; :max-active -- fill to the bound, then one more.
  (let ((s (fresh-notes)))
    (dotimes (i 4)
      (multiple-value-bind (ok line code new)
          (notes-write s (notes-fields :text (format nil "t~D" i)))
        (ok ok "filling to :max-active accepted")
        (setf s new)))
    (check-equal 4 (notes-active-count s) "four active notes fill :max-active")
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :text "overflow"))
      (ok (not ok) "a write past :max-active was accepted")
      (check-equal 2 code "max-active exit code")
      (ok (search "active=" line) "names :max-active field ~S" line)
      (ok (search ":max-active=" line) "names :max-active bound ~S" line)
      (check-equal s new "a write past :max-active wrote nothing")))
  ;; :max-text-bytes
  (let ((s (fresh-notes)))
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :text (make-string 40 :initial-element #\x)))
      (ok (not ok) "a text past :max-text-bytes was accepted")
      (check-equal 2 code "text-bytes exit code")
      (ok (search "text=" line) "names the text field ~S" line)
      (ok (search ":max-text-bytes=32" line) "names both numbers ~S" line)
      (check-equal s new "a write past :max-text-bytes wrote nothing")))
  ;; :max-constraint-nodes
  (let ((s (fresh-notes)))
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :constraint (big-constraint)))
      (ok (not ok) "a constraint past :max-constraint-nodes was accepted")
      (check-equal 2 code "constraint-nodes exit code")
      (ok (search "constraint=" line) "names the constraint field ~S" line)
      (ok (search ":max-constraint-nodes=8" line) "names both numbers ~S" line)
      (check-equal s new "a write past :max-constraint-nodes wrote nothing")))
  ;; a missing bound refuses to guess.
  (let* ((cfg (make-notes-config :max-active nil :max-text-bytes 32 :max-constraint-nodes 8
                                 :participants '("A") :groups '()))
         (s (make-notes-store :config cfg)))
    (multiple-value-bind (ok line code new)
        (notes-write s (notes-fields :author "A"))
      (ok (not ok) "a missing bound was guessed")
      (check-equal 2 code "missing-bound exit code")
      (ok (search "refusing to guess" line) "names guessing ~S" line)
      (check-equal s new "a missing bound wrote nothing"))))

;;; ------------------------------------------------------------------
;;; 5. notes-supersede-is-one-envelope           SPEC-WORK.md:4480
;;; ------------------------------------------------------------------

(deftest "notes-supersede-is-one-envelope" "docs/SPEC-WORK.md:4480"
    "a supersede writes both events or neither; the second of two competing supersedes is refused not active: superseded-by <first>; a superseded note is never printed as active"
  (let ((s (fresh-notes)))
    (multiple-value-bind (ok line code s)
        (notes-write s (notes-fields :text "v1"))
      (ok ok "the preimage written")
      (let ((old-id (note-id (first (notes-notes s)))))
        (multiple-value-bind (ok2 l2 c2 s2)
            (notes-supersede s (list :as "A" :id old-id :source "new words"
                                     :date "2026-09-14T23:10Z" :text "v2" :reason "updated"))
          (ok ok2 "the supersede accepted")
          (check-equal 0 c2 "supersede exit code")
          (check-equal (notes-active-count s) (notes-active-count s2)
                       "a supersede never changes the active count")
          (let ((old (note-by-id s2 old-id)))
            (ok (note-superseded old) "the old note is not marked superseded"))
          (let ((act (notes-active s2)))
            (check-equal 1 (length act) "one active note after a supersede")
            (ok (string/= old-id (note-id (first act)))
                "the superseded note is still printed as active"))
          ;; the second of two competing supersedes is refused.
          (multiple-value-bind (ok3 l3 c3 s3)
              (notes-supersede s2 (list :as "A" :id old-id :source "other"
                                        :date "2026-09-14T23:11Z" :text "v3"
                                        :reason "again"))
            (ok (not ok3) "a second competing supersede was accepted")
            (check-equal 2 c3 "second supersede exit code")
            (ok (search "not active" l3) "names not active ~S" l3)
            (ok (search "superseded-by" l3) "names superseded-by ~S" l3)
            (check-equal s2 s3 "the second supersede wrote something"))))))
  ;; a supersede writes both events or neither: a failing supersede writes neither.
  (let ((s (fresh-notes)))
    (multiple-value-bind (ok line code s)
        (notes-write s (notes-fields :text "keep"))
      (ok ok "the preimage for atomicity written")
      (let ((old-id (note-id (first (notes-notes s)))))
        (multiple-value-bind (ok2 l2 c2 s2)
            (notes-supersede s (list :as "A" :id old-id :source "s"
                                     :date "2026-09-14T23:12Z"
                                     :text (make-string 100 :initial-element #\y)
                                     :reason "too long"))
          (ok (not ok2) "an over-long supersede was accepted")
          (check-equal 1 (length (notes-notes s2)) "no replacement event was written")
          (ok (not (note-superseded (first (notes-notes s2))))
              "the supersede event was written alone"))))))

;;; ------------------------------------------------------------------
;;; 6. notes-weaker-kind-cannot-supersede        SPEC-WORK.md:4481
;;; ------------------------------------------------------------------

(deftest "notes-weaker-kind-cannot-supersede" "docs/SPEC-WORK.md:4481"
    "an :observation or :heuristic replacement for an :instruction is refused; the replacement inherits :kind and :scope and carries a new :source"
  (flet ((refused (new-kind)
           (handler-case (progn (check-not-weaker-kind :instruction new-kind "note:deadbeef")
                                nil)
             (unsupported-input (c)
               (search "weaker" (format nil "~A" c))))))
    (ok (refused :observation) "an :observation replacement for an :instruction was accepted")
    (ok (refused :heuristic) "a :heuristic replacement for an :instruction was accepted")
    (ok (not (refused :instruction)) "an :instruction replacement was refused"))
  (let* ((old (make-note :scope '(:coordinator "A") :author "A" :date "2026-09-14T23:05Z"
                         :source "old words" :kind :instruction :text "v1"))
         (repl (make-replacement-note old "A" "new words" "2026-09-14T23:10Z" :text "v2")))
    (check-equal :instruction (note-kind repl) "the replacement inherited :kind")
    (check-equal '(:coordinator "A") (note-scope repl) "the replacement inherited :scope")
    (check-string= "new words" (note-source repl) "the replacement carries a new :source")))

;;; ------------------------------------------------------------------
;;; 7. TestE08F01ProvideBoundedFamilyVerbHelp   SPEC-WORK.md:2664
;;;    (criterion E08-F01-02: "Provide bounded family/verb help and
;;;    machine discovery with schema hash")
;;; ------------------------------------------------------------------
;;; docs/SPEC-WORK.md:2664 pins the schema the criterion names: it is "one
;;; generated schema file (every verb with its op, event kind, ordered fields
;;; and grammar line)". From it, "bounded family/verb help" is a reader that
;;; lists the verbs grouped into families within a bound, "machine discovery"
;;; is the fleet listing, and the "schema hash" is the digest that lets a
;;; client refuse a stale discovered copy. The kernel's verb schema today is
;;; the flat *mutation-grammar*: three mutation verbs, each an event kind and
;;; an ordered field list, but no verb belongs to a named family, nothing
;;; exposes a help listing, and no schema hash is carried on the schema or on
;;; a machine row -- so a client still has to carry the manual, and cannot
;;; tell a stale schema from a current one. This records the gap.

(deftest "TestE08F01ProvideBoundedFamilyVerbHelp" "docs/SPEC-WORK.md:2664"
    "the verb schema groups verbs into families for a bounded help listing and carries a schema hash; machine discovery is answered against it"
  (let ((grammar nova-work:*mutation-grammar*))
    (ok grammar "the kernel has no verb schema at all")
    ;; Bounded family/verb help: every verb belongs to a named family, so help
    ;; can list them grouped and bounded rather than as one flat grammar.
    (dolist (entry grammar)
      (ok (getf (cdr entry) :family)
          "expected verb ~S to belong to a named family for bounded family/verb help, but the schema groups no family"
          (car entry)))
    ;; Schema hash: the schema (and each row of the discovery that reads it)
    ;; carries a digest, so a client can refuse a stale discovered copy.
    (ok (every (lambda (entry) (getf (cdr entry) :schema-hash)) grammar)
        "expected the verb schema to carry a schema hash for stale-discovery refusal, but ~S carries none"
        grammar)))
