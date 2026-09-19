;;;; slice-10-fleet.lisp --- the five fleet acceptance replays
;;;; of docs/SPEC-WORK.md:3459-3588 (card #8628).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

;;;; ------------------------------------------------------------------
;;;; The fleet's acceptance replays (docs/SPEC-WORK.md:3459-3588, card #8628).
;;;;
;;;; Each replay sets up the state its paragraph describes, drives the one
;;;; `:machine` verb and asserts the outcome the paragraph promises. The
;;;; kernel change these need is src/fleet.lisp: the static member records,
;;;; the register path and its refusals. The machine parameter of a request
;;;; is the record's stable id, the team's own configuration; no machine or
;;;; host name is a constant anywhere in the tool.
;;;; ------------------------------------------------------------------

(defun machine-kernel (&key (friends '("glenn" "rowan")))
  (make-kernel :state (make-seed-state *seed*) :friends friends))

(defun register-request (&key (id "m-a1") (name "studio") (owner "glenn")
                              (connect "profile:studio") (roles '(:build :test))
                              (permits '("go-test")) (excludes '("bench:schema"))
                              (limits '(:concurrent 4)) (facts nil)
                              (declared-by "glenn") (request "mreq-1")
                              (stamp "2026-09-15T01:05:00Z"))
  (list :verb :machine :change :register :machine id :name name :owner owner
        :connect connect :roles roles :permits permits :excludes excludes
        :limits limits :facts facts :declared-by declared-by
        :request request :stamp stamp :clock :tool :generation-owner "gen-1"))

(deftest "fleet-is-static-config" "docs/SPEC-WORK.md:3585"
    "expected=machine-event-moves-no-count-no-roadmap;heartbeat-observe-probe-change-no-member"
  (let* ((k (machine-kernel))
         ;; One preceding envelope, so the record this case reads is not also
         ;; the only one: with two records, reading the wrong end is detectable.
         (pre (submit k (edit-request "acme/work/f1/t1" :request "pre-1"
                                      :title '(:set "t"))))
         (before-open (state-open-count (kernel-state k)))
         (before-rev (state-revision (kernel-state k)))
         (before-history (length (state-history (kernel-state k))))
         (before-rows (length (state-closed-rows (kernel-state k)))))
    (declare (ignorable pre))
    (multiple-value-bind (okp line code) (submit k (register-request))
      (ok okp "machine register refused: ~A" line)
      (check-equal 0 code "machine register exit code")
      (ok (search "MACHINE OK" line) "machine OK line")
      (ok (search "machine=m-a1" line) "machine OK line names the member"))
    ;; A member is CONFIG, never work: no count, roadmap or required set moves.
    (check-equal before-open (state-open-count (kernel-state k))
                 "a :machine event moves no |O|")
    (check-equal (1+ before-rev) (state-revision (kernel-state k))
                 "the revision advanced by exactly one")
    ;; The CONFIG envelope grows the work-tree history like every other
    ;; envelope (apply-envelope, state.lisp:697-700), and its single event
    ;; names no containment node: `:node` is `(:absent)`. `state-history` is
    ;; oldest-first (it reverses the push-ordered `wstate-history`), so the
    ;; record just added is the LAST one, not the first.
    (let* ((history (state-history (kernel-state k)))
           (record (car (last history)))
           (event (first (getf record :events))))
      (check-equal (1+ before-history) (length history)
                   "exactly one envelope record was added")
      (check-equal t (absentp (getf event :node))
                   "the envelope record names no containment node")
      (check-equal +absent+ (getf event :node) "the node field is the absent value"))
    (check-equal before-rows (length (state-closed-rows (kernel-state k)))
                 "a :machine event moves no closed row")
    ;; A heartbeat, an observe and a probe are not CONFIG changes: the verb
    ;; refuses them and the member is exactly what registration wrote.
    (let ((registered (fleet-member (kernel-fleet k) "m-a1")))
      (dolist (change '(:heartbeat :observe :probe))
        (multiple-value-bind (okp line code)
            (submit k (list :verb :machine :change change :machine "m-a1"
                            :request (format nil "mreq-~A" change)))
          (ok (not okp) "~A is not a static-config change: ~A" change line)
          (ok (= 2 code) "~A is unsupported, not a member change" change))
        (let ((after (fleet-member (kernel-fleet k) "m-a1")))
          (ok (equal (machine-name registered) (machine-name after))
              "~A changed no member name" change)
          (ok (equal (machine-connect registered) (machine-connect after))
              "~A changed no member connect" change)))
      (check-equal 1 (fleet-member-count (kernel-fleet k))
                   "one configured member, unchanged by ACTIVE events"))))

(deftest "no-machine-name-in-the-tool" "docs/SPEC-WORK.md:3586"
    "expected=no-default-machine-or-host;identity-arrives-as-configuration"
  (let ((k (machine-kernel)))
    ;; Nothing ships with a machine name: the fleet is empty until CONFIG
    ;; supplies a record.
    (check-equal 0 (fleet-member-count (kernel-fleet k))
                 "the tool ships no machine; the fleet is a configured section")
    (multiple-value-bind (okp line code)
        (submit k (register-request :id "m-a1" :name "studio" :request "mreq-name"))
      (ok okp "configured register refused: ~A" line)
      (check-equal 0 code "configured register exit code"))
    (let ((member (fleet-member (kernel-fleet k) "m-a1")))
      (check-equal "m-a1" (machine-id member) "identity is the configured stable id")
      (check-equal "studio" (machine-name member) "the display name is instance data")
      ;; The tool keys a member by its configured id and resolves no name.
      (ok (null (gethash "studio" (fleet-machines (kernel-fleet k))))
          "a display name is not an identity the tool resolves"))))

(deftest "one-profile-one-unit" "docs/SPEC-WORK.md:3586"
    "expected=one-profile-one-unit;second-register-on-a-held-profile-refused"
  (let ((k (machine-kernel)))
    (multiple-value-bind (okp line code)
        (submit k (register-request :id "m-a1" :connect "profile:studio"
                                    :request "mreq-p1"))
      (ok okp "first register refused: ~A" line)
      (check-equal 0 code "first register exit code"))
    ;; A second record whose --connect is the connection profile of a member
    ;; already in the fleet is refused, naming the holder, and nothing written.
    (multiple-value-bind (okp line code)
        (submit k (register-request :id "m-b2" :name "other"
                                    :connect "profile:studio" :request "mreq-p2"))
      (ok (not okp) "a held profile is not a second unit: ~A" line)
      (check-equal 1 code "held-profile refusal exit code")
      (check-string= "MACHINE FAIL machine=m-b2: connect held by m-a1" line
                     "the refusal names the holder"))
    (check-equal 1 (fleet-member-count (kernel-fleet k))
                 "a refused register writes nothing")))

(deftest "no-credential-in-a-member" "docs/SPEC-WORK.md:3587"
    "expected=credential-in-record-refused-whole;value-not-echoed"
  (let ((k (machine-kernel)))
    ;; A --connect that is not a `profile:` reference is a credential.
    (multiple-value-bind (okp line code)
        (submit k (register-request :id "m-raw" :connect "ssh://glenn@host"
                                    :request "mreq-c1"))
      (ok (not okp) "a raw connect is not a profile reference: ~A" line)
      (check-equal 1 code "credential refusal exit code")
      (check-string= "MACHINE FAIL machine=m-raw: credential in record" line
                     "credential in record refusal")
      (ok (not (search "ssh://glenn@host" line))
          "the refused --connect value is never echoed"))
    ;; Any field named key, token, password or secret refuses the record whole.
    (dolist (field '(:key :token :password :secret))
      (let ((request (register-request :id "m-secret"
                                       :request (format nil "mreq-~A" field))))
        (setf (getf request field) "s3cr3t-value")
        (multiple-value-bind (okp line code) (submit k request)
          (ok (not okp) "a ~A field is a credential: ~A" field line)
          (check-equal 1 code "credential field refusal exit code")
          (check-string= "MACHINE FAIL machine=m-secret: credential in record" line
                         "credential-in-record refusal")
          (ok (not (search "s3cr3t-value" line))
              "the refused ~A value is never echoed" field))))
    (check-equal 0 (fleet-member-count (kernel-fleet k))
                 "a record with a credential is refused whole, nothing written")))

(deftest "unknown-owner-is-refused" "docs/SPEC-WORK.md:3587"
    "expected=owner-must-be-a-friend;no-owner-and-no-id-refused;nothing-written"
  (let ((k (machine-kernel :friends '("glenn" "rowan"))))
    ;; An :owner who is not a friend of `friends` is refused.
    (multiple-value-bind (okp line code)
        (submit k (register-request :id "m-x" :owner "stranger"
                                    :request "mreq-o1"))
      (ok (not okp) "an unknown owner is refused: ~A" line)
      (check-equal 1 code "unknown-owner refusal exit code")
      (check-string= "MACHINE FAIL machine=m-x: unknown owner" line
                     "unknown-owner refusal"))
    ;; A record with no :owner.
    (multiple-value-bind (okp line code)
        (submit k (register-request :id "m-y" :owner nil :request "mreq-o2"))
      (ok (not okp) "a record with no owner is refused: ~A" line)
      (check-equal 1 code "no-owner refusal exit code")
      (check-string= "MACHINE FAIL machine=m-y: no owner" line "no-owner refusal"))
    ;; A record with no stable :id prints machine=-.
    (multiple-value-bind (okp line code)
        (submit k (register-request :id nil :request "mreq-o3"))
      (ok (not okp) "a record with no id is refused: ~A" line)
      (check-equal 1 code "no-id refusal exit code")
      (check-string= "MACHINE FAIL machine=-: no id" line "no-id refusal"))
    ;; An owner who is a friend is admitted, so the refusal is about the owner.
    (multiple-value-bind (okp line code)
        (submit k (register-request :id "m-ok" :owner "rowan" :request "mreq-o4"))
      (ok okp "a friend's record is admitted: ~A" line)
      (check-equal 0 code "friend-owner register exit code"))
    (check-equal 1 (fleet-member-count (kernel-fleet k))
                 "only the friend's record is written")))
