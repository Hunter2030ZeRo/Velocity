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

type windowsTokenOwner struct {
	owner *windows.SID
}

type windowsPolicyInput struct {
	owner        *windows.SID
	dacl         *windows.ACL
	user         *windows.SID
	defaultOwner *windows.SID
	directory    bool
}

// validatePrivateOwner enforces the Windows cache integrity boundary: the
// object is owned by the current user or the process token's default owner,
// and only that owner, the current user, LocalSystem, or BUILTIN
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
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current Windows user: %w: %w", err, ErrUnsafeCache)
	}
	defaultOwner, err := windowsDefaultOwner(token)
	if err != nil {
		return fmt.Errorf("read current Windows default owner: %w: %w", err, ErrUnsafeCache)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read cache DACL: %w: %w", err, ErrUnsafeCache)
	}
	if user == nil || user.User.Sid == nil {
		return fmt.Errorf("current Windows user is absent: %w", ErrUnsafeCache)
	}
	return validateWindowsPolicy(windowsPolicyInput{
		owner:        owner,
		dacl:         dacl,
		user:         user.User.Sid,
		defaultOwner: defaultOwner,
		directory:    directory,
	})
}

func windowsDefaultOwner(token windows.Token) (*windows.SID, error) {
	size := uint32(64)
	for {
		buffer := make([]byte, size)
		var required uint32
		err := windows.GetTokenInformation(
			token,
			windows.TokenOwner,
			&buffer[0],
			uint32(len(buffer)),
			&required,
		)
		if err == nil {
			information := (*windowsTokenOwner)(unsafe.Pointer(&buffer[0])) //nolint:gosec // Mirrors the TOKEN_OWNER pointer layout.
			if information.owner == nil || !information.owner.IsValid() {
				return nil, fmt.Errorf("Windows token default owner is absent or invalid")
			}
			owner, copyErr := information.owner.Copy()
			if copyErr != nil {
				return nil, fmt.Errorf("copy Windows token default owner: %w", copyErr)
			}
			return owner, nil
		}
		if err != windows.ERROR_INSUFFICIENT_BUFFER || required <= uint32(len(buffer)) {
			return nil, err
		}
		size = required
	}
}

func validateWindowsPolicy(input windowsPolicyInput) error {
	if input.owner == nil || input.user == nil || !input.owner.IsValid() || !input.user.IsValid() {
		return fmt.Errorf("cache owner or current user is invalid: %w", ErrUnsafeCache)
	}
	ownedByUser := input.owner.Equals(input.user)
	ownedByTokenDefault := input.defaultOwner != nil && input.defaultOwner.IsValid() && input.owner.Equals(input.defaultOwner)
	if !ownedByUser && !ownedByTokenDefault {
		return fmt.Errorf("cache owner is not trusted by the current process token: %w", ErrUnsafeCache)
	}
	if input.dacl == nil {
		return fmt.Errorf("cache DACL is absent: %w", ErrUnsafeCache)
	}
	return validateWindowsDACL(input.dacl, input.owner, input.user, input.directory)
}

func validateWindowsDACL(dacl *windows.ACL, owner *windows.SID, user *windows.SID, directory bool) error {
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
		if !windowsTrustedSID(sid, owner, user) {
			return fmt.Errorf("cache DACL grants write access outside current user, owner, SYSTEM, or Administrators: %w", ErrUnsafeCache)
		}
	}
	return nil
}

func windowsTrustedSID(sid *windows.SID, owner *windows.SID, user *windows.SID) bool {
	if sid == nil || !sid.IsValid() {
		return false
	}
	return sid.Equals(owner) ||
		sid.Equals(user) ||
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
