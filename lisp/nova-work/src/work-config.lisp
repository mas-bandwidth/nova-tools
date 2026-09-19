;;;; work-config.lisp --- the CONFIG the session holds beside the work tree.
;;;;
;;;; docs/SPEC-WORK.md:1056-1058 rules the `friends`, `models`, `fleet` and
;;;; `routes` sections "indexes in the resident model under the one writer and
;;;; the one journal", no node kind of *The data*. docs/SPEC-WORK.md:327 orders
;;;; a mutation journal-durable THEN applied, and :2620-2624 makes a mutation
;;;; outside the command loop a defect.
;;;;
;;;; This file is where that CONFIG lives so it can be carried by the state and
;;;; applied by the one writer: the fleet, the model routes and the ACTIVE
;;;; allocation registry, each deep-copied so a candidate's mutation cannot
;;;; reach the installed state (SPEC-WORK.md:307, the all-or-none apply). The
;;;; three container structs and the records the verbs mutate in place -- the
;;;; `machine` `%machine-change` edits, the `fleet-allocator`
;;;; `%sync-machine-allocator` moves, and the `allocation-record` `fleet-release`
;;;; frees -- are copied field by field, not shared.

(in-package #:nova-work)

(defstruct (work-config (:constructor %make-work-config (&key fleet routes allocations)))
  "The CONFIG the session holds: the fleet section, the model routes section and
the fleet's ACTIVE allocation registry. It rides on the work state so the one
writer applies and the one journal replays it (SPEC-WORK.md:1056-1058)."
  fleet routes allocations)

(defun make-empty-work-config ()
  "A CONFIG with an empty fleet, an empty route registry and an empty ACTIVE
allocation registry, as a session starts before any `machine` or `route` writes."
  (%make-work-config :fleet (make-fleet)
                     :routes (make-route-registry)
                     :allocations (make-fleet-registry)))

;;; The fleet section.

(defun %copy-machine (machine)
  (and machine
       (%make-machine :id (machine-id machine)
                      :name (machine-name machine)
                      :owner (machine-owner machine)
                      :connect (machine-connect machine)
                      :roles (copy-list (machine-roles machine))
                      :permits (copy-list (machine-permits machine))
                      :excludes (copy-list (machine-excludes machine))
                      :limits (copy-list (machine-limits machine))
                      :facts (copy-tree (machine-facts machine)))))

(defun %copy-fleet (fleet)
  (let ((new (%make-fleet)))
    (setf (fleet-friends new) (copy-list (fleet-friends fleet)))
    (maphash (lambda (id member)
               (setf (gethash id (fleet-machines new)) (%copy-machine member)))
             (fleet-machines fleet))
    (setf (fleet-order new) (copy-list (fleet-order fleet)))
    new))

;;; The routes section.

(defun %copy-route (route)
  (and route
       (make-route :id (route-id route)
                   :provider (route-provider route)
                   :endpoint (route-endpoint route)
                   :key-location (copy-tree (route-key-location route))
                   :plan (route-plan route)
                   :cost-per-mtok (route-cost-per-mtok route)
                   :capabilities (copy-tree (route-capabilities route))
                   :owner (route-owner route))))

(defun %copy-route-registry (registry)
  (let ((new (make-route-registry)))
    (maphash (lambda (id route)
               (setf (gethash id (route-registry-routes new)) (%copy-route route)))
             (route-registry-routes registry))
    (setf (route-registry-order new) (copy-list (route-registry-order registry)))
    (maphash (lambda (id probes)
               (setf (gethash id (route-registry-probes new)) (copy-tree probes)))
             (route-registry-probes registry))
    (maphash (lambda (id until)
               (setf (gethash id (route-registry-benches new)) until))
             (route-registry-benches registry))
    (maphash (lambda (class routes)
               (setf (gethash class (route-registry-classes new)) (copy-list routes)))
             (route-registry-classes registry))
    (setf (route-registry-scope-revision new)
          (route-registry-scope-revision registry))
    new))

;;; The ACTIVE allocation registry.

(defun %copy-fleet-allocator (allocator)
  (let ((new (make-fleet-allocator
              :machine-id (fleet-allocator-machine-id allocator)
              :name (fleet-allocator-name allocator)
              :owner (fleet-allocator-owner allocator)
              :aliases (copy-list (fleet-allocator-aliases allocator))
              :concurrent (fleet-allocator-concurrent allocator)
              :cores (fleet-allocator-cores allocator)
              :generation (fleet-allocator-generation allocator)
              :facts (copy-tree (fleet-allocator-facts allocator))
              :limits (copy-tree (fleet-allocator-limits allocator))
              :connect (fleet-allocator-connect allocator)
              :roles (copy-list (fleet-allocator-roles allocator)))))
    (setf (fleet-allocator-allocations new)
          (mapcar #'copy-allocation-record (fleet-allocator-allocations allocator)))
    (setf (fleet-allocator-observations new)
          (copy-tree (fleet-allocator-observations allocator)))
    (maphash (lambda (request record)
               (setf (gethash request (fleet-allocator-requests new))
                     (copy-tree record)))
             (fleet-allocator-requests allocator))
    (setf (fleet-allocator-reduced-p new) (fleet-allocator-reduced-p allocator))
    new))

(defun %copy-fleet-registry (registry)
  (let ((new (make-fleet-registry)))
    (maphash (lambda (id allocator)
               (setf (gethash id (fleet-registry-allocators new))
                     (%copy-fleet-allocator allocator)))
             (fleet-registry-allocators registry))
    (maphash (lambda (alias id)
               (setf (gethash alias (fleet-registry-aliases new)) id))
             (fleet-registry-aliases registry))
    (setf (fleet-registry-events new) (fleet-registry-events registry))
    new))

(defun copy-work-config (config)
  "A deep copy of CONFIG: every container, its hash tables and lists, and the
records the verbs mutate in place are copied, never shared with CONFIG."
  (%make-work-config :fleet (%copy-fleet (work-config-fleet config))
                     :routes (%copy-route-registry (work-config-routes config))
                     :allocations (%copy-fleet-registry (work-config-allocations config))))
