;;;; s9-routes.lisp --- the route registry: how a model is run, remembered by the
;;;; tool so a team need not remember it.
;;;;
;;;; docs/SPEC-WORK.md "Model routes" (#500, #691). A route is a `:kind :route`
;;;; member of a `routes` section of CONFIG beside the fleet; its key location is
;;;; a path or an env name and never a value; routing is a projection, cheapest
;;;; first, flat before free before metered; a route with no passing probe
;;;; carries no card; three consecutive abstains bench it until the next probe.
;;;;
;;;; Pure functions and records, not wired into the command thread.

(in-package #:nova-work)

;;; ------------------------------------------------------------------ records

(defstruct (s9-route (:conc-name route-))
  (id nil)             ; `provider/model`; never reused, never display text
  (provider nil)
  (endpoint nil)
  (key-location nil)   ; (:path "<p>") or (:env "<name>"), never a key value
  (plan nil)           ; one of :flat, :metered, :free, :local
  (cost-per-mtok nil)  ; present only on :metered
  (capabilities nil)   ; (:text yes|no :code yes|no :tool-calls yes|no)
  (owner nil)
  (probe-record nil)   ; the newest dated probe: (:card :pass :wall :usd :at :source)
  (abstains 0)         ; consecutive abstains
  (benched-until nil)  ; the date a benched route may be tried again
  (retired-p nil))

(defstruct (s9-routes (:conc-name routes-) (:constructor %make-s9-routes))
  (routes (make-hash-table :test #'equal))  ; id -> s9-route
  (order '())
  (classes (make-hash-table :test #'equal)) ; class -> list of ids (shares)
  (friends '())
  (revision 0))

(defun make-s9-routes (&key friends classes)
  (let ((r (%make-s9-routes :friends (or friends '()))))
    (loop for (class ids) on classes by #'cddr
          do (setf (gethash class (routes-classes r)) ids))
    r))

(defun routes-live-routes (registry)
  (remove-if #'route-retired-p
             (mapcar (lambda (id) (gethash id (routes-routes registry)))
                     (routes-order registry))))

(defun routes-find-route (registry id)
  (gethash id (routes-routes registry)))

(defun routes-generate-id (registry)
  (incf (routes-revision registry)))

;;; -------------------------------------------------------------- validation

(defun s9-key-location-p (kl)
  (and (consp kl)
       (= 2 (length kl))
       (member (first kl) '(:path :env))
       (stringp (second kl))))

(defun s9-key-location-name (kl)
  "The path or env name a route lists — the location, never a value."
  (second kl))

(defun s9-valid-plan-p (plan)
  (member plan '(:flat :metered :free :local)))

(defun s9-valid-capabilities-p (caps)
  (and (listp caps)
       (evenp (length caps))
       (loop for (k v) on caps by #'cddr
             always (and (member k '(:text :code :tool-calls))
                         (member v '(:yes :no))))))

(defun s9-route-refusal (registry request)
  (let* ((id (getf request :id))
         (owner (getf request :owner))
         (kl (getf request :key-location))
         (plan (getf request :plan))
         (cost (getf request :cost-per-mtok))
         (caps (getf request :capabilities)))
    (cond
      ((or (null id) (and (stringp id) (string= id ""))) (values "no id" "-"))
      ((or (null owner) (and (stringp owner) (string= owner ""))) (values "no owner" id))
      ((not (member owner (routes-friends registry) :test #'string=))
       (values "unknown owner" id))
      ((not (s9-key-location-p kl)) (values "credential in record" id))
      ((not (s9-valid-plan-p plan)) (values "unknown plan" id))
      ((and (eq plan :metered) (null cost))
       (values "a metered route with no cost-per-mtok" id))
      ((and (not (eq plan :metered)) cost)
       (values "cost-per-mtok on a non-metered route" id))
      ((not (s9-valid-capabilities-p caps)) (values "unknown capability" id)))))

;;; ----------------------------------------------------------------- the verb

(defun route-register (registry request)
  "`nova-work route --register <id> --provider --endpoint --key-location --plan
  [--cost-per-mtok] --capabilities --owner --reason`. One `:route` event, subject
  `:node (:absent)`. Answers (values ok-p line registry route)."
  (multiple-value-bind (reason id) (s9-route-refusal registry request)
    (when reason
      (return-from route-register
        (values nil (format nil "ROUTE FAIL route=~A: ~A" id reason)
                registry nil))))
  (let ((id (getf request :id)))
    (when (routes-find-route registry id)
      (return-from route-register
        (values nil (format nil "ROUTE FAIL route=~A: duplicate id" id) registry nil)))
    (let* ((route (make-s9-route
                   :id id
                   :provider (getf request :provider)
                   :endpoint (getf request :endpoint)
                   :key-location (getf request :key-location)
                   :plan (getf request :plan)
                   :cost-per-mtok (getf request :cost-per-mtok)
                   :capabilities (getf request :capabilities)
                   :owner (getf request :owner)
                   :probe-record nil
                   :abstains 0
                   :benched-until nil
                   :retired-p nil))
           (rev (routes-generate-id registry)))
      (setf (gethash id (routes-routes registry)) route)
      (setf (routes-order registry) (append (routes-order registry) (list id)))
      (values t
              (format nil "ROUTE OK id=~A request=~A route=~A change=register rev=~D pushed=- changed=~D"
                      rev (getf request :request) id rev 1)
              registry route))))

(defun route-retire (registry id request)
  (let ((route (routes-find-route registry id)))
    (unless route
      (return-from route-retire
        (values nil (format nil "ROUTE FAIL route=~A: no id" id) registry nil)))
    (setf (route-retired-p route) t)
    (let ((rev (routes-generate-id registry)))
      (values t
              (format nil "ROUTE OK id=~A request=~A route=~A change=retire rev=~D pushed=- changed=~D"
                      rev (getf request :request) id rev 1)
              registry route))))

(defun route-probe (registry request)
  "`nova-work route --probe <id> --card --pass --wall --usd --source`. A probe is
  ACTIVE evidence with its date, source and last-contact stamp; it never writes
  CONFIG (no :endpoint, :key-location, :plan, :cost-per-mtok, :capabilities or
  :owner change). Three consecutive abstains bench the route; a passing probe
  clears the bench. Answers (values ok-p line PROBE-line)."
  (let ((id (getf request :id)))
    (let ((route (routes-find-route registry id)))
      (unless route
        (return-from route-probe
          (values nil (format nil "ROUTE FAIL route=~A: no id" id))))
      (let ((pass (getf request :pass))
            (at (getf request :at)))
        (setf (route-probe-record route)
              (list :card (getf request :card)
                    :pass pass
                    :wall (getf request :wall)
                    :usd (getf request :usd)
                    :at at
                    :source (getf request :source)))
        (ecase pass
          (:true
           (setf (route-abstains route) 0
                 (route-benched-until route) nil))
          (:false
           (setf (route-abstains route) 0))
          (:absent
           (incf (route-abstains route))
           (when (>= (route-abstains route) 3)
             (setf (route-benched-until route) at))))
        (values t
                (format nil "PROBE OK route=~A card=~A pass=~A wall=~A usd=~A at=~A source=~A"
                        id
                        (or (getf request :card) "-")
                        (if pass (string-downcase (symbol-name pass)) "-")
                        (or (getf request :wall) "-")
                        (or (getf request :usd) "-")
                        (or (getf request :at) "-")
                        (or (getf request :source) "-")))))))

;;; ------------------------------------------------------------- projection

(defun s9-plan-rank (plan)
  (case plan (:flat 0) (:free 1) (:metered 2) (:local 3) (t 3)))

(defun s9-route-passes-p (route &key now)
  "The route carries a real card only when its newest probe is a pass and its
  bench is not live. An unprobed or benched route is absent, never warned."
  (let ((probe (route-probe-record route)))
    (and probe
         (eq :true (getf probe :pass))
         (not (and (route-benched-until route)
                   (or (null now)
                       (s9-stamp< now (route-benched-until route))))))))

(defun s9-route-cost (route)
  (or (route-cost-per-mtok route) most-positive-fixnum))

(defun s9-route-lessp (a b)
  (let ((ra (s9-plan-rank (route-plan a)))
        (rb (s9-plan-rank (route-plan b))))
    (cond ((< ra rb) t)
          ((> ra rb) nil)
          ((< (s9-route-cost a) (s9-route-cost b)) t)
          ((> (s9-route-cost a) (s9-route-cost b)) nil)
          (t (string< (route-id a) (route-id b))))))

(defun route-projection (registry class &key now)
  "The ordered route list for a card class: a projection of the registry at a
  named scope revision, cheapest first, each admitted route carrying a passing
  probe. A route listed n times in the class list is n shares. Answers
  (values ok-p line route-ids)."
  (let ((ids (gethash class (routes-classes registry))))
    (unless ids
      (return-from route-projection
        (values nil (format nil "QUERY FAIL ask=routes class=~A: no such class" class) '())))
    (let* ((routes (mapcar (lambda (id) (routes-find-route registry id)) ids))
           (admitted (remove-if-not (lambda (r) (and r (s9-route-passes-p r :now now)))
                                    routes)))
      (values t
              (format nil "QUERY OK ask=routes class=~A rows=~D shown=~D"
                      class (length admitted) (length admitted))
              (mapcar #'route-id (sort (copy-list admitted) #'s9-route-lessp))))))

;;; ----------------------------------------------------------------- listing

(defun s9-route-row (route)
  "One `QUERY ROW` per route. The key location is printed as a path or env name
  and never a value."
  (let ((probe (route-probe-record route)))
    (format nil "QUERY ROW ~A kind=route provider=~A endpoint=~A key-location=~A plan=~A cost-per-mtok=~A capabilities=~A owner=~A probe=~A at=~A benched-until=~A"
            (route-id route)
            (or (route-provider route) "-")
            (or (route-endpoint route) "-")
            (or (s9-key-location-name (route-key-location route)) "-")
            (if (route-plan route) (string-downcase (symbol-name (route-plan route))) "-")
            (or (route-cost-per-mtok route) "-")
            (format nil "~A,~A,~A"
                    (getf (route-capabilities route) :text)
                    (getf (route-capabilities route) :code)
                    (getf (route-capabilities route) :tool-calls))
            (or (route-owner route) "-")
            (if probe
                (if (getf probe :pass)
                    (string-downcase (symbol-name (getf probe :pass)))
                    "-")
                "-")
            (or (getf probe :at) "-")
            (or (route-benched-until route) "-"))))

(defun routes-query (registry &key class now)
  "`query --ask routes` lists the registry; `--class <class>` emits the ordered
  projection, cheapest first, each row a passing probe. Answers
  (values ok-p line rows)."
  (if class
      (multiple-value-bind (ok line ids) (route-projection registry class :now now)
        (if (not ok)
            (values nil line '())
            (values t line
                    (mapcar (lambda (id)
                              (s9-route-row (routes-find-route registry id)))
                            ids))))
      (let ((routes (routes-live-routes registry)))
        (values t
                (format nil "QUERY OK ask=routes rows=~D shown=~D"
                        (length routes) (length routes))
                (mapcar #'s9-route-row routes)))))
