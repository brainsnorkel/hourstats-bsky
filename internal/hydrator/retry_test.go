package hydrator

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/atproto/client"
)

// apiErr builds the error shape indigo returns for a non-2xx XRPC response.
func apiErr(status int) error {
	return &client.APIError{StatusCode: status, Name: "TestError", Message: "synthetic"}
}

func TestHydrateRetriesTransientFailure(t *testing.T) {
	var attempts atomic.Int64
	fetcher := &mockFetcher{
		response: func(uris []string) ([]*bsky.FeedDefs_PostView, error) {
			if attempts.Add(1) == 1 {
				return nil, apiErr(502)
			}
			return makeViews(uris), nil
		},
	}
	h := New(fetcher, &mockUpdater{}, Config{BatchSize: 5, MaxConcurrency: 1, RateLimit: 1000})

	res, err := h.Hydrate(context.Background(), makePosts(5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Hydrated != 5 {
		t.Errorf("Hydrated = %d, want 5 (the retry should recover the batch)", res.Hydrated)
	}
	if res.Errors != 0 {
		t.Errorf("Errors = %d, want 0", res.Errors)
	}
	if res.Retries != 1 {
		t.Errorf("Retries = %d, want 1", res.Retries)
	}
	if res.RateLimited != 0 {
		t.Errorf("RateLimited = %d, want 0", res.RateLimited)
	}
}

func TestHydrateRateLimitedExhaustsRetries(t *testing.T) {
	var attempts atomic.Int64
	fetcher := &mockFetcher{
		response: func(_ []string) ([]*bsky.FeedDefs_PostView, error) {
			attempts.Add(1)
			return nil, apiErr(429)
		},
	}
	h := New(fetcher, &mockUpdater{}, Config{BatchSize: 5, MaxConcurrency: 1, RateLimit: 1000})

	res, err := h.Hydrate(context.Background(), makePosts(5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := attempts.Load(), int64(1+maxRetries); got != want {
		t.Errorf("fetch attempts = %d, want %d", got, want)
	}
	if res.Retries != maxRetries {
		t.Errorf("Retries = %d, want %d", res.Retries, maxRetries)
	}
	if res.RateLimited != 1+maxRetries {
		t.Errorf("RateLimited = %d, want %d", res.RateLimited, 1+maxRetries)
	}
	if res.Errors != 5 {
		t.Errorf("Errors = %d, want 5 (the batch is charged exactly once)", res.Errors)
	}
	if got := res.Hydrated + res.Filtered + res.Errors; got != res.Total {
		t.Errorf("accounting mismatch: hydrated(%d) + filtered(%d) + errors(%d) = %d, want Total = %d",
			res.Hydrated, res.Filtered, res.Errors, got, res.Total)
	}
}

func TestHydrateDoesNotRetryClientError(t *testing.T) {
	var attempts atomic.Int64
	fetcher := &mockFetcher{
		response: func(_ []string) ([]*bsky.FeedDefs_PostView, error) {
			attempts.Add(1)
			return nil, apiErr(400)
		},
	}
	h := New(fetcher, &mockUpdater{}, Config{BatchSize: 5, MaxConcurrency: 1, RateLimit: 1000})

	res, err := h.Hydrate(context.Background(), makePosts(5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("fetch attempts = %d, want 1 (400 is not retryable)", got)
	}
	if res.Retries != 0 {
		t.Errorf("Retries = %d, want 0", res.Retries)
	}
	if res.Errors != 5 {
		t.Errorf("Errors = %d, want 5", res.Errors)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429", apiErr(429), true},
		{"500", apiErr(500), true},
		{"502", apiErr(502), true},
		{"504", apiErr(504), true},
		{"400", apiErr(400), false},
		{"401", apiErr(401), false},
		{"404", apiErr(404), false},
		{"wrapped 503", fmt.Errorf("batch: %w", apiErr(503)), true},
		{"network error", &net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")}, true},
		{"unexpected eof", fmt.Errorf("decode body: %w", io.ErrUnexpectedEOF), true},
		{"plain error", fmt.Errorf("something odd"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable(tt.err); got != tt.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestBackoffForIsJitteredAndGrows(t *testing.T) {
	for attempt := 1; attempt <= maxRetries; attempt++ {
		base := float64(retryBaseBackoff) * math.Pow(2, float64(attempt-1))
		lo := time.Duration(base * (1 - retryJitterFrac))
		hi := time.Duration(base * (1 + retryJitterFrac))
		for i := 0; i < 100; i++ {
			got := backoffFor(attempt)
			if got < lo || got > hi {
				t.Fatalf("backoffFor(%d) = %v, want within [%v, %v]", attempt, got, lo, hi)
			}
		}
	}
}

func TestNewPublicFetcherConfiguresClient(t *testing.T) {
	f := NewPublicFetcher(DefaultPublicHost)

	api, ok := f.client.(*client.APIClient)
	if !ok {
		t.Fatalf("fetcher client is %T, want *client.APIClient", f.client)
	}
	if api.Host != DefaultPublicHost {
		t.Errorf("Host = %q, want %q", api.Host, DefaultPublicHost)
	}
	if api.Client == nil {
		t.Fatal("APIClient.Client is nil")
	}
	if api.Client == http.DefaultClient {
		t.Error("APIClient.Client is http.DefaultClient, want a client with a request timeout")
	}
	if api.Client.Timeout != 15*time.Second {
		t.Errorf("Timeout = %v, want 15s", api.Client.Timeout)
	}

	transport, ok := api.Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is %T, want *http.Transport", api.Client.Transport)
	}
	if transport.MaxIdleConns != 64 {
		t.Errorf("MaxIdleConns = %d, want 64", transport.MaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != 16 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 16", transport.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", transport.IdleConnTimeout)
	}
	// Cloned from http.DefaultTransport, so the dial/TLS defaults survive.
	if transport.TLSHandshakeTimeout == 0 {
		t.Error("TLSHandshakeTimeout = 0, want the http.DefaultTransport default")
	}
}
