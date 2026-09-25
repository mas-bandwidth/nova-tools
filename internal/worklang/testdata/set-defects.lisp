;;; The defects the real set does NOT have, in the real set's own shape: an id written
;;; twice, a unit that needs itself, a two-unit cycle, a three-unit cycle, a deadline that
;;; is not an instant, an owner and a lane nothing names, and a member of :units that is
;;; not a unit at all. One fixture per rule would hide the thing that matters -- that
;;; every rule runs over every unit in ONE pass -- so they are all here together.

(work-set "defects"
  :title "one of everything a work set gets wrong"
  :units
  ((unit "a" :needs ("b") :owner "Emma" :lane "work" :title "half of the two-unit cycle")
   (unit "b" :needs ("a") :owner "Nobody" :lane "no-such-lane" :title "the other half")
   (unit "self" :needs ("self") :title "a unit that needs itself")
   (unit "x" :needs ("y") :title "the three-unit cycle")
   (unit "y" :needs ("z") :title "the three-unit cycle")
   (unit "z" :needs ("x") :title "the three-unit cycle")
   (unit "dup" :title "the first one written")
   (unit "dup" :title "the second one, which nobody can address")
   (unit "late" :deadline "next tuesday" :title "a deadline that is not an instant")
   (unit "ghost" :needs ("never-defined") :title "a need no unit of this set defines")
   (unit "" :title "a unit with no id at all")
   (:not-a-unit "a member of :units that is not a unit")))
