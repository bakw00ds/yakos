//go:build windows

// Package winsec provides Windows ACL hardening for sensitive files.
//
// SecureFile sets a restrictive DACL on the file at path: Full Control for
// the current user SID only, with inheritance blocked. This replaces the
// default NTFS behaviour of inheriting parent-directory ACLs, which typically
// grant read access to the local Users group — making token files world-
// readable on multi-user Windows workstations.
//
// Implementation notes:
//   - Uses SE_FILE_OBJECT / SetNamedSecurityInfo from golang.org/x/sys/windows.
//   - The DACL contains exactly one ACE: ACCESS_ALLOWED_ACE for the current
//     user with GENERIC_ALL rights and NO_INHERITANCE flags (non-inheriting).
//   - PROTECTED_DACL_SECURITY_INFORMATION is OR'd with DACL_SECURITY_INFORMATION
//     so that parent-directory inheritance is explicitly broken on the file.
package winsec

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// SecureFile restricts the NTFS DACL on path so that only the current user
// has Full Control access. Inherited permissions from parent directories are
// removed. The file must already exist.
//
// Errors:
//   - returns a wrapped error when the Windows security API call fails
func SecureFile(path string) error {
	// Obtain the current user's SID via the process token.
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return fmt.Errorf("winsec: OpenCurrentProcessToken: %w", err)
	}
	defer tok.Close()

	tokenUser, err := tok.GetTokenUser()
	if err != nil {
		return fmt.Errorf("winsec: GetTokenUser: %w", err)
	}
	userSID := tokenUser.User.Sid

	// Build an EXPLICIT_ACCESS entry granting the current user Full Control.
	// AccessMode = GRANT_ACCESS, Inheritance = NO_INHERITANCE.
	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(userSID),
		},
	}

	// Create a new DACL from the single explicit-access entry.
	// mergedACL=nil means start from scratch — no inherited entries survive.
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, nil)
	if err != nil {
		return fmt.Errorf("winsec: ACLFromEntries: %w", err)
	}

	// Apply the DACL. PROTECTED_DACL_SECURITY_INFORMATION tells Windows to
	// ignore parent-directory inheritance, making the DACL self-contained.
	const securityInfo = windows.DACL_SECURITY_INFORMATION |
		windows.PROTECTED_DACL_SECURITY_INFORMATION

	// SetNamedSecurityInfo takes a plain Go string on the x/sys wrapper.
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		securityInfo,
		nil, // owner SID unchanged
		nil, // group SID unchanged
		acl,
		nil, // SACL unchanged
	)
	if err != nil {
		return fmt.Errorf("winsec: SetNamedSecurityInfo(%q): %w", path, err)
	}

	return nil
}
