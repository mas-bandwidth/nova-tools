# tools/bench-wsl2.ps1 -- the one-step WSL2 bench bootstrap (#1458).
#
# Run this ONCE, from an ELEVATED PowerShell, on the Windows box that is to join
# the fleet as a Linux/WSL2 bench:
#
#     .\tools\bench-wsl2.ps1 -AuthKey <the Tailscale auth key Glenn minted>
#
# The run IS the one approval: a UAC prompt to open it, and a Tailscale auth key
# minted once in the admin console -- reusable, tagged, pre-approved. After that
# the keeper does everything else over ssh: `nova-pulse fleet add`, the registry
# row, `nova-update release adopt --platform linux-amd64`, the Linux runners,
# `nova-pulse fleet standard`. Not twenty hands.
#
# The fleet's public keys -- the half of an ssh keypair that is meant to be
# published -- are carried beside this script in fleet/authorized_keys. They are
# read, never written back, and the file's absence is a refusal, not a guess: no
# key is ever generated or invented here. The Tailscale auth key is the one
# secret at the one approval and it reaches no printed line.
#
# WSL2 is what the box is, per Glenn 2026-09-18: "drop the native windows CI
# runners. WSL only from now on." docs/BENCH-STANDARD-WINDOWS.md is the record of
# what a native Windows bench would have needed and is parked; this script is
# the live host half of the Linux standard.

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$AuthKey,
    [string]$Distro = 'Ubuntu-24.04',
    [string]$User = 'nova',
    [string]$GoVersion = 'go1.26.5'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Root = Split-Path -Parent $PSScriptRoot
$KeysFile = Join-Path $Root 'fleet/authorized_keys'
$Wanted = "linux,X64,threadripper"

# Refuse is the one door. It names what was wrong and how to run the script, and
# exits 2 -- the tool's own refusal -- so a half-run is never read as a green one.
function Refuse([string]$What) {
    Write-Error ("bench-wsl2: {0}; run: tools/bench-wsl2.ps1 -AuthKey <key> from an elevated PowerShell in the repo checkout" -f $What)
    exit 2
}

# Check is the wire the fleet already parses: one CHECK<TAB>name<TAB>value line,
# and nothing else on that line. The fleet standard's probes print this shape.
function Check([string]$Name, [string]$Value) {
    Write-Output ("CHECK`t{0}`t{1}" -f $Name, $Value)
}

# -- preconditions -----------------------------------------------------------

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Refuse 'it must run from an elevated PowerShell: winget, the firewall setting, powercfg and the startup task all need the one UAC approval'
}
if (-not (Test-Path $KeysFile)) {
    Refuse ("the fleet's public keys are not in {0}; authorized_keys inside the distro is written from that file and from nothing else" -f $KeysFile)
}
$GoLine = (Select-String -Path (Join-Path $Root 'go.mod') -Pattern '^go\s+(\d+\.\d+)').Matches.Groups[1].Value
if (-not $GoLine) {
    Refuse "go.mod carries no `go <version>` line, so the toolchain to install cannot be named"
}
if ($GoVersion -notlike "go$GoLine.*") {
    Refuse ("this script installs {0} and go.mod asks for go {1}; a toolchain below go.mod's line refuses the module by name in one line nobody reads" -f $GoVersion, $GoLine)
}

# -- the host: Tailscale, the clock, power, the firewall, .wslconfig, startup --

winget install --id Tailscale.Tailscale -e --source winget --accept-package-agreements --accept-source-agreements
$tailscale = Join-Path ${env:ProgramFiles} 'Tailscale\tailscale.exe'
if (-not (Test-Path $tailscale)) { Refuse "tailscale did not install where winget said it would" }
& $tailscale up "--authkey=$AuthKey" --ssh=false
Check 'tailscale' 'up'

w32tm /config /manualpeerlist:"time.windows.com" /syncfromflags:manual /update
Restart-Service w32time
Check 'w32time' 'time.windows.com'

# Hibernate off is wake-on-LAN's precondition: with Fast Startup a shutdown is a
# hybrid hibernate and most NIC drivers drop the wake-armed state on the way in.
powercfg /h off
Check 'hibernate' 'off'

# The Hyper-V firewall sits in front of the mirrored WSL2 interface, and the
# fleet reaches the distro's sshd over the tailnet on it.
Set-NetFirewallHyperVVMSetting -DefaultInboundAction Allow
Check 'hyperv-firewall' 'allow'

# 75% of RAM, and the mirrored network so the tailnet address lands on an
# interface inside WSL2 -- that is why there is no tailscaled in the distro.
$ramGB = [math]::Round((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1GB)
$memGB = [math]::Floor($ramGB * 0.75)
@"
[wsl2]
memory=${memGB}GB
networkingMode=mirrored
"@ | Set-Content -Encoding ascii (Join-Path $HOME '.wslconfig')
Check 'wslconfig-memory' "${memGB}GB"
Check 'wslconfig-network' 'mirrored'

# The bench has to come back after a reboot with nobody logged in: a startup
# task, as SYSTEM, starts the distro's sshd before any hand touches the box.
$action = New-ScheduledTaskAction -Execute 'wsl.exe' -Argument ("-d {0} -u root -- systemctl start ssh" -f $Distro)
$trigger = New-ScheduledTaskTrigger -AtStartup
$taskPrincipal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName 'nova-bench-sshd' -Action $action -Trigger $trigger -Principal $taskPrincipal -Force | Out-Null
Check 'startup-task' 'nova-bench-sshd'

# -- the distro: user, sudo, systemd, packages, sshd, Go, git, keys -----------

wsl --install -d $Distro --no-launch
Check 'distro' $Distro

# The public keys reach the distro on stdin: the key text is public, and the
# root setup installs it 0600 for the nova user and then removes the copy.
Get-Content -Raw $KeysFile | wsl -d $Distro -u root -- bash -c 'cat > /tmp/fleet_authorized_keys'

$setup = @'
set -e
id -u nova >/dev/null 2>&1 || useradd -m -s /bin/bash nova
printf 'nova ALL=(ALL) NOPASSWD:ALL\n' > /etc/sudoers.d/nova
chmod 440 /etc/sudoers.d/nova
mkdir -p /etc
cat > /etc/wsl.conf <<'CONF'
[boot]
systemd=true
[user]
default=nova
CONF
cat > /etc/environment <<'CONF'
PATH="/home/nova/.local/bin:/home/nova/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
CONF
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y --no-install-recommends openssh-server build-essential sbcl gh redis-tools ca-certificates curl
install -d -m 755 /home/nova/sdk /home/nova/go/bin /home/nova/.ssh
if [ ! -x "/home/nova/sdk/${NOVA_GO}/bin/go" ]; then
  curl -fsSL "https://go.dev/dl/${NOVA_GO}.linux-amd64.tar.gz" -o /tmp/go.tgz
  tar -C /home/nova/sdk -xzf /tmp/go.tgz
  mv /home/nova/sdk/go "/home/nova/sdk/${NOVA_GO}"
  rm -f /tmp/go.tgz
fi
ln -sf "/home/nova/sdk/${NOVA_GO}/bin/go" /home/nova/go/bin/go
install -m 600 -o nova -g nova /tmp/fleet_authorized_keys /home/nova/.ssh/authorized_keys
rm -f /tmp/fleet_authorized_keys
printf '[user]\n\tname = Rowan Claude\n\temail = rowan@mas-bandwidth.com\n' > /home/nova/.gitconfig
chown -R nova:nova /home/nova
systemctl enable --now ssh
'@
$setup | wsl -d $Distro -u root -- env "NOVA_GO=$GoVersion" bash -s

wsl --set-default $Distro
Check 'default-distro' $Distro

# -- what a card will see, not what a person at the console sees ---------------

$goOut = wsl -d $Distro -u $User -- bash -lc 'go version' 2>$null
Check 'go' $goOut
$who = wsl -d $Distro -u $User -- id -un 2>$null
Check 'user' $who
$sshd = wsl -d $Distro -u root -- systemctl is-active ssh 2>$null
Check 'sshd' $sshd

$tailIP = (& $tailscale ip -4 2>$null | Select-Object -First 1)
Check 'tailnet' $tailIP

# The tail is the only green: sshd has to answer on the tailnet address, or the
# keeper has nothing to adopt over.
if (-not $tailIP) { Refuse 'tailscale reported no tailnet address' }
ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 "$User@$tailIP" 'true' 2>$null
if ($LASTEXITCODE -ne 0) {
    Refuse ("sshd did not answer on {0}; the keeper cannot adopt the bench over ssh" -f $tailIP)
}
Check 'ssh' 'answered'

Write-Output ("BENCH-WSL2 OK distro={0} user={1} tailnet={2} labels={3}" -f $Distro, $User, $tailIP, $Wanted)
exit 0
