//go:build windows

package download

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsFileDeleteChild windows.ACCESS_MASK = 0x00000040

type windowsACLHeader struct {
	revision byte
	reserved byte
	size     uint16
	count    uint16
	padding  uint16
}

type windowsPolicyInput struct {
	owner     *windows.SID
	dacl      *windows.ACL
	user      *windows.SID
	directory bool
}

// validatePrivateOwner enforces the Windows cache integrity boundary: the
// current user owns the object, and only that owner, LocalSystem, or BUILTIN
// Administrators may receive write-capable DACL entries.
func validatePrivateOwner(path string, _ os.FileInfo, directory bool) error {
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read cache security descriptor: %w: %w", err, ErrUnsafeCache)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read cache owner: %w: %w", err, ErrUnsafeCache)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current Windows user: %w: %w", err, ErrUnsafeCache)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read cache DACL: %w: %w", err, ErrUnsafeCache)
	}
	if user == nil {
		return fmt.Errorf("current Windows user is absent: %w", ErrUnsafeCache)
	}
	return validateWindowsPolicy(windowsPolicyInput{
		owner:     owner,
		dacl:      dacl,
		user:      user.User.Sid,
		directory: directory,
	})
}

func validateWindowsPolicy(input windowsPolicyInput) error {
	if input.owner == nil || input.user == nil ||
		!input.owner.IsValid() || !input.user.IsValid() || !input.owner.Equals(input.user) {
		return fmt.Errorf("cache owner is not the current user: %w", ErrUnsafeCache)
	}
	if input.dacl == nil {
		return fmt.Errorf("cache DACL is absent: %w", ErrUnsafeCache)
	}
	return validateWindowsDACL(input.dacl, input.owner, input.directory)
}

func validateWindowsDACL(dacl *windows.ACL, owner *windows.SID, directory bool) error {
	// The exported x/sys ACL type intentionally hides its fixed Win32 header.
	header := (*windowsACLHeader)(unsafe.Pointer(dacl)) //nolint:gosec // Audited layout mirrors the Windows ACL header.
	for index := range header.count {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return fmt.Errorf("read cache DACL entry: %w: %w", err, ErrUnsafeCache)
		}
		if ace == nil {
			return fmt.Errorf("cache DACL contains a nil entry: %w", ErrUnsafeCache)
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("unsupported cache DACL entry type %d: %w", ace.Header.AceType, ErrUnsafeCache)
		}
		if !windowsWriteAccess(ace.Mask, directory) {
			continue
		}
		// ACCESS_ALLOWED_ACE stores its variable-length SID at SidStart by API contract.
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) //nolint:gosec // Audited Win32 ACE layout.
		if !windowsTrustedSID(sid, owner) {
			return fmt.Errorf("cache DACL grants write access outside owner or SYSTEM: %w", ErrUnsafeCache)
		}
	}
	return nil
}

func windowsTrustedSID(sid *windows.SID, owner *windows.SID) bool {
	return sid.Equals(owner) ||
		sid.IsWellKnown(windows.WinLocalSystemSid) ||
		sid.IsWellKnown(windows.WinBuiltinAdministratorsSid)
}

func windowsWriteAccess(mask windows.ACCESS_MASK, directory bool) bool {
	write := windows.ACCESS_MASK(
		windows.FILE_WRITE_DATA |
			windows.FILE_APPEND_DATA |
			windows.FILE_WRITE_EA |
			windows.FILE_WRITE_ATTRIBUTES |
			windows.DELETE |
			windows.WRITE_DAC |
			windows.WRITE_OWNER |
			windows.GENERIC_WRITE |
			windows.GENERIC_ALL,
	)
	if directory {
		write |= windowsFileDeleteChild
	}
	return mask&write != 0 || mask&windows.FILE_GENERIC_WRITE == windows.FILE_GENERIC_WRITE
}
