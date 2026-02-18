package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/jetstream"
	"github.com/christophergentle/hourstats-bsky/internal/stats"
	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

// ---------------------------------------------------------------------------
// Jetstream consumer
// ---------------------------------------------------------------------------

func runJetstream(ctx context.Context, db *store.Store, trendingEnabled bool, collector *stats.Collector, writeCh chan<- store.PendingWrite) {
	cfg := jetstream.ConsumerConfig{
		OnPost: func(evt *jetstream.Event, rec *jetstream.PostRecord) {
			collector.IncrementFirehosePost()

			if strings.TrimSpace(rec.Text) == "" {
				return
			}
			if !isEnglish(rec.Langs) {
				return
			}
			cid := ""
			if evt.Commit != nil {
				cid = evt.Commit.CID
			}
			createdAt := normalizeTimestamp(rec.CreatedAt)
			post := store.Post{
				URI:       evt.PostURI(),
				CID:       cid,
				Text:      rec.Text,
				AuthorDID: evt.DID,
				CreatedAt: createdAt,
				IsReply:   rec.Reply != nil,
			}

			collector.IncrementEnglishPost(rec.Reply != nil)

			pw := store.PendingWrite{
				Post:      post,
				CreatedAt: createdAt,
			}
			if trendingEnabled && rec.Reply == nil && !rec.HasAdultContent() {
				hashtagCount := strings.Count(rec.Text, "#")
				if hashtagCount <= 1 && !topics.IsRepetitive(rec.Text) {
					toks := topics.Tokenize(rec.Text)
					if len(toks) > 0 {
						tokJSON, _ := json.Marshal(toks)
						pw.TokensJSON = string(tokJSON)
					}
				}
			}

			select {
			case writeCh <- pw:
			default:
				collector.IncrementDroppedPosts(1)
				slog.Warn("write buffer full, dropping post", "uri", post.URI)
			}
		},
		SaveCursor: func(saveCtx context.Context, cursor int64) error {
			return db.SaveCursor(saveCtx, cursor)
		},
		LoadCursor: func(loadCtx context.Context) (int64, error) {
			return db.GetCursor(loadCtx)
		},
	}

	for {
		consumer := jetstream.NewConsumer(cfg)
		collector.SetConsumer(consumer)
		err := consumer.Run(ctx)
		collector.SetConsumer(nil)
		if ctx.Err() != nil {
			return
		}

		// consumer.Run only returns on fatal errors not handled by its
		// internal reconnect loop; restart immediately.
		_ = collector.LogEvent(ctx, "consumer_restart", fmt.Sprintf("unexpected exit: %v", err))
		slog.Error("jetstream consumer exited unexpectedly, restarting immediately", "error", err)
	}
}

func isEnglish(langs []string) bool {
	if len(langs) == 0 {
		return false
	}
	for _, l := range langs {
		if l == "en" || strings.HasPrefix(l, "en-") {
			return true
		}
	}
	return false
}

func normalizeTimestamp(raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, raw)
	}
	if err != nil {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return t.UTC().Format(time.RFC3339)
}
