package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	atclient "github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

// Visibility is the result of resolving an account's Content Visibility
// Declaration (app.bsky.actor.contentVisibilityDeclaration).
type Visibility int

const (
	// VisibilityUnknown means the lookup failed; the caller decides what to do.
	VisibilityUnknown Visibility = iota
	// VisibilityAllowed means the record is absent or the flag is false.
	VisibilityAllowed
	// VisibilityHidden means hideFromAlgorithmicRecommendations is true.
	VisibilityHidden
)

func (v Visibility) String() string {
	switch v {
	case VisibilityAllowed:
		return "allowed"
	case VisibilityHidden:
		return "hidden"
	default:
		return "unknown"
	}
}

const (
	// The declaration is a single record with a literal rkey of "self".
	visibilityCollection = "app.bsky.actor.contentVisibilityDeclaration"
	visibilityRkey       = "self"

	// visibilityLookupTimeout bounds identity resolution plus the PDS call.
	visibilityLookupTimeout = 10 * time.Second
	visibilityDefaultTTL    = 24 * time.Hour
	visibilityCacheMax      = 10000
)

// errNoPDSEndpoint is returned when the DID document declares no atproto_pds service.
var errNoPDSEndpoint = errors.New("identity has no PDS endpoint")

type visibilityEntry struct {
	v       Visibility
	expires time.Time
}

// VisibilityResolver fetches Content Visibility Declarations from author PDSes
// and caches the definitive answers. There is no AppView field for this record,
// so it must be read from the repo directly.
type VisibilityResolver struct {
	dir        identity.Directory
	httpClient *http.Client
	ttl        time.Duration

	mu    sync.Mutex
	cache map[string]visibilityEntry
}

// NewVisibilityResolver builds a resolver. A nil dir uses identity.DefaultDirectory(),
// a nil httpClient uses a client with a 10s timeout, and a ttl <= 0 selects 24h.
func NewVisibilityResolver(dir identity.Directory, httpClient *http.Client, ttl time.Duration) *VisibilityResolver {
	if dir == nil {
		dir = identity.DefaultDirectory()
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: visibilityLookupTimeout}
	}
	if ttl <= 0 {
		ttl = visibilityDefaultTTL
	}
	return &VisibilityResolver{
		dir:        dir,
		httpClient: httpClient,
		ttl:        ttl,
		cache:      make(map[string]visibilityEntry),
	}
}

// Lookup returns the author's declaration. A missing record is Allowed. Only
// Allowed/Hidden are cached, so a transient failure is retried on the next call.
func (r *VisibilityResolver) Lookup(ctx context.Context, did string) Visibility {
	parsed, err := syntax.ParseDID(did)
	if err != nil {
		slog.Warn("visibility lookup", "did", did, "result", VisibilityUnknown.String(), "source", "none", "cache_hit", false, "error", err)
		return VisibilityUnknown
	}
	key := parsed.String()

	if v, ok := r.cached(key); ok {
		slog.Debug("visibility lookup", "did", key, "result", v.String(), "source", "cache", "cache_hit", true)
		return v
	}

	v, err := r.fetch(ctx, parsed)
	if err != nil {
		slog.Info("visibility lookup", "did", key, "result", v.String(), "source", "pds", "cache_hit", false, "error", err)
	} else {
		slog.Info("visibility lookup", "did", key, "result", v.String(), "source", "pds", "cache_hit", false)
	}

	if v != VisibilityUnknown {
		r.store(key, v)
	}
	return v
}

// fetch resolves the DID to a PDS and reads the declaration record from it.
func (r *VisibilityResolver) fetch(ctx context.Context, did syntax.DID) (Visibility, error) {
	ctx, cancel := context.WithTimeout(ctx, visibilityLookupTimeout)
	defer cancel()

	ident, err := r.dir.LookupDID(ctx, did)
	if err != nil {
		return VisibilityUnknown, fmt.Errorf("failed to resolve identity: %w", err)
	}
	pds := ident.PDSEndpoint()
	if pds == "" {
		return VisibilityUnknown, errNoPDSEndpoint
	}

	api := atclient.NewAPIClient(pds)
	api.Client = r.httpClient

	out, err := atproto.RepoGetRecord(ctx, api, "", visibilityCollection, did.String(), visibilityRkey)
	if err != nil {
		// An absent record is the common case and MUST be treated as false.
		if isRecordNotFound(err) {
			return VisibilityAllowed, nil
		}
		return VisibilityUnknown, fmt.Errorf("failed to get visibility record: %w", err)
	}
	if out.Value == nil {
		slog.Warn("visibility record has no value", "did", did.String())
		return VisibilityUnknown, fmt.Errorf("visibility record has no value")
	}
	decl, ok := out.Value.Val.(*bsky.ActorContentVisibilityDeclaration)
	if !ok {
		// Present but undecodable: do not assume either way.
		slog.Warn("visibility record could not be decoded", "did", did.String())
		return VisibilityUnknown, fmt.Errorf("visibility record is not a contentVisibilityDeclaration")
	}
	if decl.HideFromAlgorithmicRecommendations {
		return VisibilityHidden, nil
	}
	return VisibilityAllowed, nil
}

// isRecordNotFound reports whether err is the PDS's "no such record" response:
// HTTP 400 with an error name of RecordNotFound.
func isRecordNotFound(err error) bool {
	var apiErr *atclient.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusBadRequest && apiErr.Name == "RecordNotFound"
}

func (r *VisibilityResolver) cached(did string) (Visibility, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.cache[did]
	if !ok {
		return VisibilityUnknown, false
	}
	if !e.expires.After(time.Now()) {
		delete(r.cache, did)
		return VisibilityUnknown, false
	}
	return e.v, true
}

func (r *VisibilityResolver) store(did string, v Visibility) {
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.cache[did] = visibilityEntry{v: v, expires: now.Add(r.ttl)}
	if len(r.cache) <= visibilityCacheMax {
		return
	}

	for k, e := range r.cache {
		if !e.expires.After(now) {
			delete(r.cache, k)
		}
	}
	if len(r.cache) <= visibilityCacheMax {
		return
	}

	oldestKey := ""
	var oldest time.Time
	for k, e := range r.cache {
		if oldestKey == "" || e.expires.Before(oldest) {
			oldestKey, oldest = k, e.expires
		}
	}
	delete(r.cache, oldestKey)
}

// CacheLen returns the number of cached entries, including any not yet reaped.
func (r *VisibilityResolver) CacheLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cache)
}
