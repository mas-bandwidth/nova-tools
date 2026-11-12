# The Windows ccache directory, made sealed in the one call that creates it (tasks/windows-ccache.yml), so no job can
# open it, or anything later made in it, while it is being made: under C:\ProgramData every user may add a folder, and
# a handle a job opened on a folder made open outlives any later seal. Never staged: windows-ccache.yml hands it to
# powershell.exe as -EncodedCommand, after a first line that sets $Dir (the Windows path). The seal is the one
# rp_win_ccache_seal sets: owned by Administrators, nothing inherited, full control for SYSTEM and Administrators, read
# and run for Authenticated Users and Users.
#   CCACHE-DIR <dir> made          made sealed
#   CCACHE-DIR-PLANTED <dir> ...   it was there already, or is not owned by Administrators with nothing inherited (a job
#                                  made it after the play removed it); exit 15, nothing changed
$ErrorActionPreference = 'Stop'
$admins = New-Object Security.Principal.SecurityIdentifier 'S-1-5-32-544'
$ds = New-Object Security.AccessControl.DirectorySecurity
$ds.SetAccessRuleProtection($true, $false)
$ds.SetOwner($admins)
foreach ($g in @(@('S-1-5-18', 'FullControl'), @('S-1-5-32-544', 'FullControl'), @('S-1-5-11', 'ReadAndExecute'), @('S-1-5-32-545', 'ReadAndExecute'))) {
  $ds.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule((New-Object Security.Principal.SecurityIdentifier $g[0]),
    $g[1], 'ContainerInherit,ObjectInherit', 'None', 'Allow')))
}
$was = Test-Path -LiteralPath $Dir
[void][IO.Directory]::CreateDirectory($Dir, $ds)
# CreateDirectory returns a folder that is already there as it is, so a job's folder made in the gap is caught here
$acl = Get-Acl -LiteralPath $Dir
$owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
if ($was -or $owner -ne $admins.Value -or -not $acl.AreAccessRulesProtected) {
  "CCACHE-DIR-PLANTED $Dir owner=$owner protected=$($acl.AreAccessRulesProtected): it was not made by this play and is never used; run the play again (it removes the directory first)"
  exit 15
}
"CCACHE-DIR $Dir made"
