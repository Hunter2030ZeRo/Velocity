//go:build windows

package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsCachePolicy_creator_owner_inheritance(t *testing.T) {
	user := currentWindowsUser(t)
	creator := wellKnownWindowsSID(t, windows.WinCreatorOwnerSid)
	everyone := wellKnownWindowsSID(t, windows.WinWorldSid)
	inheritOnly := uint32(windows.INHERIT_ONLY_ACE | windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
	cases := []struct {
		name       string
		sid        *windows.SID
		flags      uint32
		directory  bool
		wantUnsafe bool
	}{
		{"creator owner template", creator, inheritOnly, true, false},
		{"inherited creator owner template", creator, inheritOnly | windows.INHERITED_ACE, true, false},
		{"effective creator owner is not allowlisted", creator, 0, true, true},
		{"creator owner on a file is not allowlisted", creator, inheritOnly, false, true},
		{"Everyone inherit-only write remains unsafe", everyone, inheritOnly, true, true},
		{"Everyone inherited write remains unsafe", everyone, windows.INHERITED_ACE, true, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dacl := windowsACL(t, windowsAccess(windowsAccessSpec{
				test.sid, windows.GENERIC_ALL, windows.GRANT_ACCESS, test.flags &^ windows.INHERITED_ACE,
			}))
			// SetEntriesInAcl expects explicit entries and can discard entries
			// marked inherited. Apply that provenance bit after construction.
			var ace *windows.ACCESS_ALLOWED_ACE
			mustNoError(t, windows.GetAce(dacl, 0, &ace))
			if ace == nil {
				t.Fatal("fixture ACL has no entry")
			}
			ace.Header.AceFlags |= byte(test.flags & windows.INHERITED_ACE)
			err := validateWindowsPolicy(windowsPolicyInput{
				owner: user, user: user, dacl: dacl, directory: test.directory,
			})
			if errors.Is(err, ErrUnsafeCache) != test.wantUnsafe || (!test.wantUnsafe && err != nil) {
				t.Fatalf("policy error = %v, want unsafe = %v", err, test.wantUnsafe)
			}
			if test.wantUnsafe && !strings.Contains(err.Error(), "SID "+test.sid.String()) {
				t.Fatalf("missing rejected SID in diagnostic: %v", err)
			}
		})
	}
}

func TestWindowsCache_creator_owner_parent_download_and_reuse(t *testing.T) {
	parent := t.TempDir()
	user := currentWindowsUser(t)
	creator := wellKnownWindowsSID(t, windows.WinCreatorOwnerSid)
	inherit := uint32(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
	dacl := windowsACL(t,
		windowsAccess(windowsAccessSpec{user, windows.GENERIC_ALL, windows.GRANT_ACCESS, inherit}),
		windowsAccess(windowsAccessSpec{creator, windows.GENERIC_ALL, windows.GRANT_ACCESS, inherit | windows.INHERIT_ONLY_ACE}),
	)
	mustNoError(t, windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil))
	cache := filepath.Join(parent, "velocity")
	mustNoError(t, os.Mkdir(cache, 0o700))
	// Verify the on-disk fixture actually contains the template that used to
	// trigger ErrUnsafeCache, rather than only testing a synthetic ACL.
	descriptor, err := windows.GetNamedSecurityInfo(cache, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	mustNoError(t, err)
	if !strings.Contains(descriptor.String(), ";CO)") {
		t.Fatalf("fixture lost CREATOR OWNER inheritance: %s", descriptor)
	}
	mustNoError(t, validateCacheDirectory(cache))
	content := "verified registry index fixture"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResponse(t, w, content)
	}))
	defer server.Close()
	fetcher := mustNew(t, Config{CacheDir: cache, Concurrency: 1, MaxBytes: 1024, AllowHTTP: true})
	artifact := Artifact{URL: server.URL + "/index.json", SHA256: sha(content)}
	paths, err := fetcher.FetchAll(context.Background(), []Artifact{artifact})
	mustNoError(t, err)
	server.Close()
	// A second request must validate the inherited file owner/DACL and use
	// the verified cache entry without needing the now-offline server.
	cached, err := fetcher.FetchAll(context.Background(), []Artifact{artifact})
	mustNoError(t, err)
	if len(paths) != 1 || len(cached) != 1 || paths[sha(content)] != cached[sha(content)] {
		t.Fatalf("downloaded = %v, cached = %v", paths, cached)
	}
}
