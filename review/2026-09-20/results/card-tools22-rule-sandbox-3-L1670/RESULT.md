RESULT tools22-rule-sandbox-3-L1670 sha=5298f6be12ea
GAP cmd/nova-sandbox/runwin_windows.go:77
SPEC docs/SPEC-SANDBOX.md:1670 rule 3
PKG internal/sandbox
ASK A Windows launch that calls InitializeProcThreadAttributeList, then UpdateProcThreadAttribute(PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES) with a SECURITY_CAPABILITIES carrying an AppContainerSid (CreateWellKnownSid) and WinCapabilityInternetClientSid in its capability array (omitted under --net-deny), then CreateProcessW with EXTENDED_STARTUPINFO_PRESENT, and waits.
GREPS:
  grep -rn "InitializeProcThreadAttributeList\|UpdateProcThreadAttribute\|PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES\|SECURITY_CAPABILITIES\|AppContainerSid\|WinCapabilityInternetClient\|CreateWellKnownSid\|CreateProcessW\|EXTENDED_STARTUPINFO_PRESENT\|net-deny\|NetDeny" --include='*.go' internal/sandbox/ cmd/nova-sandbox/ -> 35+ matches
  grep -rn "procThreadAttributeSecurityCapabilities\|updateAttribute.*SecurityCapabilities" --include='*.go' . -> 1 match: the const declaration only
  ls internal/sandbox/ -> no *_windows.go; wrap_other.go handles windows (build tag !darwin && !linux)
DECIDING LINES:
  cmd/nova-sandbox/runwin_windows.go:77  — procThreadAttributeSecurityCapabilities defined but NEVER passed to updateAttribute
  cmd/nova-sandbox/runwin_windows.go:316-327 — attribute list created with 2 slots; only procThreadAttributeJobList filled (line 325), the security-capabilities slot is "reserved" (comment says "one-line addition rather than a restructure")
  cmd/nova-sandbox/runwin_windows.go:186-204 — winWallAvailable() returns false because sandbox.Available() is wrap_other.go's ("", false); the AppContainer body is "not built"
  cmd/nova-sandbox/runwin_windows.go:360-371 — CreateProcessW with EXTENDED_STARTUPINFO_PRESENT IS implemented
  cmd/nova-sandbox/runwin_windows.go:399-413 — WaitForSingleObject wait IS implemented
  internal/sandbox/wrap_other.go:28 — Available() returns ("", false) on windows
UNGUARDED — no test checks that procThreadAttributeSecurityCapabilities is actually passed to updateAttribute, or that the SECURITY_CAPABILITIES structure carries the right SID and capability. TestTheWin32BodyNeverPermitsBreakaway (runwin_test.go:987) checks the job-list side of W3 but not the security-capabilities side. TestWindowsRefusesWhileTheWallIsNotBuilt (runwin_test.go:969) checks the refusal before Start, not the contents of Start.
Left owed: the SECURITY_CAPABILITIES structure itself, the AppContainerSID and capability array with CreateWellKnownSid, the --net-deny conditional, and a test that would go red if any of these stopped being set.
git status --short