package fn

// DoubledSprintPart, appended to the sprint profile's source, registers the
// lease part a second time: the load must stop (sprint_00_core.lua's
// refuse_registration).
const DoubledSprintPart = "\ndo NS.SP.part('lease', {pre = function() end, cmds = function() end}) end\n"
