package download

import (
	"net/http"
	"net/url"
	"testing"
)

func TestConfiguredClient_rejects_redirect_at_ten_hops_after_caller_policy(t *testing.T) {
	// Given
	callerCalls := 0
	source := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		callerCalls++
		return nil
	}}
	configured, err := configuredClient(source, true)
	if err != nil {
		t.Fatalf("configuredClient() error = %v", err)
	}
	request := &http.Request{URL: &url.URL{Scheme: "http", Host: "example.test", Path: "/hop/10"}}
	via := make([]*http.Request, 10)
	for index := range via {
		via[index] = request
	}

	// When
	redirectErr := configured.CheckRedirect(request, via)

	// Then
	if redirectErr == nil || redirectErr.Error() != "stopped after 10 redirects" {
		t.Fatalf("redirect error = %v, want stopped after 10 redirects", redirectErr)
	}
	if callerCalls != 1 {
		t.Fatalf("caller CheckRedirect calls = %d, want 1", callerCalls)
	}
	if sourceErr := source.CheckRedirect(request, nil); sourceErr != nil {
		t.Fatalf("source CheckRedirect() error = %v", sourceErr)
	}
	if callerCalls != 2 {
		t.Fatalf("source CheckRedirect calls = %d, want caller client unchanged", callerCalls)
	}
}
