package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func TestAwaitTopicOutcome(t *testing.T) {
	ctx := context.Background()
	ch := make(chan topicAnalysisOutcome, 1)
	if _, ok := awaitTopicOutcome(ctx, ch, 10*time.Millisecond); ok {
		t.Fatal("expected timeout when nothing is sent")
	}
	want := topicAnalysisOutcome{snapshotTime: "2026-09-04T10:00:00Z", err: errors.New("x")}
	ch <- want
	got, ok := awaitTopicOutcome(ctx, ch, time.Second)
	if !ok || got.snapshotTime != want.snapshotTime || got.err != want.err {
		t.Fatalf("got %+v, ok=%v; want %+v", got, ok, want)
	}

	// A timed-out wait leaves the value in the channel for the later receive.
	ch <- want
	if _, ok := awaitTopicOutcome(ctx, ch, time.Second); !ok {
		t.Fatal("outcome should still be receivable after an earlier timeout")
	}

	// Cancellation returns immediately instead of running out the timeout.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	start := time.Now()
	if _, ok := awaitTopicOutcome(cancelled, ch, 5*time.Second); ok {
		t.Fatal("expected no outcome on cancelled context")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancelled wait did not return promptly")
	}
}

type fakeTopTopicStore struct {
	label     string
	labelErr  error
	updated   bool
	setErr    error
	gotLookup []string
	gotSet    []string
}

func (f *fakeTopTopicStore) GetTopicLabelAt(_ context.Context, snapshotTime string, rank int) (string, error) {
	f.gotLookup = append(f.gotLookup, snapshotTime)
	if rank != 1 {
		return "", errors.New("unexpected rank")
	}
	return f.label, f.labelErr
}

func (f *fakeTopTopicStore) SetSentimentTopTopic(_ context.Context, runID, label string) (bool, error) {
	f.gotSet = append(f.gotSet, runID+"="+label)
	return f.updated, f.setErr
}

func TestRecordTopTopic(t *testing.T) {
	ok := topicAnalysisOutcome{snapshotTime: "2026-09-04T10:00:00Z"}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name       string
		ctx        context.Context
		outcome    topicAnalysisOutcome
		store      fakeTopTopicStore
		wantLookup int
		wantSet    []string
	}{
		{"writes rank-1 label", context.Background(), ok, fakeTopTopicStore{label: "Topic", updated: true}, 1, []string{"run-1=Topic"}},
		{"analysis error skips", context.Background(), topicAnalysisOutcome{snapshotTime: "x", err: errors.New("boom")}, fakeTopTopicStore{label: "Topic"}, 0, nil},
		{"no snapshot skips", context.Background(), topicAnalysisOutcome{}, fakeTopTopicStore{label: "Topic"}, 0, nil},
		{"cancelled context skips", cancelled, ok, fakeTopTopicStore{label: "Topic"}, 0, nil},
		{"lookup error skips write", context.Background(), ok, fakeTopTopicStore{labelErr: errors.New("db")}, 1, nil},
		{"empty label skips write", context.Background(), ok, fakeTopTopicStore{label: ""}, 1, nil},
		{"missing row is tolerated", context.Background(), ok, fakeTopTopicStore{label: "Topic", updated: false}, 1, []string{"run-1=Topic"}},
		{"write error is tolerated", context.Background(), ok, fakeTopTopicStore{label: "Topic", setErr: errors.New("db")}, 1, []string{"run-1=Topic"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.store
			recordTopTopic(tc.ctx, &st, "run-1", tc.outcome)
			if len(st.gotLookup) != tc.wantLookup {
				t.Errorf("lookups = %v, want %d", st.gotLookup, tc.wantLookup)
			}
			if len(st.gotSet) != len(tc.wantSet) {
				t.Fatalf("sets = %v, want %v", st.gotSet, tc.wantSet)
			}
			for i := range st.gotSet {
				if st.gotSet[i] != tc.wantSet[i] {
					t.Errorf("set[%d] = %q, want %q", i, st.gotSet[i], tc.wantSet[i])
				}
			}
		})
	}
}

// TestScoreWindowHeadlineIsEmojiAware pins the 2026-09-11 switch: the headline
// figures come from the emoji-aware analyzer and stock VADER only fills the
// second column.
func TestScoreWindowHeadlineIsEmojiAware(t *testing.T) {
	posts := []analyzer.Post{
		{URI: "at://a/1", Text: "this set is 🔥🔥", Author: "a"},
		{URI: "at://a/2", Text: "absolutely gutted 😭", Author: "b", IsReply: true},
		{URI: "at://a/3", Text: "a plain sentence with no emoji at all", Author: "c"},
	}

	scores, err := scoreWindow(posts, "run-test")
	if err != nil {
		t.Fatalf("scoreWindow: %v", err)
	}
	if len(scores.Analyzed) != len(posts) {
		t.Fatalf("analyzed %d posts, want %d", len(scores.Analyzed), len(posts))
	}

	emojiAnalyzed, err := analyzer.NewEmojiAware().AnalyzePosts(posts)
	if err != nil {
		t.Fatalf("emoji-aware analyze: %v", err)
	}
	wantCategory, wantNet := calculateOverallSentiment(emojiAnalyzed)
	wantRoot, wantReply := calculateSplitSentiment(emojiAnalyzed)
	if scores.Category != wantCategory || scores.NetPct != wantNet {
		t.Errorf("headline = (%q, %v), want the emoji-aware (%q, %v)",
			scores.Category, scores.NetPct, wantCategory, wantNet)
	}
	if scores.RootPct != wantRoot || scores.ReplyPct != wantReply {
		t.Errorf("split = (%v, %v), want the emoji-aware (%v, %v)",
			scores.RootPct, scores.ReplyPct, wantRoot, wantReply)
	}

	stockAnalyzed, err := analyzer.New().AnalyzePosts(posts)
	if err != nil {
		t.Fatalf("stock analyze: %v", err)
	}
	_, wantStock := calculateOverallSentiment(stockAnalyzed)
	if scores.StockNetPct == nil {
		t.Fatal("StockNetPct = nil, want the stock VADER net percent")
	}
	if *scores.StockNetPct != wantStock {
		t.Errorf("StockNetPct = %v, want %v", *scores.StockNetPct, wantStock)
	}
	// 🔥 scores -1.4 under stock VADER's Unicode-name lookup and +2.5 under
	// the curated table, so the two scorers must not agree on this window.
	if *scores.StockNetPct == scores.NetPct {
		t.Errorf("headline and stock net both %v; the headline is not emoji-aware", scores.NetPct)
	}
}

func TestRecordScorerV2Cutover(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "cutover.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	first := time.Date(2026, 9, 11, 0, 30, 0, 0, time.UTC)
	recordScorerV2Cutover(ctx, db, first)
	got, err := db.GetKeyValue(ctx, scorerV2Key)
	if err != nil {
		t.Fatalf("read %s: %v", scorerV2Key, err)
	}
	if got != "2026-09-11" {
		t.Errorf("%s = %q, want the UTC date of the first start", scorerV2Key, got)
	}

	// A later restart must not move the cutover.
	recordScorerV2Cutover(ctx, db, first.AddDate(0, 0, 5))
	if got, _ := db.GetKeyValue(ctx, scorerV2Key); got != "2026-09-11" {
		t.Errorf("%s = %q after a restart, want it unchanged", scorerV2Key, got)
	}
}

func TestWindowCapDecision(t *testing.T) {
	tests := []struct {
		name      string
		available int
		limit     int
		want      bool
	}{
		{"under the cap", 1000, 300000, false},
		{"exactly at the cap", 300000, 300000, false},
		{"over the cap", 300001, 300000, true},
		{"cap disabled by zero", 5000000, 0, false},
		{"cap disabled by a negative", 5000000, -1, false},
		{"empty window", 0, 300000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowCapDecision(tt.available, tt.limit); got != tt.want {
				t.Errorf("windowCapDecision(%d, %d) = %v, want %v", tt.available, tt.limit, got, tt.want)
			}
		})
	}
}
