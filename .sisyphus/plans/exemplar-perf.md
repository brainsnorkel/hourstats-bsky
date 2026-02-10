# Fix GetExemplarCandidates Performance

## Context

### Original Request
The `GetExemplarCandidates` query in `internal/store/topic_store.go` times out on staging (shared-cpu-1x, 512MB, Fly.io). Even a single-keyword query takes 60s+ and returns nothing. The root cause is `json_each()` expanding ~60k rows x ~10 tokens = ~600k+ virtual rows per query, with no indexable path through the result.

### Interview Summary
- **6h window**: ~60k rows in `topic_tokens` (COUNT itself is slow)
- **Keywords per topic**: 3-10 keywords; synonyms rarely populated
- **Hashtag filter is redundant**: Multi-hashtag posts are already filtered at ingestion (`main.go` line 220-221). The SQL-side `LENGTH(pb.text) - LENGTH(REPLACE(pb.text, '#', ''))` check can be dropped.
- **New table is acceptable**: Idempotent migration style means adding a table is trivial.
- **Cold-start gap is tolerable**: No exemplars for up to 6h after first deploy is fine.
- **Only `json_each` user**: `GetExemplarCandidates` is the sole consumer. TF-IDF pipeline reads raw JSON in Go — unaffected.

### Research Findings
- `topic_tokens` stores a JSON array of unigram/bigram tokens per post (avg 8-12 tokens).
- The tokenizer produces lowercase alphanumeric tokens like `"hockey"`, `"age_verification"`.
- `PurgeTopicTokens` runs at 26h cutoff in `analyzer.go` line 56-63.
- `ExemplarCandidateStore` interface (in `exemplar.go` line 11-13) defines `GetExemplarCandidates` — signature stays unchanged.
- `AnalyzerStore` in `analyzer.go` embeds `ExemplarCandidateStore` — no interface changes needed.
- Mock in `analyzer_test.go` line 71 implements the same signature — no mock changes needed.
- 5 existing exemplar tests in `topic_store_test.go` (lines 99-222) must continue to pass.

## Work Objectives

### Core Objective
Eliminate the `json_each()` bottleneck by introducing a denormalized `token_postings` table with a composite index, replacing the virtual-table expansion with a direct indexed lookup.

### Deliverables
1. New `token_postings` table with composite index `(token, created_at)`
2. Dual-write at ingestion: `topic_tokens` (unchanged) + `token_postings`
3. Rewritten `GetExemplarCandidates` query using `token_postings` instead of `json_each`
4. Purge logic for `token_postings` alongside existing `topic_tokens` purge
5. All existing tests passing + new targeted tests for the rewritten query

### Definition of Done
- [ ] `GetExemplarCandidates` returns correct results in <1s on 60k-row dataset
- [ ] All existing tests in `topic_store_test.go` and `analyzer_test.go` pass
- [ ] `make test` passes
- [ ] `make build` succeeds
- [ ] TF-IDF pipeline (`ComputeTFIDF`, `GetTopicTokensSinceLimit`) is completely unmodified
- [ ] No interface changes to `ExemplarCandidateStore` or `AnalyzerStore`

## Guardrails

### Must Have
- `token_postings` table with `(token TEXT, post_uri TEXT, created_at TEXT)` and composite index on `(token, created_at)`
- Dual-write in the ingestion path (`main.go`)
- `InsertTopicTokens` helper also writes to `token_postings` (so tests that call it directly work)
- Purge of `token_postings` piggybacks on `PurgeTopicTokens`
- Drop the redundant hashtag spam filter from the SQL query
- Keep `GetExemplarCandidates` method signature identical

### Must NOT Have
- Do NOT modify `topic_tokens` table or its schema
- Do NOT change `GetTopicTokensSinceLimit`, `ComputeTFIDF`, or any TF-IDF pipeline code
- Do NOT change the `ExemplarCandidateStore` or `AnalyzerStore` interfaces
- Do NOT add CGO dependencies
- Do NOT add a backfill migration (cold-start gap is accepted)

## Task Flow

```
T1: Schema ──> T2: Dual-write ──> T3: Rewrite query ──> T4: Purge ──> T5: Tests ──> T6: Verify
```

All tasks are sequential — each builds on the previous.

## Tasks

### T1: Add `token_postings` table and index to schema migration
**File**: `internal/store/store.go`
**Action**: Append two statements to the `stmts` slice in `migrate()` (after the `topic_identity` table, around line 261):

```sql
CREATE TABLE IF NOT EXISTS token_postings (
    token TEXT NOT NULL,
    post_uri TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (token, post_uri)
)
```
```sql
CREATE INDEX IF NOT EXISTS idx_token_postings_token_created ON token_postings(token, created_at)
```

**Rationale**: The composite PK `(token, post_uri)` prevents duplicates if a post is re-processed. The index `(token, created_at)` is the critical one — it lets the exemplar query seek directly to matching tokens within the time window.

**Acceptance criteria**:
- `make build` succeeds
- Store opens without error on fresh database
- Idempotent: running `migrate()` twice doesn't fail

### T2: Dual-write token postings at ingestion
**File**: `internal/store/topic_store.go`
**Action**: Modify `InsertTopicTokens` to also write individual rows to `token_postings`. After the existing `INSERT OR IGNORE INTO topic_tokens`, parse the JSON token array and batch-insert into `token_postings`.

```go
func (s *Store) InsertTopicTokens(ctx context.Context, postURI, tokensJSON, createdAt string) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT OR IGNORE INTO topic_tokens (post_uri, tokens, created_at) VALUES (?, ?, ?)`,
        postURI, tokensJSON, createdAt,
    )
    if err != nil {
        return fmt.Errorf("insert topic_tokens: %w", err)
    }

    // Denormalize into token_postings for indexed exemplar lookups.
    var tokens []string
    if err := json.Unmarshal([]byte(tokensJSON), &tokens); err != nil {
        return fmt.Errorf("unmarshal tokens for postings: %w", err)
    }
    for _, tok := range tokens {
        _, err := s.db.ExecContext(ctx,
            `INSERT OR IGNORE INTO token_postings (token, post_uri, created_at) VALUES (?, ?, ?)`,
            tok, postURI, createdAt,
        )
        if err != nil {
            return fmt.Errorf("insert token_posting: %w", err)
        }
    }
    return nil
}
```

**Note**: This adds `encoding/json` to the import list for `topic_store.go`.

**Why per-row INSERT instead of batch**: Average 8-12 tokens per post. At ingestion rate (~1 post/sec during bursts), this is ~12 tiny INSERTs per post — trivial for SQLite WAL mode. `INSERT OR IGNORE` handles the idempotency (a token appearing twice in one post's array). A batch approach would add complexity for negligible gain at this scale.

**Acceptance criteria**:
- After calling `InsertTopicTokens`, both `topic_tokens` and `token_postings` contain the expected data
- Existing tests that call `InsertTopicTokens` still pass (they may not check `token_postings` yet — that's T5)

### T3: Rewrite `GetExemplarCandidates` to use `token_postings`
**File**: `internal/store/topic_store.go`
**Action**: Replace the `json_each`-based query with an indexed query against `token_postings`:

```sql
SELECT pb.uri, pb.author_handle, (pb.likes + pb.reposts + pb.replies) AS eng
FROM token_postings tp
JOIN post_buffer pb ON tp.post_uri = pb.uri
WHERE tp.token IN (?, ?, ...)
  AND tp.created_at >= ?
GROUP BY pb.uri
ORDER BY COUNT(DISTINCT tp.token) DESC, eng DESC
LIMIT ?
```

**Key changes from current query**:
1. `token_postings tp` replaces `topic_tokens tt, json_each(tt.tokens) AS je` — indexed seek instead of virtual table scan
2. `tp.token IN (...)` replaces `je.value IN (...)` — hits the `(token, created_at)` index directly
3. `tp.created_at >= ?` replaces `tt.created_at >= ?` — same column, now on the indexed table
4. Hashtag spam filter `LENGTH(pb.text) - LENGTH(REPLACE(pb.text, '#', ''))` is **removed** (redundant with ingestion filter)
5. `COUNT(DISTINCT tp.token)` replaces `COUNT(DISTINCT je.value)` — same semantics

**Query plan should be**: Index scan on `idx_token_postings_token_created` for each keyword → PK lookup on `post_buffer` → GROUP BY → sort → LIMIT.

**Updated Go code**:

```go
func (s *Store) GetExemplarCandidates(ctx context.Context, keywords []string, cutoff string, limit int) ([]ExemplarCandidate, error) {
    if len(keywords) == 0 {
        return nil, nil
    }

    placeholders := make([]string, len(keywords))
    args := make([]any, 0, len(keywords)+2)
    for i, kw := range keywords {
        placeholders[i] = "?"
        args = append(args, kw)
    }
    args = append(args, cutoff, limit)

    q := fmt.Sprintf(
        `SELECT pb.uri, pb.author_handle, (pb.likes + pb.reposts + pb.replies) AS eng
         FROM token_postings tp
         JOIN post_buffer pb ON tp.post_uri = pb.uri
         WHERE tp.token IN (%s)
           AND tp.created_at >= ?
         GROUP BY pb.uri
         ORDER BY COUNT(DISTINCT tp.token) DESC, eng DESC
         LIMIT ?`,
        strings.Join(placeholders, ","),
    )

    rows, err := s.db.QueryContext(ctx, q, args...)
    if err != nil {
        return nil, fmt.Errorf("query exemplar candidates: %w", err)
    }
    defer rows.Close()

    var result []ExemplarCandidate
    for rows.Next() {
        var c ExemplarCandidate
        if err := rows.Scan(&c.URI, &c.Handle, &c.Engagement); err != nil {
            return nil, fmt.Errorf("scan exemplar candidate: %w", err)
        }
        result = append(result, c)
    }
    return result, rows.Err()
}
```

**Note the argument order change**: Keywords come first (for the IN clause), then cutoff, then limit. The current code puts cutoff first. This is an internal detail — the method signature is identical.

**Acceptance criteria**:
- All 5 existing exemplar tests pass with the new query
- `TestGetExemplarCandidates_FiltersMultiHashtag` — this test inserts a multi-hashtag post directly via `InsertPost` + `InsertTopicTokens`, bypassing the ingestion filter. It will need adjustment (see T5) since the SQL filter is being removed.

### T4: Add purge for `token_postings`
**File**: `internal/store/topic_store.go`
**Action**: Modify `PurgeTopicTokens` to also purge `token_postings` with the same cutoff:

```go
func (s *Store) PurgeTopicTokens(ctx context.Context, cutoff string) (int64, error) {
    result, err := s.db.ExecContext(ctx,
        `DELETE FROM topic_tokens WHERE created_at < ?`, cutoff,
    )
    if err != nil {
        return 0, fmt.Errorf("purge topic_tokens: %w", err)
    }

    // Purge denormalized token postings in sync.
    if _, err := s.db.ExecContext(ctx,
        `DELETE FROM token_postings WHERE created_at < ?`, cutoff,
    ); err != nil {
        return 0, fmt.Errorf("purge token_postings: %w", err)
    }

    return result.RowsAffected()
}
```

**Acceptance criteria**:
- `TestTopicTokens_Purge` still passes
- After purge, `token_postings` rows with old `created_at` are also deleted
- The returned `RowsAffected` count still reflects `topic_tokens` deletions (callers only log this)

### T5: Update and add tests
**File**: `internal/store/topic_store_test.go`

**5a. Fix `TestGetExemplarCandidates_FiltersMultiHashtag`**

This test verifies that a post with `"Trump #maga #election #vote"` (3 hashtags) is filtered out. Currently the SQL filter does this, but we're removing it. The ingestion path already prevents these posts from entering `topic_tokens`. However, this test directly calls `InsertTopicTokens` for both posts, bypassing the ingestion filter.

**Two options**:
1. **Remove the test** — the behavior is now an ingestion concern, not a query concern.
2. **Rename and refactor** — change it to verify that if only the "clean" post is in `token_postings`, the spammy one doesn't appear. This is trivially true (it was never inserted), making the test tautological.

**Recommended**: Remove the test. Add a comment explaining that multi-hashtag filtering is handled at ingestion (`main.go` line 220-221), not at query time. The ingestion path is already tested implicitly by the tokenizer and ingestion-level filtering.

**5b. Add a test verifying `token_postings` is populated**

New test `TestInsertTopicTokens_PopulatesTokenPostings`:
- Insert via `InsertTopicTokens`
- Query `token_postings` directly to verify rows exist
- Verify count matches expected token count

**5c. Add a test verifying `token_postings` is purged**

New test `TestPurgeTopicTokens_AlsoPurgesTokenPostings`:
- Insert tokens at old + recent timestamps
- Purge with cutoff
- Verify `token_postings` old rows are gone, recent rows survive

**5d. Verify remaining 4 exemplar tests pass unchanged**:
- `TestGetExemplarCandidates_RankedByEngagement` — should pass (dual-write populates `token_postings`)
- `TestGetExemplarCandidates_RelevanceBeatsEngagement` — should pass
- `TestGetExemplarCandidates_NoMatch` — should pass
- `TestGetExemplarCandidates_EmptyKeywords` — should pass

**Acceptance criteria**:
- `make test` passes with 0 failures
- New tests cover dual-write and dual-purge

### T6: Build and verify
**Action**: Run full verification:
1. `make build` — binary compiles
2. `make test` — all tests pass
3. `make lint` — no new lint issues (if linter is configured)

**Acceptance criteria**:
- Clean build, clean tests, no regressions

## Performance Analysis

### Before (current query)
```
json_each expansion: 60,000 rows x 10 tokens = 600,000 virtual rows
JOIN with post_buffer: 600,000 PK lookups
Per-row hashtag filter: 600,000 LENGTH/REPLACE computations
GROUP BY on 600,000 rows
Total: O(rows x tokens) — unbounded full scan
```

### After (token_postings query)
```
Index seek per keyword: ~5-10 keywords x ~few hundred matching rows each
JOIN with post_buffer: ~500-2000 PK lookups (only matching posts)
GROUP BY on ~500-2000 rows
Total: O(keywords x matches_per_keyword) — index-driven, bounded
```

**Expected improvement**: 60s+ timeout → <100ms. The index `(token, created_at)` allows SQLite to seek directly to the matching token and then range-scan only within the 6h window.

### Space overhead
- `token_postings`: 60k posts x 10 tokens = ~600k rows
- Each row: ~80 bytes (token ~15 chars + post_uri ~40 chars + created_at ~25 chars)
- Table size: ~48MB + index overhead ~30MB = ~78MB
- On a 512MB RAM staging machine with WAL mode, this is comfortable

## Commit Strategy

**Single commit**: All changes in one atomic commit.

This is a tightly coupled change — the schema, dual-write, query rewrite, purge, and test updates all depend on each other. Splitting into multiple commits would leave intermediate states where the build passes but the exemplar query is broken (e.g., schema exists but query still uses `json_each`, or dual-write active but purge missing).

**Suggested commit message**:
```
fix: replace json_each with token_postings table for exemplar query performance

GetExemplarCandidates timed out on staging (60s+) due to json_each()
expanding ~600k virtual rows per query. Introduce a denormalized
token_postings table with a (token, created_at) index, enabling direct
indexed lookups. Removes redundant SQL-side hashtag spam filter (already
enforced at ingestion).

Expected: 60s+ → <100ms on shared-cpu-1x.
```

## Success Criteria

1. **Functional**: `GetExemplarCandidates` returns correct relevance-ranked results
2. **Performance**: Query completes in <1s on staging workload (60k posts, 10 keywords)
3. **Safety**: TF-IDF pipeline (`ComputeTFIDF`, `GetTopicTokensSinceLimit`) is completely untouched
4. **Tests**: `make test` passes, no regressions
5. **Build**: `make build` succeeds, single binary deploys to Fly.io unchanged
