//go:build windows

package download

import (
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsCachePolicy_accepts_trusted_write_and_untrusted_read_or_deny(t *testing.T) {
	// Given
	owner := currentWindowsUser(t)
	system := wellKnownWindowsSID(t, windows.WinLocalSystemSid)
	administrators := wellKnownWindowsSID(t, windows.WinBuiltinAdministratorsSid)
	everyone := wellKnownWindowsSID(t, windows.WinWorldSid)
	cases := []struct {
		name  string
		entry windowsAccessSpec
	}{
		{"owner write", windowsAccessSpec{owner, windows.GENERIC_ALL, windows.GRANT_ACCESS, windows.NO_INHERITANCE}},
		{"SYSTEM write", windowsAccessSpec{system, windows.GENERIC_ALL, windows.GRANT_ACCESS, windows.NO_INHERITANCE}},
		{"administrators write", windowsAccessSpec{administrators, windows.GENERIC_ALL, windows.GRANT_ACCESS, windows.NO_INHERITANCE}},
		{"Everyone read", windowsAccessSpec{everyone, windows.GENERIC_READ, windows.GRANT_ACCESS, windows.NO_INHERITANCE}},
		{"Everyone deny", windowsAccessSpec{everyone, windows.GENERIC_ALL, windows.DENY_ACCESS, windows.NO_INHERITANCE}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// When
			err := validateWindowsPolicy(windowsPolicyInput{
				owner: owner, dacl: windowsACL(t, windowsAccess(test.entry)), user: owner, directory: true,
			})
			// Then
			if err != nil {
				t.Fatalf("policy error = %v, want nil", err)
			}
		})
	}
}

func TestWindowsCachePolicy_accepts_process_token_default_owner(t *testing.T) {
	// Given
	user := currentWindowsUser(t)
	defaultOwner := wellKnownWindowsSID(t, windows.WinBuiltinAdministratorsSid)
	dacl := windowsACL(t,
		windowsAccess(windowsAccessSpec{defaultOwner, windows.GENERIC_ALL, windows.GRANT_ACCESS, windows.NO_INHERITANCE}),
		windowsAccess(windowsAccessSpec{user, windows.GENERIC_WRITE, windows.GRANT_ACCESS, windows.NO_INHERITANCE}),
	)

	// When
	err := validateWindowsPolicy(windowsPolicyInput{
		owner: defaultOwner, dacl: dacl, user: user, defaultOwner: defaultOwner, directory: true,
	})

	// Then
	if err != nil {
		t.Fatalf("policy error = %v, want nil", err)
	}
}

func TestWindowsCachePolicy_rejects_untrusted_write_masks(t *testing.T) {
	// Given
	owner := currentWindowsUser(t)
	everyone := wellKnownWindowsSID(t, windows.WinWorldSid)
	cases := []struct {
		name        string
		permissions windows.ACCESS_MASK
		directory   bool
	}{
		{"write data", windows.FILE_WRITE_DATA, false},
		{"append data", windows.FILE_APPEND_DATA, false},
		{"delete", windows.DELETE, false},
		{"write DACL", windows.WRITE_DAC, false},
		{"delete child", windowsFileDeleteChild, true},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// When
			entry := windowsAccess(windowsAccessSpec{
				sid:         everyone,
				permissions: test.permissions,
				mode:        windows.GRANT_ACCESS,
				inheritance: windows.NO_INHERITANCE,
			})
			err := validateWindowsPolicy(windowsPolicyInput{
				owner: owner, dacl: windowsACL(t, entry), user: owner, directory: test.directory,
			})

			// Then
			if !errors.Is(err, ErrUnsafeCache) {
				t.Fatalf("policy error = %v, want ErrUnsafeCache", err)
			}
		})
	}
}

func TestWindowsCachePolicy_rejects_wrong_owner_and_null_dacl(t *testing.T) {
	// Given
	owner := currentWindowsUser(t)
	everyone := wellKnownWindowsSID(t, windows.WinWorldSid)
	dacl := windowsACL(t, windowsAccess(windowsAccessSpec{
		owner, windows.GENERIC_ALL, windows.GRANT_ACCESS, windows.NO_INHERITANCE,
	}))

	// When
	ownerErr := validateWindowsPolicy(windowsPolicyInput{owner: owner, dacl: dacl, user: everyone})
	daclErr := validateWindowsPolicy(windowsPolicyInput{owner: owner, user: owner})

	// Then
	if !errors.Is(ownerErr, ErrUnsafeCache) || !errors.Is(daclErr, ErrUnsafeCache) {
		t.Fatalf("owner error = %v, DACL error = %v; want ErrUnsafeCache", ownerErr, daclErr)
	}
}

func TestWindowsCachePolicy_rejects_security_descriptor_api_error(t *testing.T) {
	// Given
	missing := filepath.Join(t.TempDir(), "missing")

	// When
	err := validatePrivateOwner(missing, nil, false)

	// Then
	if !errors.Is(err, ErrUnsafeCache) {
		t.Fatalf("error = %v, want ErrUnsafeCache", err)
	}
}

func currentWindowsUser(t *testing.T) *windows.SID {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if user == nil || user.User.Sid == nil {
		t.Fatal("current process token has no user SID")
		return nil
	}
	return user.User.Sid
}

func wellKnownWindowsSID(t *testing.T, kind windows.WELL_KNOWN_SID_TYPE) *windows.SID {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(kind)
	if err != nil {
		t.Fatal(err)
	}
	if sid == nil {
		t.Fatal("well-known SID is nil")
		return nil
	}
	return sid
}

type windowsAccessSpec struct {
	sid         *windows.SID
	permissions windows.ACCESS_MASK
	mode        windows.ACCESS_MODE
	inheritance uint32
}

func windowsAccess(spec windowsAccessSpec) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: spec.permissions,
		AccessMode:        spec.mode,
		Inheritance:       spec.inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeValue: windows.TrusteeValueFromSID(spec.sid),
		},
	}
}

func windowsACL(t *testing.T, entries ...windows.EXPLICIT_ACCESS) *windows.ACL {
	t.Helper()
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	return dacl
}
