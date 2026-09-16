;;;; s8-config.lisp --- the `config` and `observe` records and parsers (S8).
;;;;
;;;; docs/SPEC-WORK.md:1035-1043 (`:observe`, `:config`), :2273, :2275,
;;;; :3409-3422. Both are friend-identity subjects (never a node) that a later
;;;; wiring card journals; this file holds the pure record shape and the
;;;; argument normalization only.

(in-package #:nova-work)

(defstruct (config-record (:conc-name config-))
  friend base revision hash parts reason)

(defstruct (observation (:conc-name observation-))
  change friend state source attempt observed-model bench usage reason)

(defun parse-config-args (args)
  "Normalize `config --request|--export|--intake`. Returns a config-record with
  the intake fields left `(:absent)` where not given. The digest pins what was
  named, never the path it arrived on (SPEC-WORK.md:1040-1041)."
  (make-config-record
   :friend (getf args :friend)
   :base (getf args :base +absent+)
   :revision (getf args :revision +absent+)
   :hash (getf args :hash +absent+)
   :parts (getf args :parts +absent+)
   :reason (getf args :reason)))

(defun parse-observe-args (args)
  "Normalize `observe --state|--attempt`. Exactly one of the two changes."
  (let ((changes (remove-if #'null
                            (list (and (member :state args) :state)
                                  (and (member :attempt args) :attempt)))))
    (when (null changes)
      (error 'unsupported-input :what "observe needs --state or --attempt"))
    (when (cdr changes)
      (error 'unsupported-input :what "observe takes exactly one of --state|--attempt")))
  (make-observation
   :change (if (member :state args) :state :attempt)
   :friend (getf args :friend)
   :state (getf args :state)
   :source (getf args :source)
   :attempt (getf args :attempt)
   :observed-model (getf args :observed-model)
   :bench (getf args :bench)
   :usage (getf args :usage)
   :reason (getf args :reason)))
