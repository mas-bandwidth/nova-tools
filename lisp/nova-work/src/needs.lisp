;;;; needs.lisp --- the needs-met predicate (nova-tools #785).
;;;;
;;;; docs/SPEC-WORK.md:4780-4867, rules 1 and 2 of *The dependency gate and the
;;;; hand report*. One function answers whether a need is met, and one reason
;;;; token says why when it is not. Rule 2's vocabulary is the whole of it:
;;;; NEED-OPEN, NEED-REVERTED, NEED-UNAVAILABLE, NEED-CLOSED-UNACCEPTED,
;;;; NEED-UNVERIFIED. Nothing here is wired to a verb: rule 3's gate at the
;;;; admission verbs is the next slice.
;;;;
;;;; Rule 1: a need is *terminal accepted* when it is in C with disposition
;;;; `done` AND every evidence event its standing `:to :done` names qualifies
;;;; its criterion by a raw fact the session's verification cache already
;;;; holds. Recorded is not verified (:4785). The gate never fetches (:4789).
;;;; Stale does not unmeet a need (:4760): the `:against`-versus-source
;;;; comparison that makes an event stale is the one comparison left out, so a
;;;; `source` bump re-blocks no dependent.
;;;;
;;;; THE READ-TIME SEAM. `wstate` carries no evidence store — a `:to :done`
;;;; holds its evidence as opaque event ids (src/kernel.lisp:217) — and no
;;;; verification cache: that lives in a VERIFICATION-SESSION (src/verifier.lisp)
;;;; the tree does not hold. So rule 1's verified half reaches the gate through
;;;; a NEEDS-VIEW the session hands it: the evidence records a node's standing
;;;; done names, the verification session whose cache already holds the raw
;;;; facts, each node's current generation, and each node's `responsible`.
;;;; A view naming no evidence for a node names no evidence event that fails to
;;;; qualify, and rule 1's quantifier is then vacuous — which is the only
;;;; reading available while the tree keeps evidence as ids, and is unreachable
;;;; for a leaf, since validator rule 17 refuses a `:to :done` with no evidence
;;;; at all (src/kernel.lisp:218).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; the reason vocabulary                        SPEC-WORK.md:4820
;;; ------------------------------------------------------------------

(defparameter *needs-reasons*
  '(:need-open :need-reverted :need-unavailable :need-closed-unaccepted
    :need-unverified)
  "The five tokens of rule 2's table and the whole vocabulary. A row prints one
of these whenever `unmet=` is above zero, and nothing else ever.")

(defun needs-reason-token (reason)
  "The printed spelling of a reason, or `-` where a node is needs-met."
  (if (null reason)
      "-"
      (progn
        (unless (member reason *needs-reasons*)
          (error 'unsupported-input
                 :what (format nil "~S is not one of the five need reasons" reason)))
        (string-downcase (symbol-name reason)))))

;;; ------------------------------------------------------------------
;;; the needs view                               SPEC-WORK.md:4780-4790
;;; ------------------------------------------------------------------

(defstruct (needs-view
            (:constructor make-needs-view
                (&key session evidence generations responsible)))
  "The read-time facts rule 1 needs and the tree does not hold.

SESSION is a VERIFICATION-SESSION: its cache holds the raw resolutions `verify`
established, and its resolver list names the identities a fact may be
attributed to. The gate reads that cache and runs no resolver.

EVIDENCE is ((node-id record ...) ...): the VERIFY-EVIDENCE records the node's
standing `:to :done` names. GENERATIONS is ((node-id . generation) ...): a
`correct` bumps a node's generation, and evidence of an older generation
qualifies nothing (:4835). RESPONSIBLE is ((node-id . name) ...), read for the
resolver column of rule 2's table."
  session evidence generations responsible)

(defun needs-view-node-evidence (view id)
  "The evidence records VIEW names for ID, or NIL where it names none."
  (and view (cdr (assoc id (needs-view-evidence view) :test #'equal))))

(defun needs-view-node-generation (view id)
  (and view (cdr (assoc id (needs-view-generations view) :test #'equal))))

(defun needs-view-node-responsible (view id)
  (and view (cdr (assoc id (needs-view-responsible view) :test #'equal))))

;;; ------------------------------------------------------------------
;;; rule 1's verified half                       SPEC-WORK.md:4780-4792
;;; ------------------------------------------------------------------

(defun %need-evidence-qualifies-p (view id evidence)
  "Whether one evidence event of ID qualifies its criterion by a raw fact the
session's verification cache already holds. No fetch, and no staleness: the
`:against`-versus-source comparison is the one left out (:4760)."
  (let ((generation (needs-view-node-generation view id))
        (session (and view (needs-view-session view))))
    (cond
      ;; a `correct` bumped the node's generation: evidence of the older one is
      ;; of the older generation and qualifies nothing (:4835).
      ((and generation (verify-evidence-generation evidence)
            (not (eql generation (verify-evidence-generation evidence))))
       nil)
      ;; no session means no cache, and a fact no cache holds is not verified.
      ((null session) nil)
      (t
       (let* ((pointer (verify-evidence-pointer evidence))
              (subject (verify-evidence-subject evidence))
              (scheme (pointer-scheme pointer))
              (resolver (and scheme
                             (find scheme (verification-session-resolvers session)
                                   :key #'verification-resolver-scheme
                                   :test #'equal)))
              (identity (and resolver (verification-resolver-command resolver)))
              (cache (verification-session-cache session))
              (fact (and cache identity
                         (verification-cache-lookup cache pointer subject identity))))
         (and fact
              (eq :holds (verification-fact-fact fact))
              (verify-qualifies-p evidence)
              t))))))

(defun %need-evidence-verified-p (state id view)
  "True when every evidence event ID's standing `:to :done` names qualifies.
A view naming none names none that fails; see THE READ-TIME SEAM above."
  (declare (ignore state))
  (let ((records (needs-view-node-evidence view id)))
    (every (lambda (e) (%need-evidence-qualifies-p view id e)) records)))

;;; ------------------------------------------------------------------
;;; the one predicate                            SPEC-WORK.md:4780-4867
;;; ------------------------------------------------------------------

(defun %need-resolver (state need reason dependent view)
  "The resolver rule 2's table names for one unmet need."
  (let ((n (%node-quiet state need)))
    (case reason
      ((:need-open :need-reverted)
       (or (and n (wnode-holder n))
           (needs-view-node-responsible view need)
           "-"))
      (:need-unavailable "-")
      (:need-closed-unaccepted
       (or (and dependent (needs-view-node-responsible view dependent)) "-"))
      (:need-unverified
       (or (needs-view-node-responsible view need) "-"))
      (t "-"))))

(defun need-met-p (state need &key view dependent)
  "Rule 1's *terminal accepted*, and rule 2's one reason when it is not.

Answers (values MET-P REASON RESOLVER). MET-P is true only for a need that is
in C with disposition `done` and whose standing done's every evidence event is
verified from the cache, or for a container whose every direct required member
is met by this same rule. REASON is NIL when met and one of *NEEDS-REASONS*
otherwise; DEPENDENT is the node that holds the edge, which rule 2's table
names as the resolver of a closed-unaccepted need.

This reads the tree, the closed index and the verification cache and fetches
nothing (:4789). A cancelled, superseded or removed need is never met and never
becomes met (:4841)."
  (let ((n (%node-quiet state need)))
    (labels ((unmet (reason)
               (values nil reason (%need-resolver state need reason dependent view))))
      (cond
        ;; An id naming nothing is validator rule 2's `dangling` at the verb
        ;; that would write the edge; read here it is a page that cannot be
        ;; read, and incomplete is not green (:4839).
        ((null n) (unmet :need-unavailable))
        ;; Row 4 is read before rows 1 and 2, and from the disposition rather
        ;; than the branch. The table says a cancelled, superseded or removed
        ;; need is in C; this kernel's `event --kind cancel` writes a
        ;; `:terminal` event that sets the disposition and leaves the node in O
        ;; (src/edit-undo.lisp:248-253), while `node remove` settles into C with
        ;; disposition `removed` (src/kernel.lisp:480). Reading the disposition
        ;; keeps the substance of :4841 -- never met and never becomes met --
        ;; under both spellings. The branch gap is a finding, not a licence.
        ((%need-closed-unaccepted-p state need) (unmet :need-closed-unaccepted))
        ;; rows 1 and 2: the need is in O.
        ((eq :o (wnode-branch n))
         (unmet (if (%need-revived-p state need) :need-reverted :need-open)))
        (t
         (let ((row (%need-settle-row state need)))
           (cond
             ;; row 3: in C, and the closed-index page that says how cannot be read.
             ((null row) (unmet :need-unavailable))
             (t
              (let ((disposition (getf row :disposition)))
                (cond
                  ((not (eq disposition :done)) (unmet :need-unverified))
                  ;; the container clause: no evidence of its own, met with its
                  ;; direct required members, and an empty required set never
                  ;; settles (:4849).
                  ((%need-container-p n)
                   (let ((members (%need-required-members state n)))
                     (if (null members)
                         (unmet :need-open)
                         (let ((first-unmet
                                 (loop for m in members
                                       for (met reason) = (multiple-value-list
                                                           (need-met-p state m
                                                                       :view view
                                                                       :dependent need))
                                       unless met return reason)))
                           (if first-unmet (unmet first-unmet) (values t nil "-"))))))
                  ;; row 5: done, and short of rule 1.
                  ((not (%need-evidence-verified-p state need view))
                   (unmet :need-unverified))
                  (t (values t nil "-"))))))))))))

(defun node-needs-status (state id &key view)
  "The dependency reading of one node: (values UNMET FIRST-NEED REASON RESOLVER).

Every unmet need is counted and the first in `:deps` order is named, with its
one reason (:4859). A node with no `:deps` is needs-met, so UNMET is 0,
FIRST-NEED is `-` and REASON is NIL. The direct needs only: a need's own needs
were its own gate (:4755). This reads and writes nothing."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (let ((unmet 0) (first-need nil) (first-reason nil) (first-resolver "-"))
      (dolist (need (wnode-deps n))
        (multiple-value-bind (met reason resolver)
            (need-met-p state need :view view :dependent id)
          (unless met
            (incf unmet)
            (unless first-need
              (setf first-need need first-reason reason first-resolver resolver)))))
      (values unmet (or first-need "-") first-reason first-resolver))))

(defun node-needs-met-p (state id &key view)
  "True when every need of ID is met, so a node with no `:deps` is needs-met."
  (zerop (node-needs-status state id :view view)))
