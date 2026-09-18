#!/bin/bash
# setup-fleet-mac.sh: first-touch setup of a Mac joining the nova fleet. Run ONCE at the keyboard from an admin account:
#
#     bash /Volumes/<your-flash-drive>/setup-fleet-mac.sh            (keeps the current computer name)
#     bash /Volumes/<your-flash-drive>/setup-fleet-mac.sh robin      (also names the machine "robin")
#
# Works from any directory. It asks for YOUR password once (sudo) and for a NEW password for the fleet user "nova"
# (any password: it is only a fallback, SSH uses the key below). Contains no secrets: the key is a PUBLIC key.
# After it finishes: turn on Remote Login in System Settings (it tells you where), then send Rowan the last two lines.
set -u
KEY='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJRwVwAcMQLglwGZyd2dLO1QMYC91gKGqfqlHpNpFTzY glenn@studio.local'
NAME="${1:-}"
say() { printf '\n==> %s\n' "$*"; }

say "checking for admin rights (sudo will ask for your password)"
sudo -n true 2>/dev/null || sudo -v || { echo "this account cannot sudo; run from an admin account"; exit 1; }

if [ -n "$NAME" ]; then
  say "naming this machine: $NAME"
  sudo scutil --set LocalHostName "$NAME"; sudo scutil --set ComputerName "$NAME"; sudo scutil --set HostName "$NAME"
fi

if id nova >/dev/null 2>&1; then
  say "user nova already exists: skipping"
else
  say "creating the fleet user nova (admin). Type a NEW password for nova when asked"
  sudo sysadminctl -addUser nova -fullName "Nova Fleet" -admin -password - || { echo "could not create nova"; exit 1; }
fi

say "passwordless sudo for nova (same as the Linux benches)"
echo 'nova ALL=(ALL) NOPASSWD: ALL' | sudo tee /etc/sudoers.d/nova >/dev/null
sudo chmod 440 /etc/sudoers.d/nova
sudo visudo -cf /etc/sudoers.d/nova || { echo "sudoers file did not validate; removing it"; sudo rm -f /etc/sudoers.d/nova; exit 1; }

say "installing the Studio's public SSH key for nova"
sudo mkdir -p /Users/nova/.ssh
sudo touch /Users/nova/.ssh/authorized_keys
# append, never overwrite: a second run, or a nova account that already had keys, must keep them (Stella, #1263 F22)
sudo grep -qF "$KEY" /Users/nova/.ssh/authorized_keys || echo "$KEY" | sudo tee -a /Users/nova/.ssh/authorized_keys >/dev/null
sudo chown -R nova:staff /Users/nova/.ssh; sudo chmod 700 /Users/nova/.ssh; sudo chmod 600 /Users/nova/.ssh/authorized_keys

say "power settings: never sleep, display off after 1 min, restart after power loss, wake on network"
sudo pmset -a sleep 0 disksleep 0 displaysleep 1 womp 1 autorestart 1

say "Remote Login: trying from the command line"
if sudo systemsetup -setremotelogin on 2>&1 | grep -qi 'full disk'; then
  echo "    macOS refused (needs Full Disk Access). Do it in the GUI instead:"
  echo "    System Settings > General > Sharing > Remote Login: ON, then (i) > Allow access for: All users"
fi
sudo systemsetup -getremotelogin 2>/dev/null

say "done. Plug in Ethernet if you can. Send Rowan these two lines:"
scutil --get LocalHostName
ipconfig getifaddr en0 || ipconfig getifaddr en1
