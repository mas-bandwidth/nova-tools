;;;; nova-work.asd --- slice 1 of the nova-work engine.
;;;;
;;;; The spec fixes the engine's language (Common Lisp, docs/SPEC-WORK.md:290)
;;;; and fixes no directory for it. `lisp/nova-work/` with this ASDF system is a
;;;; layout choice for review, beside the Go client's existing `cmd/` and
;;;; `internal/`.

(defsystem "nova-work"
  :description "nova-work slice 1: the internal C/O transition kernel with a write-maintained |O|."
  :author "Rowan <rowan@mas-bandwidth.com>"
  :license "See LICENSE at the repository root"
  :serial t
  :components ((:file "src/package")
               (:file "src/conditions")
               (:file "src/sha256")
               (:file "src/value")
               (:file "src/event")
               (:file "src/state")
               (:file "src/journal")
               (:file "src/kernel")
               (:file "src/state-export")
               (:file "src/fleet")
               (:file "src/replays-8605")
               (:file "src/closed-history")
               (:file "src/replays-slice-05")
               (:file "src/node-verbs")
               (:file "src/replays-applicable-delegation")
               (:file "src/replays-config-and-availability")
               (:file "src/replays-8640")
               (:file "src/replays-closed-history")
               (:file "src/replays-publication")
               (:file "src/replays-8603")
               (:file "src/replays-wire-and-operations")
               (:file "src/replays-operations-undo")
               (:file "src/edit-undo")
               (:file "src/replays-8621")
               (:file "src/replays-render-priority")
               (:file "src/replays-priority-and-export")
               (:file "src/replays-attempts-capabilities")
               (:file "src/replays-fleet-assignment")
               (:file "src/assignment")
               (:file "src/control")
               (:file "src/replays-8641")
               (:file "src/replays-8642")
               (:file "src/replays-8643")
               (:file "src/replays-efficiency-goal")))

(defsystem "nova-work/tests"
  :description "The slice-1 acceptance cases, each naming its line of docs/SPEC-WORK.md."
  :depends-on ("nova-work")
  :serial t
  :components ((:file "tests/harness")
               (:file "tests/acceptance")
               (:file "tests/replays-8680")
               (:file "tests/replays-applicable-delegation")
               (:file "tests/replays-8681")
               (:file "tests/replays-8683")
               (:file "tests/replays-8640")
               (:file "tests/replays-8641")
               (:file "tests/replays-8642")
               (:file "tests/replays-8643")
               (:file "tests/replays-8644")))
