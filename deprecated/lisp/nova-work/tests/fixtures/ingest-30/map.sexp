;; EXAMPLE DATA, NOT PRODUCT CONSTANTS: the invented ingest map of the ingest-30
;; fixture (SPEC-WORK.md, "nova-work is the primary source", sections 3 and 10).
;; Three repositories of an invented org, and one skipped. The storage split
;; (nova-tools#3174 part (i)) reads only the declared rows' order: its manifest
;; has one :repositories row per repository that is not :skip, in this order.
(:schema "nova-work-ingest-map-1"
 :org "acme"
 :team ("ada" "brook")
 :poll (:every-seconds 600)
 :repos ((:repo "engine" :disposition :work :work-set "acme/engine")
         (:repo "tools" :disposition :work :work-set "acme/tools")
         (:repo "ideas" :disposition :pool :work-set "acme/ideas" :private :true)
         (:repo "archive" :disposition :skip :reason "invented: a repository nothing is ingested from"))
 :containers ((:id "acme/engine/net" :type :epic :title "Networking"))
 :labels ((:repo "engine" :label "net" :under "acme/engine/net")
          (:repo "engine" :label "bug" :category "bug"))
 :milestones ())
