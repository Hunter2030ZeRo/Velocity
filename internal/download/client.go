package download

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

func configuredClient(source *http.Client, allowHTTP bool) (*http.Client, error) {
	var client http.Client
	if source == nil {
		transport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, errors.New("download: default HTTP transport is not configurable")
		}
		clone := transport.Clone()
		clone.MaxIdleConns = 200
		clone.MaxIdleConnsPerHost = 40
		clone.IdleConnTimeout = 90 * time.Second
		clone.ForceAttemptHTTP2 = true
		client = http.Client{Timeout: 30 * time.Second, Transport: clone}
	} else {
		client = *source
	}
	redirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if isInsecureRedirect(request, via, allowHTTP) {
			return fmt.Errorf("redirect to %s: %w", request.URL, ErrInsecureURL)
		}
		if redirect != nil {
			if err := redirect(request, via); err != nil {
				return err
			}
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &client, nil
}

func isInsecureRedirect(request *http.Request, via []*http.Request, allowHTTP bool) bool {
	if !allowHTTP && request.URL.Scheme != "https" {
		return true
	}
	return len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && request.URL.Scheme == "http"
}
