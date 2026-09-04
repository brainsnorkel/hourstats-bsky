package main

import (
	"context"
	"errors"
	"testing"
	"time"
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
