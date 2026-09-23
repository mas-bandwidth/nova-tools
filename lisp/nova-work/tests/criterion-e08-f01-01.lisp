;;;; criterion-e08-f01-01.lisp --- nova-work E08-F01-01: "Use one schema for
;;;; client validation, protocol, help and examples" (docs/roadmaps/nova-work.sexp,
;;;; feature E08-F01, "Versioned typed command schema and discovery").
;;;;
;;;; The proof is that the four readings of a verb come from ONE table,
;;;; NOVA-WORK::*REQUEST-SCHEMA* (src/request-line.lisp), and that each reading
;;;; is the one the live socket actually serves:
;;;;
;;;;   protocol    -- the verbs the session answers are the schema's verbs, and
;;;;                  every schema verb's example is answered OK on a real socket;
;;;;   validation  -- a flag the schema does not list for the verb, or a flag
;;;;                  with no value, is refused at exit 2 naming it, where before
;;;;                  the session answered OK and dropped it unread;
;;;;   help        -- the usage line is rendered from the schema, and for a verb
;;;;                  the spec's verbs block spells it is that block's line byte
;;;;                  for byte; the refusal carries it, so help is on the wire;
;;;;   examples    -- every example validates under the same schema that refuses
;;;;                  the bad request, and the refusal carries it too.

(in-package #:nova-work/tests)

(defun %e08-spec-lines ()
  "The lines of docs/SPEC-WORK.md, read from the repository this system lives in."
  (with-open-file (in (asdf:system-relative-pathname :nova-work "../../docs/SPEC-WORK.md")
                      :external-format :utf-8)
    (loop for line = (read-line in nil nil) while line collect line)))

;;;; The Go client's side of the one schema. cmd/nova-work/main.go's verbFlags
;;;; is the table the client validates with: sessionVerb builds a flag.FlagSet of
;;;; exactly a verb's row, so the client admits a flag iff the row lists it and
;;;; refuses a listed flag with no value ("flag needs an argument"). Reading the
;;;; row out of the Go source here, the way cmd/nova-work/specverbs_test.go reads
;;;; the spec's verbs block, makes the schema the one table both sides answer
;;;; to: a row that drifts from *REQUEST-SCHEMA* is a red test, and every flag the
;;;; Go client admits or refuses is sent over the wire and must get the same
;;;; answer from the session.

(defun %e08-repo-lines (relative)
  "The lines of RELATIVE, a path from the repository root."
  (with-open-file (in (asdf:system-relative-pathname :nova-work
                                                     (concatenate 'string "../../" relative))
                      :external-format :utf-8)
    (loop for line = (read-line in nil nil) while line collect line)))

(defun %e08-quoted-after (line key)
  "The double-quoted string that follows KEY on LINE, or NIL."
  (let ((at (search key line)))
    (when at
      (let* ((q1 (position #\" line :start (+ at (length key))))
             (q2 (and q1 (position #\" line :start (1+ q1)))))
        (and q2 (subseq line (1+ q1) q2))))))

(defun %e08-go-verb-table (relative var)
  "The Go map literal VAR (`var VAR = map[string][]flagSpec{`) in RELATIVE, as
an alist (VERB . ((NAME KIND) ...)) in source order; KIND is :bool, :multi or
:value, the three kinds flagSpec carries."
  (let ((lines (member (format nil "var ~A = map[string][]flagSpec{" var)
                       (%e08-repo-lines relative) :test #'string=))
        (rows '()) (row nil))
    (dolist (line (rest lines) (nreverse rows))
      (let ((trimmed (string-trim '(#\Space #\Tab) line)))
        (cond
          ((string= line "}") (return (nreverse rows)))
          ((and (> (length trimmed) 4) (char= #\" (char trimmed 0))
                (string= "\": {" trimmed :start2 (- (length trimmed) 4)))
           (setf row (list (subseq trimmed 1 (- (length trimmed) 4))))
           (push row rows))
          ((and row (search "{name: " trimmed))
           (setf (cdr (last row))
                 (list (list (%e08-quoted-after trimmed "{name: ")
                             (cond ((search "bool: true" trimmed) :bool)
                                   ((search "multi: true" trimmed) :multi)
                                   (t :value)))))))))))

(defun %e08-go-client-flags ()
  "Every row the Go client validates with: main.go's verbFlags, then
socketverbs.go's moreVerbFlags, which init() folds into verbFlags."
  (append (%e08-go-verb-table "cmd/nova-work/main.go" "verbFlags")
          (%e08-go-verb-table "cmd/nova-work/socketverbs.go" "moreVerbFlags")))

(deftest "e08-f01-01-use-schema-client-validation"
    "docs/roadmaps/nova-work.sexp E08-F01-01; docs/SPEC-WORK.md, The verbs"
    "one schema drives client validation, the protocol's answered verbs, help lines and examples"
  (let ((schema nova-work::*request-schema*)
        (spec (%e08-spec-lines)))
    (ok (consp schema) "there is no request schema: ~S" schema)
    ;; Help: rendered from the schema, and the spec's own line where the spec
    ;; spells the verb.
    (let ((usage (nova-work::request-schema-usage "session status")))
      (check-string= "session status --session <path>" usage
                     "the usage line rendered from the schema for session status"))
    (dolist (entry schema)
      (let* ((verb (nova-work::request-schema-verb entry))
             (usage (nova-work::request-schema-usage verb))
             (example (nova-work::request-schema-example verb)))
        (ok (and (stringp usage) (eql 0 (search verb usage)))
            "the usage line of ~S does not open with its verb: ~S" verb usage)
        (when (nova-work::request-schema-in-spec-p entry)
          (ok (member (concatenate 'string "nova-work " usage) spec :test #'string=)
              "the help line ~S is not a line of the spec's verbs block" usage))
        ;; Examples: every one is a request the same schema admits.
        (check-string= verb (nova-work::request-line-verb example)
                       (format nil "the example ~S spells another verb" example))
        (check-equal nil (nova-work::request-schema-problem example)
                     (format nil "the schema refuses its own example ~S" example))))
    (with-served-session (server sock)
      ;; Protocol: every schema verb's example is answered on the real socket,
      ;; and a verb outside the schema is not.
      (dolist (entry schema)
        (let* ((example (nova-work::request-schema-example
                         (nova-work::request-schema-verb entry)))
               (reply (ask-over-the-socket sock example)))
          (check-string= "OK" (reply-verdict reply)
                         (format nil "the example ~S answered ~S" example reply))))
      (multiple-value-bind (ok-p line code)
          (nova-work:serve-session-request server "machine --session x --register m1")
        (ok (not ok-p) "a verb outside the schema was answered: ~S" line)
        (check-equal 2 code "the exit of a verb outside the schema"))
      ;; Validation: a flag the schema does not list for the verb.
      (let ((reply (ask-over-the-socket
                    sock (format nil "session status --session ~A --bogus 1" sock))))
        (check-string= "FAIL" (reply-verdict reply)
                       (format nil "an unlisted flag was answered ~S" reply))
        (ok (search "--bogus" reply) "the refusal does not name the flag: ~S" reply)
        (ok (search (nova-work::request-schema-usage "session status") reply)
            "the refusal does not carry the schema's usage line: ~S" reply)
        (ok (search (nova-work::request-schema-example "session status") reply)
            "the refusal does not carry the schema's example: ~S" reply))
      (multiple-value-bind (ok-p line code)
          (nova-work:serve-session-request server "session status --bogus 1")
        (ok (not ok-p) "an unlisted flag was accepted: ~S" line)
        (check-equal 2 code "the exit of an unlisted flag"))
      ;; Validation: a listed flag with no value.
      (multiple-value-bind (ok-p line code)
          (nova-work:serve-session-request server "session status --session")
        (ok (not ok-p) "a flag with no value was accepted: ~S" line)
        (ok (search "--session needs a value" line)
            "the refusal does not say the value is missing: ~S" line)
        (check-equal 2 code "the exit of a flag with no value"))
      ;; Client validation: the Go client's row for every verb it sends that
      ;; the schema holds is the schema's flags, name for name and in order,
      ;; and every flag any Go row names is admitted on the wire exactly when
      ;; the Go client admits it for this verb.
      (let* ((go-rows (%e08-go-client-flags))
             (every-go-flag (remove-duplicates
                             (loop for (nil . flags) in go-rows
                                   append (mapcar #'first flags))
                             :test #'string= :from-end t)))
        (ok (> (length every-go-flag) 10)
            "the Go client's flag tables were not read: ~S" go-rows)
        (dolist (entry schema)
          (when (nova-work::request-schema-in-spec-p entry)
            (let* ((verb (nova-work::request-schema-verb entry))
                   (go-row (cdr (assoc verb go-rows :test #'string=)))
                   (go-names (mapcar #'first go-row)))
              (ok go-row "the Go client carries no verbFlags row for ~S" verb)
              (check-equal (mapcar #'first (getf (rest entry) :flags)) go-names
                           (format nil "the Go client's flags for ~S are not the schema's" verb))
              (ok (every (lambda (f) (eq :value (second f))) go-row)
                  "the Go client's row for ~S carries a switch the schema has no kind for: ~S"
                  verb go-row)
              (dolist (flag every-go-flag)
                (let* ((request (format nil "~A --~A v" verb flag))
                       (client-admits (member flag go-names :test #'string=))
                       (reply (ask-over-the-socket sock request)))
                  (check-string= (if client-admits "OK" "FAIL") (reply-verdict reply)
                                 (format nil "the Go client ~:[refuses~;admits~] ~S but the wire answered ~S"
                                         client-admits request reply))))
              ;; A flag with no value: the Go FlagSet refuses it, and so must the wire.
              (dolist (flag go-names)
                (let ((reply (ask-over-the-socket sock (format nil "~A --~A" verb flag))))
                  (check-string= "FAIL" (reply-verdict reply)
                                 (format nil "the wire admitted ~A --~A with no value" verb flag)))))))))))
