package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

const (
	didHidden   = "did:plc:hiddenaccount0000000000"
	didAllowed  = "did:plc:allowedaccount000000000"
	didMissing  = "did:plc:missingrecord000000000"
	didServer50 = "did:plc:servererror00000000000"
)

// fakeDirectory resolves every DID to the same PDS endpoint. Only LookupDID is
// used by the resolver; the rest satisfy identity.Directory.
type fakeDirectory struct {
	pds string
}

func (d *fakeDirectory) LookupHandle(ctx context.Context, handle syntax.Handle) (*identity.Identity, error) {
	return nil, fmt.Errorf("not implemented")
}

func (d *fakeDirectory) LookupDID(ctx context.Context, did syntax.DID) (*identity.Identity, error) {
	return &identity.Identity{
		DID: did,
		Services: map[string]identity.ServiceEndpoint{
			"atproto_pds": {Type: "AtprotoPersonalDataServer", URL: d.pds},
		},
	}, nil
}

func (d *fakeDirectory) Lookup(ctx context.Context, atid syntax.AtIdentifier) (*identity.Identity, error) {
	return nil, fmt.Errorf("not implemented")
}

func (d *fakeDirectory) Purge(ctx context.Context, atid syntax.AtIdentifier) error {
	return fmt.Errorf("not implemented")
}

// newVisibilityTestServer serves com.atproto.repo.getRecord keyed by the repo
// query param, and counts every request it handles.
func newVisibilityTestServer(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()

	record := func(did string, hide bool) string {
		return fmt.Sprintf(`{"uri":"at://%s/%s/self","cid":"bafyreiatestcid",`+
			`"value":{"$type":"%s","hideFromAlgorithmicRecommendations":%t}}`,
			did, visibilityCollection, visibilityCollection, hide)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/com.atproto.repo.getRecord", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)

		if got := r.URL.Query().Get("collection"); got != visibilityCollection {
			t.Errorf("collection = %q, want %q", got, visibilityCollection)
		}
		if got := r.URL.Query().Get("rkey"); got != visibilityRkey {
			t.Errorf("rkey = %q, want %q", got, visibilityRkey)
		}

		w.Header().Set("Content-Type", "application/json")
		switch repo := r.URL.Query().Get("repo"); repo {
		case didHidden:
			fmt.Fprint(w, record(repo, true))
		case didAllowed:
			fmt.Fprint(w, record(repo, false))
		case didMissing:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"RecordNotFound","message":"Could not locate record"}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"InternalServerError","message":"boom"}`)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestResolver(t *testing.T, ttl time.Duration) (*VisibilityResolver, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := newVisibilityTestServer(t, &hits)
	return NewVisibilityResolver(&fakeDirectory{pds: srv.URL}, srv.Client(), ttl), &hits
}

func TestVisibilityLookup(t *testing.T) {
	resolver, _ := newTestResolver(t, time.Minute)

	tests := []struct {
		name string
		did  string
		want Visibility
	}{
		{"hide flag true", didHidden, VisibilityHidden},
		{"hide flag false", didAllowed, VisibilityAllowed},
		{"record absent", didMissing, VisibilityAllowed},
		{"server error", didServer50, VisibilityUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolver.Lookup(context.Background(), tt.did); got != tt.want {
				t.Errorf("Lookup(%s) = %v, want %v", tt.did, got, tt.want)
			}
		})
	}
}

func TestVisibilityLookupCaching(t *testing.T) {
	resolver, hits := newTestResolver(t, time.Minute)

	if got := resolver.Lookup(context.Background(), didHidden); got != VisibilityHidden {
		t.Fatalf("first Lookup = %v, want %v", got, VisibilityHidden)
	}
	after := hits.Load()
	if after != 1 {
		t.Fatalf("server hits after first lookup = %d, want 1", after)
	}

	if got := resolver.Lookup(context.Background(), didHidden); got != VisibilityHidden {
		t.Fatalf("second Lookup = %v, want %v", got, VisibilityHidden)
	}
	if hits.Load() != after {
		t.Errorf("server hits = %d, want %d (result should come from cache)", hits.Load(), after)
	}
	if resolver.CacheLen() != 1 {
		t.Errorf("CacheLen = %d, want 1", resolver.CacheLen())
	}
}

func TestVisibilityLookupUnknownNotCached(t *testing.T) {
	resolver, hits := newTestResolver(t, time.Minute)

	for i := 1; i <= 2; i++ {
		if got := resolver.Lookup(context.Background(), didServer50); got != VisibilityUnknown {
			t.Fatalf("Lookup #%d = %v, want %v", i, got, VisibilityUnknown)
		}
		if hits.Load() != int64(i) {
			t.Fatalf("server hits after lookup #%d = %d, want %d", i, hits.Load(), i)
		}
	}
	if resolver.CacheLen() != 0 {
		t.Errorf("CacheLen = %d, want 0 (Unknown must not be cached)", resolver.CacheLen())
	}
}

func TestVisibilityLookupInvalidDID(t *testing.T) {
	resolver, hits := newTestResolver(t, time.Minute)

	if got := resolver.Lookup(context.Background(), "not-a-did"); got != VisibilityUnknown {
		t.Errorf("Lookup(invalid) = %v, want %v", got, VisibilityUnknown)
	}
	if hits.Load() != 0 {
		t.Errorf("server hits = %d, want 0 (invalid DID must not reach the PDS)", hits.Load())
	}
}

func TestVisibilityCacheExpiry(t *testing.T) {
	resolver, hits := newTestResolver(t, time.Millisecond)

	if got := resolver.Lookup(context.Background(), didAllowed); got != VisibilityAllowed {
		t.Fatalf("first Lookup = %v, want %v", got, VisibilityAllowed)
	}
	time.Sleep(5 * time.Millisecond)

	if got := resolver.Lookup(context.Background(), didAllowed); got != VisibilityAllowed {
		t.Fatalf("second Lookup = %v, want %v", got, VisibilityAllowed)
	}
	if hits.Load() != 2 {
		t.Errorf("server hits = %d, want 2 (expired entry must be refetched)", hits.Load())
	}
}

func TestVisibilityString(t *testing.T) {
	tests := []struct {
		v    Visibility
		want string
	}{
		{VisibilityUnknown, "unknown"},
		{VisibilityAllowed, "allowed"},
		{VisibilityHidden, "hidden"},
		{Visibility(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("Visibility(%d).String() = %q, want %q", int(tt.v), got, tt.want)
		}
	}
}
