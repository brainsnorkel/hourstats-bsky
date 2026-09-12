package jetstream

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/denylist"
)

// useDenyList publishes dids for the duration of the test and clears the
// process-wide set afterwards.
func useDenyList(t *testing.T, dids ...string) {
	t.Helper()
	denylist.Replace(dids)
	t.Cleanup(func() { denylist.Replace(nil) })
}

// TestDeniedCreateDIDScansBothProtocols: the drop decision is a bounded byte
// scan of the raw frame, made before any parse, on both wire formats.
func TestDeniedCreateDIDScansBothProtocols(t *testing.T) {
	useDenyList(t, "did:plc:banned")

	witness := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	v2Denied := strings.ReplaceAll(v2PostFrame(1, "3abc", witness, witness, "en"),
		"did:plc:aaa", "did:plc:banned")
	v2Allowed := v2PostFrame(2, "3def", witness, witness, "en")
	v1Denied := strings.ReplaceAll(createFrame, "did:plc:aaa", "did:plc:banned")
	v1Delete := strings.ReplaceAll(deleteFrame, "did:plc:bbb", "did:plc:banned")
	v1Account := strings.ReplaceAll(accountFrame, "did:plc:ccc", "did:plc:banned")

	tests := []struct {
		name  string
		frame string
		want  string
	}{
		{"v2 create from a denied repo", v2Denied, "did:plc:banned"},
		{"v2 create from another repo", v2Allowed, ""},
		{"v1 create from a denied repo", v1Denied, "did:plc:banned"},
		{"v1 create from another repo", createFrame, ""},
		{"v1 delete from a denied repo", v1Delete, ""},
		{"v1 account event from a denied repo", v1Account, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deniedCreateDID([]byte(tt.frame)); got != tt.want {
				t.Errorf("deniedCreateDID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDeniedCreateDIDIsInertWhenEmpty: with no denylist configured the scan is
// never reached, so an ordinary create is never touched.
func TestDeniedCreateDIDIsInertWhenEmpty(t *testing.T) {
	denylist.Replace(nil)
	if got := deniedCreateDID([]byte(createFrame)); got != "" {
		t.Errorf("deniedCreateDID() = %q with an empty denylist, want %q", got, "")
	}
}

// TestRefreshDenyListUnionsEnvAndStored: the environment half and the stored
// half are unioned, and a later reload picks up an edited row without losing
// the environment entries.
func TestRefreshDenyListUnionsEnvAndStored(t *testing.T) {
	t.Cleanup(func() { denylist.Replace(nil) })

	var mu sync.Mutex
	stored := []string{"did:plc:stored"}
	c := NewConsumer(ConsumerConfig{
		DenyDIDs: []string{"did:plc:env"},
		LoadDenyList: func(context.Context) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			return stored, nil
		},
	})

	c.refreshDenyList(context.Background())
	for _, did := range []string{"did:plc:env", "did:plc:stored"} {
		if !denylist.Contains(did) {
			t.Errorf("Contains(%q) = false, want true after the first load", did)
		}
	}

	// The row is edited over `fly ssh`; the next reload replaces the stored
	// half and keeps the environment half.
	mu.Lock()
	stored = []string{"did:plc:added"}
	mu.Unlock()
	c.refreshDenyList(context.Background())

	if !denylist.Contains("did:plc:added") {
		t.Error("Contains(did:plc:added) = false, want true after the reload")
	}
	if denylist.Contains("did:plc:stored") {
		t.Error("Contains(did:plc:stored) = true, want false once the row no longer lists it")
	}
	if !denylist.Contains("did:plc:env") {
		t.Error("Contains(did:plc:env) = false, want the environment half to survive a reload")
	}
}

// TestRefreshDenyListKeepsTheListOnAFailedLoad: a database error must not empty
// the denylist, which would silently re-admit every repo on it.
func TestRefreshDenyListKeepsTheListOnAFailedLoad(t *testing.T) {
	t.Cleanup(func() { denylist.Replace(nil) })

	fail := false
	c := NewConsumer(ConsumerConfig{
		DenyDIDs: []string{"did:plc:env"},
		LoadDenyList: func(context.Context) ([]string, error) {
			if fail {
				return nil, errors.New("database is locked")
			}
			return []string{"did:plc:stored"}, nil
		},
	})

	c.refreshDenyList(context.Background())
	fail = true
	c.refreshDenyList(context.Background())

	for _, did := range []string{"did:plc:env", "did:plc:stored"} {
		if !denylist.Contains(did) {
			t.Errorf("Contains(%q) = false, want the previous list kept after a failed load", did)
		}
	}
}

// TestConsumerV1_DeniedCreatesDroppedDeletesKept is the end-to-end case: a
// denied repo's create is dropped before the language pre-filter, counted as
// denied, and never counted as a post of its language, while its delete and its
// account event still drive the purge path.
func TestConsumerV1_DeniedCreatesDroppedDeletesKept(t *testing.T) {
	t.Cleanup(func() { denylist.Replace(nil) })

	denied := strings.ReplaceAll(createFrame, "did:plc:aaa", "did:plc:banned")
	deniedDelete := strings.ReplaceAll(deleteFrame, "did:plc:bbb", "did:plc:banned")
	deniedAccount := strings.ReplaceAll(accountFrame, "did:plc:ccc", "did:plc:banned")

	var mu sync.Mutex
	var posts, deletes, inactive []string
	var earlyRejects int
	done := make(chan struct{})
	var once sync.Once

	consumer := NewConsumer(ConsumerConfig{
		Protocol:      ProtocolV1,
		Endpoint:      serveV1Frames(t, [][]string{{denied, deniedDelete, createFrame, deniedAccount}}),
		DenyDIDs:      []string{"did:plc:banned"},
		MaxPostAge:    -1,
		MaxPostFuture: -1,
		OnPost: func(evt *Event, _ *PostRecord) {
			mu.Lock()
			posts = append(posts, evt.PostURI())
			mu.Unlock()
		},
		OnDelete: func(evt *Event) {
			mu.Lock()
			deletes = append(deletes, evt.PostURI())
			mu.Unlock()
		},
		OnAccountInactive: func(evt *Event) {
			mu.Lock()
			inactive = append(inactive, evt.DID)
			mu.Unlock()
			once.Do(func() { close(done) })
		},
		OnEarlyReject: func(string) {
			mu.Lock()
			earlyRejects++
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the account event")
	}

	report := consumer.GetStatsReport()
	if report.PostsDenied != 1 {
		t.Errorf("PostsDenied = %d, want 1", report.PostsDenied)
	}
	if report.PostsProcessed != 1 {
		t.Errorf("PostsProcessed = %d, want 1 (only the allowed create)", report.PostsProcessed)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || posts[0] != "at://did:plc:aaa/app.bsky.feed.post/3abc" {
		t.Errorf("posts = %v, want only the allowed create", posts)
	}
	if len(deletes) != 1 || deletes[0] != "at://did:plc:banned/app.bsky.feed.post/3def" {
		t.Errorf("deletes = %v, want the denied repo's delete through", deletes)
	}
	if len(inactive) != 1 || inactive[0] != "did:plc:banned" {
		t.Errorf("inactive = %v, want the denied repo's account event through", inactive)
	}
	if earlyRejects != 0 {
		t.Errorf("OnEarlyReject calls = %d, want 0: a denied create is not a post of its language", earlyRejects)
	}
}

// TestConsumerV2_DeniedCreatesDropped is the v2 half of the same drop, on the
// compressed-protocol read loop.
func TestConsumerV2_DeniedCreatesDropped(t *testing.T) {
	t.Cleanup(func() { denylist.Replace(nil) })

	witness := time.Now().UTC().Truncate(time.Second)
	denied := strings.ReplaceAll(v2PostFrame(1, "3bad", witness, witness, "en"),
		"did:plc:aaa", "did:plc:banned")
	// A non-English create from the denied repo must not reach OnEarlyReject
	// either, or the firehose and language totals carry it instead.
	deniedNonEnglish := strings.ReplaceAll(v2PostFrame(2, "3pt", witness, witness, "pt"),
		"did:plc:aaa", "did:plc:banned")
	allowed := v2PostFrame(3, "3ok", witness, witness, "en")

	var mu sync.Mutex
	var posts []string
	var earlyRejects int
	consumer := NewConsumer(ConsumerConfig{
		Protocol:           ProtocolV2,
		Endpoint:           serveFrames(t, []string{denied, deniedNonEnglish, allowed}),
		DisableCompression: true,
		DenyDIDs:           []string{"did:plc:banned"},
		OnPost: func(evt *Event, _ *PostRecord) {
			mu.Lock()
			posts = append(posts, evt.PostURI())
			mu.Unlock()
		},
		OnEarlyReject: func(string) {
			mu.Lock()
			earlyRejects++
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Run(ctx) }()

	waitFor(t, func() bool { return consumer.GetStatsReport().PostsProcessed == 1 })
	waitFor(t, func() bool { return consumer.GetStatsReport().PostsDenied == 2 })

	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || !strings.HasSuffix(posts[0], "3ok") {
		t.Errorf("posts = %v, want only 3ok", posts)
	}
	if earlyRejects != 0 {
		t.Errorf("OnEarlyReject calls = %d, want 0", earlyRejects)
	}
}

// TestDenyListReloadLoopPicksUpANewRow runs the real reload ticker: a DID added
// to the stored list is honoured without a restart.
func TestDenyListReloadLoopPicksUpANewRow(t *testing.T) {
	t.Cleanup(func() { denylist.Replace(nil) })

	var mu sync.Mutex
	var stored []string
	c := NewConsumer(ConsumerConfig{
		LoadDenyList: func(context.Context) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			return stored, nil
		},
		DenyListReload: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.denyListReloadLoop(ctx)

	mu.Lock()
	stored = []string{"did:plc:late"}
	mu.Unlock()

	waitFor(t, func() bool { return denylist.Contains("did:plc:late") })
}

// TestDenyListTrimsAndIgnoresBlanks: a hand-edited row with spaces and a
// trailing comma behaves like its environment equivalent.
func TestDenyListTrimsAndIgnoresBlanks(t *testing.T) {
	useDenyList(t, " did:plc:spaced ", "", "did:plc:plain")

	if !denylist.Contains("did:plc:spaced") {
		t.Error("Contains(did:plc:spaced) = false, want the entry trimmed")
	}
	if !denylist.Contains("did:plc:plain") {
		t.Error("Contains(did:plc:plain) = false")
	}
	if denylist.Contains("") {
		t.Error(`Contains("") = true, want blank entries dropped`)
	}
	if got := denylist.Len(); got != 2 {
		t.Errorf("Len = %d, want 2", got)
	}
	if denylist.Contains("did:plc:other") {
		t.Error("Contains(did:plc:other) = true, want false")
	}
}
