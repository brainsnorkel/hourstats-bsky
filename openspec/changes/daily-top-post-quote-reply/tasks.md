## 1. Store Layer — Key-Value Table

- [ ] 1.1 Add `key_value` table migration to `store.go` `migrate()`: `CREATE TABLE IF NOT EXISTS key_value (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))`
- [ ] 1.2 Add `SetKeyValue(ctx, key, value string) error` method to store — upsert using `INSERT OR REPLACE`
- [ ] 1.3 Add `GetKeyValue(ctx, key string) (string, error)` method to store — returns `""` and `sql.ErrNoRows` wrapped if not found
- [ ] 1.4 Add unit tests for SetKeyValue/GetKeyValue: insert, upsert, not-found

## 2. Store Layer — Daily Top Post Query

- [ ] 2.1 Add `GetTopPostForDate(ctx, date string) (*Post, error)` method to store — queries `SELECT top_posts FROM runs WHERE created_at >= ? AND created_at < ?` for the given date's UTC boundaries, parses all `top_posts` JSON arrays, returns the single post with the highest `EngagementScore`
- [ ] 2.2 Add unit tests: multiple runs with different top posts, no runs for date, runs with empty top_posts

## 3. Bluesky Client — PostReplyWithQuote

- [ ] 3.1 Add `PostReplyWithQuote(ctx, text string, rootURI, rootCID, parentURI, parentCID, quoteURI, quoteCID string) (string, string, error)` to `bluesky.go` — creates a `FeedPost` with both `Reply` (root+parent refs) and `Embed.EmbedRecord` (quote ref), calls `RepoCreateRecord`
- [ ] 3.2 Verify the method compiles and the `bsky.FeedPost` struct accepts both `Reply` and `Embed` fields simultaneously (it does — confirmed from existing code)

## 4. Yearly Posting — Persist URI/CID

- [ ] 4.1 In `runYearlyPosting` in `cmd/hourstats/main.go`, after the yearly chart is posted and pinned, call `db.SetKeyValue(ctx, "yearly_post_uri", sentimentURI)` and `db.SetKeyValue(ctx, "yearly_post_cid", sentimentCID)`
- [ ] 4.2 Verify that re-running yearly posting (next month) overwrites the stored URI/CID with the new post

## 5. Daily Quote-Reply Step

- [ ] 5.1 Add `runDailyTopPostQuote(ctx, db, handle, password string, dryRun bool)` function in `cmd/hourstats/main.go`
- [ ] 5.2 Implement idempotency check: read `daily_quote_last_date` from key_value, skip if it matches `time.Now().UTC().Format("2006-01-02")`
- [ ] 5.3 Read `yearly_post_uri` and `yearly_post_cid` from key_value; skip with log if either is empty
- [ ] 5.4 Call `db.GetTopPostForDate(ctx, yesterday)` to find yesterday's top post; skip with log if nil
- [ ] 5.5 Authenticate Bluesky client, format reply text (e.g. `"Most engaged post yesterday (Mon Jan 6) by @handle"`)
- [ ] 5.6 Call `bskyClient.PostReplyWithQuote(...)` with yearly post as root+parent and top post as quote
- [ ] 5.7 On success, store `daily_quote_last_date` = today's date in key_value
- [ ] 5.8 On failure, log warning and continue (no retry)

## 6. Scheduling Integration

- [ ] 6.1 In `main.go` backup ticker case (line ~95), add `runDailyTopPostQuote(ctx, db, handle, password, dryRun)` call after `runDailyAggregation(ctx, db)` and before the yearly posting check
- [ ] 6.2 Verify the call order: backup → daily aggregation → daily quote-reply → yearly posting (1st of month only)

## 7. Verification

- [ ] 7.1 `go build ./...` passes
- [ ] 7.2 `go test ./...` passes (including new store tests)
- [ ] 7.3 LSP diagnostics clean on all changed files
- [ ] 7.4 Deploy to staging and verify: daily quote-reply posts correctly as a reply to the yearly post with a quote-embed of the top post
