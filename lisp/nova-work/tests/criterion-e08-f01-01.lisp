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
        (check-equal 2 code "the exit of a flag with no value")))))
