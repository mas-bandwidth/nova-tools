;;;; routes.lisp --- the model route registry of CONFIG, the `query --ask routes`
;;;; view and the cheapest-first routing projection (docs/SPEC-WORK.md:3732-3799,
;;;; the replays at :6287-6314, E09 row 2).
;;;;
;;;; A route is a `:kind :route` member record of the `routes` section of CONFIG
;;;; beside the `fleet` section: configuration, never work, no child of O and
;;;; under no repository work set, so no count, roadmap or required set moves
;;;; when one is written. A probe is ACTIVE evidence and never overwrites a
;;;; declared field. Routing is a projection of the registry at a named scope
;;;; revision, cheapest first, and a route with no passing probe carries no card.
;;;;
;;;; Nothing here starts a session, a socket, a provider or a launcher: this is
;;;; the pure view and projection the four route replays call. The `route` and
;;;; `probe` verbs and the live CLI wiring are later E09 rows.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The route member and the registry
;;; ------------------------------------------------------------------

(defparameter *route-plans* '(:flat :free :metered :local)
  "SPEC-WORK.md:3766 --- the cost class, one of flat, metered, free and local.")

(defparameter *route-capability-keys* '(:text :code :tool-calls)
  "SPEC-WORK.md:3770 --- the declared capabilities and their one word each.")

(defparameter *route-capability-values* '(:yes :no)
  "An unknown capability value is a refusal, never a guess.")

(defparameter *route-credential-keys* '(:key :token :password :secret)
  "A field named any of these in a route record is a credential (SPEC-WORK.md:3551).")

(defstruct (route (:constructor make-route
                                 (&key id provider endpoint key-location plan
                                       cost-per-mtok capabilities owner)))
  "One CONFIG `:kind :route` member: how a model is run, remembered by the
tool. Its `:key-location` names a path or an env name and is never a key value."
  id provider endpoint key-location plan cost-per-mtok capabilities owner)

(defstruct (route-registry (:constructor make-route-registry ()))
  (routes (make-hash-table :test #'equal))
  (order '())
  (probes (make-hash-table :test #'equal))   ; id -> newest-first list of probe records
  (benches (make-hash-table :test #'equal))  ; id -> benched-until stamp while live
  (classes (make-hash-table :test #'equal))  ; card class -> declared ordered route ids
  (scope-revision 0))

(defun route-member (registry id)
  "The configured route named by ID, or NIL."
  (gethash id (route-registry-routes registry)))

(defun route-members (registry)
  "The live routes in configured order."
  (loop for id in (reverse (route-registry-order registry))
        collect (gethash id (route-registry-routes registry))))

(defun route-member-count (registry)
  (hash-table-count (route-registry-routes registry)))

;;; ------------------------------------------------------------------
;;; The credential rule: a key is a location, never a value
;;; ------------------------------------------------------------------

(defun route-key-location-p (key-location)
  "A key location is `(:path \"<path>\")` or `(:env \"<name>\")`: a named place
the harness resolves, never a key value (SPEC-WORK.md:3762-3765)."
  (and (consp key-location)
       (member (car key-location) '(:path :env))
       (stringp (second key-location))
       (plusp (length (second key-location)))
       (null (cddr key-location))))

(defun route-key-location-line (key-location)
  "The key location as the listing prints it: the path or the env name, never a
value. A malformed location is a credential and is refused before it is ever
printed."
  (if (route-key-location-p key-location)
      (format nil "(:~A ~S)"
              (string-downcase (symbol-name (car key-location)))
              (second key-location))
      nil))

(defun route-config-section (route)
  "A route is a `:kind :route` member of the `routes` section of CONFIG
(SPEC-WORK.md:3750-3755)."
  (list :section :routes :kind :route :id (route-id route)))

(defun route-node-field (route)
  "Every route event writes `:node (:absent)`: a route is never a work node."
  (declare (ignore route))
  +absent+)

(defun %route-fail (id reason)
  (values nil
          (format nil "ROUTE FAIL route=~A: ~A" (or id "-") reason)
          1))

(defun route-register (registry &key id provider endpoint key-location plan
                                    cost-per-mtok capabilities owner
                                    (request (format nil "route-~A" id)))
  "Write one `:kind :route` CONFIG member, or refuse whole. A `:key-location`
that is neither a path nor an env name is `credential in record` and the value
is never echoed; an unknown plan or capability value, a metered route without a
cost and a cost on a non-metered route all refuse (SPEC-WORK.md:3762-3774,
6292-6301)."
  (cond
    ((not (route-key-location-p key-location))
     (%route-fail (and (stringp id) id) "credential in record"))
    ((not (member plan *route-plans*))
     (%route-fail id "unknown plan"))
    ((eq plan :metered)
     (if (and (numberp cost-per-mtok) (plusp cost-per-mtok))
         (let ((route (make-route :id id :provider provider :endpoint endpoint
                                  :key-location key-location :plan plan
                                  :cost-per-mtok cost-per-mtok
                                  :capabilities capabilities :owner owner)))
           (setf (gethash id (route-registry-routes registry)) route)
           (push id (route-registry-order registry))
           (values route
                   (format nil "ROUTE OK id=ev-route-~A request=~A route=~A rev=~A pushed=- changed=1 emitted=0"
                           id (or request id) id (route-registry-scope-revision registry))
                   0))
         (%route-fail id "refusing to guess")))
    (cost-per-mtok
     (%route-fail id "cost-per-mtok on a non-metered plan"))
    ((and capabilities
          (not (every (lambda (pair)
                        (and (member (car pair) *route-capability-keys*)
                             (member (cdr pair) *route-capability-values*)))
                      (loop for (k v) on capabilities by #'cddr collect (cons k v)))))
     (%route-fail id "unknown capability value"))
    (t
     (let ((route (make-route :id id :provider provider :endpoint endpoint
                              :key-location key-location :plan plan
                              :capabilities capabilities :owner owner)))
       (setf (gethash id (route-registry-routes registry)) route)
       (push id (route-registry-order registry))
       (values route
               (format nil "ROUTE OK id=~A request=~A route=~A rev=~A pushed=- changed=1 emitted=0"
                       (or request id) request id (route-registry-scope-revision registry))
               0)))))

;;; ------------------------------------------------------------------
;;; Probes: dated ACTIVE evidence, and the bench
;;; ------------------------------------------------------------------

(defun route-probe (registry id &key card pass wall usd at source benched-until)
  "Record one dated probe observation against route ID: ACTIVE evidence that
never touches the route member. PASS is `:true`, `:false` or `:absent`. Three
consecutive abstains bench the route until BENCHED-UNTIL; a pass clears it
(SPEC-WORK.md:3795-3798, 6311-6314)."
  (let ((probe (list :route id :card card :pass pass :wall wall :usd usd
                     :at at :source source)))
    (push probe (gethash id (route-registry-probes registry)))
    (cond
      ((eq pass :true)
       (remhash id (route-registry-benches registry)))
      ((and (eq pass :absent) (>= (route-consecutive-abstains registry id) 3)
            (not (route-benched-p registry id)))
       (setf (gethash id (route-registry-benches registry)) benched-until)))
    probe))

(defun route-probes (registry id)
  "The route's probe records, newest first."
  (copy-list (gethash id (route-registry-probes registry))))

(defun route-newest-probe (registry id)
  (first (gethash id (route-registry-probes registry))))

(defun route-consecutive-abstains (registry id)
  "How many of the newest probes in a row answered `pass=absent`."
  (let ((n 0))
    (dolist (p (gethash id (route-registry-probes registry)) n)
      (if (eq (getf p :pass) :absent) (incf n) (return n)))))

(defun route-passing-p (registry id)
  "T when the route's newest probe record is a pass (SPEC-WORK.md:3792)."
  (let ((p (route-newest-probe registry id)))
    (and p (eq (getf p :pass) :true))))

(defun route-benched-until (registry id)
  (gethash id (route-registry-benches registry)))

(defun route-benched-p (registry id &key now)
  "T while a `:benched-until` is live: the route carries no card until the next
probe passes (SPEC-WORK.md:3795-3798)."
  (let ((until (route-benched-until registry id)))
    (and until
         (or (null now) (fleet-stamp-before-p now until)))))

;;; ------------------------------------------------------------------
;;; The cheapest-first routing projection
;;; ------------------------------------------------------------------

(defun route-plan-rank (plan)
  "flat before free before metered (SPEC-WORK.md:3788)."
  (case plan (:flat 0) (:free 1) (:metered 2) (:local 3) (t 4)))

(defun route-lessp (registry a b)
  "Within one plan by `:cost-per-mtok` ascending; a tie by bytewise stable id."
  (let* ((ra (route-member registry a))
         (rb (route-member registry b))
         (pa (and ra (route-plan-rank (route-plan ra))))
         (pb (and rb (route-plan-rank (route-plan rb)))))
    (cond
      ((and pa pb (/= pa pb)) (< pa pb))
      ((and ra rb (eq :metered (route-plan ra)) (eq :metered (route-plan rb)))
       (let ((ca (or (route-cost-per-mtok ra) 0))
             (cb (or (route-cost-per-mtok rb) 0)))
         (if (= ca cb) (string< a b) (< ca cb))))
      (t (string< a b)))))

(defun route-projection (registry listing &key (now nil))
  "The ordered route list a card class projects: only routes whose newest probe
is a pass and whose bench is not live, cheapest first, each listing one share
(SPEC-WORK.md:3784-3798)."
  (stable-sort
   (remove-if-not (lambda (id)
                    (and (route-member registry id)
                         (route-passing-p registry id)
                         (not (route-benched-p registry id :now now))))
                  (copy-list listing))
   (lambda (a b) (route-lessp registry a b))))

(defun route-class-add (registry class ids)
  "Declare the ordered route list a card class maps to. A repeated route id is
n shares of the class's routing."
  (setf (gethash class (route-registry-classes registry)) (copy-list ids))
  registry)

(defun route-class-routes (registry class)
  (gethash class (route-registry-classes registry)))

(defun build-route-card (registry id &key now)
  "The card builder for one route: a passing, unbenched route carries its card;
an unprobed or benched route carries none, never a warning (SPEC-WORK.md:3792)."
  (if (and (route-member registry id)
           (route-passing-p registry id)
           (not (route-benched-p registry id :now now)))
      (values (list :card id) nil)
      (values nil (format nil "no card for ~A: unprobed" id))))

;;; ------------------------------------------------------------------
;;; `query --ask routes`
;;; ------------------------------------------------------------------

(defstruct (routes-ask
            (:constructor make-routes-ask
                (&key class rows fail (exit-code 0) (scope-revision 0))))
  class rows fail exit-code scope-revision)

(defun route-row-line (route)
  "One ROUTE listing row: every field but the key value. The key is printed by
its path or env name only (SPEC-WORK.md:6297-6299)."
  (format nil "ROUTE ROW ~A provider=~A endpoint=~A key-location=~A plan=~A cost-per-mtok=~A capabilities=~A owner=~A"
          (route-id route)
          (or (route-provider route) "-")
          (or (route-endpoint route) "-")
          (or (route-key-location-line (route-key-location route)) "-")
          (string-downcase (symbol-name (or (route-plan route) :unknown)))
          (if (route-cost-per-mtok route) (route-cost-per-mtok route) "-")
          (if (route-capabilities route)
              (format nil "~{~A~^,~}"
                      (loop for (k v) on (route-capabilities route) by #'cddr
                            collect (format nil "~A=~A"
                                            (string-downcase (symbol-name k))
                                            (string-downcase (symbol-name v)))))
              "-")
          (or (route-owner route) "-")))

(defun query-routes (registry &key class (scope-revision nil))
  "`query --ask routes`: without CLASS the whole registry is listed, one ROUTE
ROW per live route with the key by path or env name and never its value; with
CLASS the class's ordered route list is projected, cheapest first, only routes
that carry a card (SPEC-WORK.md:2312, 3784-3798, 6292-6314)."
  (let ((rev (or scope-revision (route-registry-scope-revision registry))))
    (if class
        (make-routes-ask :class class
                         :rows (route-projection registry
                                                 (or (route-class-routes registry class) '()))
                         :scope-revision rev)
        (make-routes-ask :class nil
                         :rows (loop for route in (route-members registry)
                                     collect (list :id (route-id route)
                                                   :provider (route-provider route)
                                                   :endpoint (route-endpoint route)
                                                   :key-location (route-key-location route)
                                                   :plan (route-plan route)
                                                   :cost-per-mtok (route-cost-per-mtok route)
                                                   :capabilities (route-capabilities route)
                                                   :owner (route-owner route)
                                                   :line (route-row-line route)))
                         :scope-revision rev)))) 

;;; ------------------------------------------------------------------
;;; The `route` verb (SPEC-WORK.md:2289, 3732-3799, E09 row 4)
;;; ------------------------------------------------------------------
;;;
;;; One verb writes one `:kind :route` CONFIG member of the `routes` section,
;;; or records a dated probe, or retires the record. It is configuration, never
;;; work: `:node` is `(:absent)`, no count, roadmap or required set moves, and
;;; the key is a location the team's own store resolves and never a value.

(defun route-event (request change route)
  "The one `:route` CONFIG event: `:kind :route`, `:node` `(:absent)`, the route
identity as its subject."
  (list :kind :route :change change :node (route-node-field route)
        :route (route-id route)
        :event-id (format nil "ev-route-~A-~A" (route-id route)
                          (string-downcase (symbol-name change)))
        :request (or (getf request :request)
                     (format nil "route-~A" (route-id route)))
        :rev 0 :pushed nil :changed 1 :emitted 0))

(defun route-submit (kernel request)
  "One `:route` event. `:register`, `:retire` and `:probe` are the three
changes the grammar names (SPEC-WORK.md:2289); an unknown change refuses at
exit 2 rather than guessing."
  (let ((registry (kernel-routes kernel))
        (change (getf request :change))
        (id (getf request :route)))
    (case change
      (:register
       (multiple-value-bind (route line code)
           (route-register registry
                           :id id
                           :provider (getf request :provider)
                           :endpoint (getf request :endpoint)
                           :key-location (getf request :key-location)
                           :plan (getf request :plan)
                           :cost-per-mtok (getf request :cost-per-mtok)
                           :capabilities (getf request :capabilities)
                           :owner (getf request :owner)
                           :request (getf request :request))
         (if route
             (values t line code (route-event request change route))
             (values nil line code nil))))
      (:retire
       (let ((route (and id (route-member registry id))))
         (if (null route)
             (values nil (format nil "ROUTE FAIL route=~A: no such route"
                                 (or id "-"))
                     1 nil)
             (progn
               (remhash id (route-registry-routes registry))
               (setf (route-registry-order registry)
                     (remove id (route-registry-order registry) :test #'equal))
               (values t (format nil "ROUTE OK route=~A" id) 0
                       (route-event request change route))))))
      (:probe
       (let ((route (and id (route-member registry id))))
         (if (null route)
             (values nil (format nil "ROUTE FAIL route=~A: no such route"
                                 (or id "-"))
                     1 nil)
             (progn
               (route-probe registry id
                            :card (getf request :card)
                            :pass (getf request :pass)
                            :wall (getf request :wall)
                            :usd (getf request :usd)
                            :source (getf request :source)
                            :benched-until (getf request :benched-until))
               (values t (format nil "PROBE OK route=~A pass=~A"
                                 id
                                 (string-downcase
                                  (princ-to-string (getf request :pass))))
                       0 (route-event request change route))))))
      (otherwise
       (values nil
               (format nil "ROUTE FAIL route=~A: unsupported change ~A"
                       (or id "-")
                       (string-downcase (princ-to-string (or change "?"))))
               2 nil)))))
