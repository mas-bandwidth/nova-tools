//go:build windows

// Package winacl answers, on Windows, the question a POSIX mode answers elsewhere: is this
// file or directory private to the user who runs the tool. Windows reports every file's
// mode as 0666 (or 0444) whatever its ACL says, so a mode check refuses every key there;
// the ACL is what keeps others out.
package winacl

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OwnerOnly says path is private to this user, by the rule OpenSSH for Windows holds a
// private key to: its owner is this user, SYSTEM or the Administrators group, and every
// access its DACL allows is to one of those three. A deny entry only narrows access and is
// passed over; an allow of any other kind, a missing DACL (everyone has full access) or a
// grant to any other account (Users, Everyone, Authenticated Users, another user) is refused,
// naming the account and the icacls line that makes the file private.
func OwnerOnly(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("the access list of %s could not be read: %w", path, err)
	}
	me, err := currentUser()
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("the owner of %s could not be read: %w", path, err)
	}
	if !trusted(owner, me) {
		return fmt.Errorf("%s is owned by %s, not by this user; run: icacls %q /setowner %%USERNAME%%", path, owner.String(), path)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("the access list of %s could not be read: %w", path, err)
	}
	if dacl == nil {
		return fmt.Errorf("%s has no access list, so everyone has full access; %s", path, remedy(path))
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("entry %d of the access list of %s could not be read: %w", i, path, err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !trusted(sid, me) {
				return fmt.Errorf("%s grants access to %s; only this user, SYSTEM and Administrators may have it; %s", path, sid.String(), remedy(path))
			}
		default:
			return fmt.Errorf("%s has an access entry of type %d this check does not read; %s", path, ace.Header.AceType, remedy(path))
		}
	}
	return nil
}

// remedy is the icacls line that leaves path to this user alone.
func remedy(path string) string {
	return fmt.Sprintf("run: icacls %q /inheritance:r /grant:r %%USERNAME%%:F", path)
}

// currentUser is the SID of the account this process runs as.
func currentUser() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("this process's account could not be read: %w", err)
	}
	return u.User.Sid, nil
}

// trusted is this user, SYSTEM or the Administrators group.
func trusted(sid, me *windows.SID) bool {
	return sid.Equals(me) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid)
}
