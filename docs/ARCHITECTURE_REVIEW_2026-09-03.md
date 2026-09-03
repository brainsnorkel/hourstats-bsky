# HourStats Architecture Review — 2026-09-03

Scope: production headroom on Fly.io for Bluesky growth, whether the bot uses the right Bluesky APIs, and whether the published and runtime statistics are robust. Evidence comes from the live prod machine (Fly machine events, Fly Prometheus, the in-VM stats API on port 9111, read-only SQLite queries) and from three code reviews with measured allocation probes. File references are to this commit (branch `exp/memory-staging` off `9d3d34e`).

## 1. Executive summary

1. **Production has no memory headroom today.** The process peaked at 986 MB resident on a 985 MB VM since its last restart and was OOM-killed (exit 137) on 2026-08-27 19:55 UTC, about 30 seconds into the hourly cycle that followed the day's largest cycle. A second restart on 2026-08-25 19:55 UTC fits the same pattern. Bluesky growth is not the trigger: English volume has been flat at 1.8–2.0 M posts/day for six months. Today's peak already exceeds the machine.
2. **The cause is a burst of three large allocators in the first 30 s of the cycle, on top of ~600 MB that the Go memory limit cannot see.** The full window of posts (~88 MB at 114 k posts) is materialised, TF-IDF builds one Go map per distinct term (~100 MB) concurrently, and the hydrator spawns ~4,500 goroutines at once, while `modernc.org/sqlite` holds 4 × 128 MB mmaps plus ~85 MB of page cache outside the Go heap. `GOMEMLIMIT=800MiB` is a no-op: it only governs Go memory and is never reached.
3. **The APIs are the right ones, used in the wrong way.** Jetstream and `app.bsky.feed.getPosts` are the endpoints Bluesky recommends. But hydration runs through the authenticated PDS at 83% of the documented 3,000 requests per 5 minutes per-IP limit, with no 429 handling, no retries, no per-request timeout, and one goroutine per batch. The WebSocket has no read deadline or ping, so a dead connection hangs until TCP keepalive. A Jetstream replay overwrites hydrated engagement counts with zeros.
4. **The published number is precise but biased, and the label is dead.** Deleted and moderated posts are removed before sentiment is scored (estimated +1.5 percentage points upward bias, ten times the sampling error). All 4,798 runs since February are labelled "neutral" because the category threshold is unreachable. The mood word resolves to about 0.45 standard errors per word. The runtime counters that operators rely on (firehose posts, posts per minute, memory) are all wrong in specific, fixable ways.
5. **First scaling wall is not memory but the hydrator's 12-minute timeout**, hit at ~150 k posts/hour, only 5% above the all-time peak of 143 k. On timeout the cycle silently drops unhydrated posts and publishes anyway.

## 2. Live evidence (2026-09-03)

| Item | Value | Source |
|---|---|---|
| Machine | shared-cpu-1x, 1 vCPU, 1024 MB, 10 GB volume, no swap | `fly machine status` |
| Config | hourly cycle at :55 (`ANALYSIS_INTERVAL_MINUTES=60`, `ANALYSIS_OFFSET_MINUTES=55`), `GOMEMLIMIT=800MiB`, `GOGC=75` | `fly.prod.toml` |
| OOM kill | 2026-08-27T19:55:32Z, exit_code=137, oom_killed=true | machine events |
| Process RSS between cycles | 588 MB; VmHWM 986 MB | `/proc/646/status` |
| Go runtime between cycles | heap_inuse 28 MB, heap_sys 376 MB, sys 388 MB | `/stats/latest` |
| Fly 1-min memory samples | 434 MB baseline, 600–665 MB in the first minute of each cycle; true peak never sampled | Fly Prometheus |
| Cycle size | 17 k min / 79 k median / 114 k max posts (7 days); all-time max 143,713 (2026-06-09 15:55) | `/stats/history`, `runs` |
| Cycle duration | 475 s median, 691 s max; hydration ≈ 9.4 min at ~8.3 req/s | logs, `/stats/history` |
| CPU | ~4% average; 43% in the first cycle minute; 94% during the post-cycle purge | Fly Prometheus |
| Volume trend | 1.79–2.06 M English posts/day every month Mar–Sep 2026; peak day 2.31 M (Apr) | `daily_sentiment` |
| SQLite | 644 MB file, freelist 2,613 pages (10 MB); `post_buffer` 425,833 rows (3 h), `topic_tokens` 772,743 rows (26 h) | `/stats/health` |
| Routine errors | getPosts HTTP 502/504 (whole 25-post batches lost, up to 950 posts/cycle); 3.6% of posts excluded as unhydrated; Gemini exemplar rejections | logs, `/stats/history` |
| Weekly VACUUM | Sunday 2026-08-30 00:06Z: WAL 430 MB, one write flush stalled 160 s, ~10 MB reclaimed | `/stats/events` |
| Jetstream | jetstream1 reconnected ~25 times/day on 08-27 with zero endpoint rotations; jetstream2 stable since | `/stats/history` |
| Staging | stopped since 2026-04-18 on an April image; `GOMEMLIMIT=400MiB` | `fly status` |

Both crashes: the cycle before was the day's largest (142,762 posts on 08-25; 114,015 on 08-27), the crash came within ~30 s of the next :55, and the run record was never written (`daily_sentiment.total_runs` = 23 on both days).

## 3. Capacity and memory headroom

### 3.1 Where the memory goes (measured at 114 k posts)

| Structure | Site | Cost | Scales with posts |
|---|---|---|---|
| `posts []store.Post` (full window) | `internal/store/post_buffer.go:97-108`, `cmd/hourstats/analysis.go:34` | 88 MB (773 B/post) | yes |
| `hydrated` copy | `cmd/hourstats/analysis.go:83-88` | +17 MB | yes |
| hydrator `uris` + `idx` map | `internal/hydrator/hydrator.go:114-119` | +7 MB | yes |
| ~4,561 goroutines at once | `internal/hydrator/hydrator.go:133-205` | +18 MB sys | yes |
| `ComputeTFIDF` one map per term | `internal/topics/tfidf.go:45-50` | 93–116 MB retained, 234–269 MB churn | capped at 20 k docs |
| `analyzerPosts` + `analyzed` (no prealloc) | `cmd/hourstats/helpers.go:16`, `internal/analyzer/sentiment.go:42` | +32 MB | yes |
| `extractTopics` (result never read) | `internal/analyzer/sentiment.go:58,83-140` | 145 MB churn, ~62 GCs/cycle | yes |
| SQLite mmap, 4 read conns × 128 MB | `internal/store/store.go:141,149` | up to 512 MB RSS (file-backed) | no |
| SQLite page cache 4 × 20 MB + write + maint | `internal/store/store.go:139` | ~84 MB anonymous, outside Go | no |
| `temp_store(MEMORY)` sorts | `internal/store/store.go:140`, `internal/store/topic_store.go:64-71,156-169` | ~14 MB each, up to 5 concurrent (exemplar queries full-scan `post_buffer`) | with table size |

Go live set ≈ 240–290 MB at cycle start → heap goal ≈ 400–500 MB at GOGC=75. Add ~600 MB of SQLite memory and the sum is 1.0–1.1 GB against 985 MB. The topic goroutine is launched deliberately in parallel with hydration (`cmd/hourstats/analysis.go:54-65`), so the two largest allocators overlap.

Cross-cycle retention in the Go heap was tested and ruled out: the scavenger returns pages between cycles. What ratchets is `heap_sys` (a permanent high-water mark), the never-closed read connections keeping their mmaps and caches hot, and the end-of-cycle purge of ~100 k rows re-dirtying pages right before the idle gap. The failure is floor-plus-burst: the previous large cycle raises the floor, the next cycle's burst crosses the line.

### 3.2 Why the metrics could not show it

`runAnalysisCycle` runs synchronously inside the main `select` (`cmd/hourstats/main.go:172-173`). For 8–11 minutes nothing else in that loop runs: the stats snapshot tick is held until the cycle ends (every snapshot is a post-cycle sample; the recorded goroutine peak of 43 versus a real ~4,500), WAL checkpoint ticks are dropped, the stall detector cannot fire, and SIGTERM is not handled (Fly's 15 s `kill_timeout` then SIGKILLs, skipping the write-flusher drain). The snapshots also record only Go runtime memory, not RSS.

### 3.3 Scaling walls (multiples of today's 114 k-post peak)

| Wall | Binding constraint | Multiple |
|---|---|---|
| Hydrator 12-min timeout, then silent exclusion of unhydrated posts | `internal/hydrator/hydrator.go:54,68`; `cmd/hourstats/analysis.go:70-72,83-92` | ~1.3× (≈150 k posts), already 1.05× the all-time peak |
| Memory OOM | 985 MB VM; ~1,160 B/post marginal | 1.0×, already breached at the daily peak |
| Per-IP AppView budget via the PDS | 3,000 req / 5 min; hydrator at 8.33 req/s = 83% | ~1.2× |
| SQLite single write connection (hydration UPDATEs + ingest + purge); `writeCh` fills and posts are dropped silently | `internal/store/store.go:131`, `cmd/hourstats/jetstream_consumer.go:63-68` | ~3–4× (estimate) |
| Hydration wall-clock exceeding the hour | ~12 k posts/min | ~5× |

## 4. API usage

Verdicts: Jetstream is the right ingest path; `getPosts` is the right hydration endpoint; posting (`createRecord`, `uploadBlob`, facets, reply refs, pinning with `SwapRecord`) is correct; grapheme handling with `[]rune` is safe because runes ≥ graphemes. Defects, ranked:

1. **No WebSocket read deadline, pong handler, or ping ticker** (`internal/jetstream/consumer.go:293-307`). A black-holed connection blocks `ReadMessage` until the kernel keepalive; the stall detector only logs. Explains the `events_skipped` burst of 65,199 after a reconnect (replay of the backlog).
2. **Hydration through the authenticated PDS** (`internal/client/bluesky.go:46,60`, `cmd/hourstats/analysis.go:67`) instead of the cached public AppView `public.api.bsky.app` that Bluesky recommends for unauthenticated public reads. `getPosts` needs no auth.
3. **No rate-limit or retry handling**: `RateLimit-*` headers ignored, no 429 backoff, a 502/504 discards 25 posts (`internal/hydrator/hydrator.go:158-163`); the only retry logic lives in dead code (`GetTrendingPostsBatch`, `internal/client/bluesky.go:73-321`).
4. **No per-request HTTP timeout and only 2 idle connections per host** for 10 workers (indigo uses `http.DefaultClient`), so most requests pay a fresh TLS handshake.
5. **Jetstream replay clobbers hydrated rows**: the upsert's `ON CONFLICT` sets `likes`, `reposts`, `replies`, `author_handle` from the incoming (zero/empty) values (`internal/store/write_batch.go:42-52`). Trending exemplar selection reads those columns.
6. **Cursor is not rewound on reconnect** (`internal/jetstream/consumer.go:265-268`) and is persisted every 10 s while posts may sit unflushed in `writeCh`, so a SIGKILL loses posts. `compress=true` (zstd, ~56% smaller) is unused.
7. **Consumer stat deltas reset on consumer restart** without a generation guard (`cmd/hourstats/jetstream_consumer.go:78-91`, `internal/stats/collector.go:205-217`).
8. The trending post can exceed 300 graphemes when every exemplar is dropped (`internal/topics/formatter.go:33-46`); `Authenticate` ignores the caller's context (`internal/client/bluesky.go:56-69`); Gemini has a call budget but no failure breaker.

Recommended hydration architecture (staged): first repoint hydration at `public.api.bsky.app` behind a fixed worker pool with timeouts and jittered retries; then subscribe to `app.bsky.feed.like` and `app.bsky.feed.repost` in Jetstream with `compress=true`, count engagement locally, handle post deletes from the firehose, and hydrate only the few hundred candidates that can be displayed (moderation labels and handles preserved for everything shown). Estimated effect: getPosts calls per cycle from 4,566 to ~30, hydration from 9.4 min to seconds, ~250 MB less JSON churn per cycle. Note that hydration today also serves as the liveness and moderation-label filter and as the source of author handles, so the redesign must replace those three roles, not just the ranking.

### 4.1 Cold-start replay (observed on staging 2026-09-02)

Starting the staging machine after 4.5 months resumed from the oldest cursor Jetstream still held (33 hours back). Jetstream replays at wire speed, the write flusher sustains ~1,400 posts/s, the 50,000-slot `writeCh` filled within minutes, and the consumer then dropped posts (14,025 by the first snapshot, still dropping 15 minutes later) with one WARN line per post (`cmd/hourstats/jetstream_consumer.go:63-68`). The same mechanism would fire on prod after any outage longer than a few minutes. Fixes: ignore or clamp cursors older than a configurable age (start from live and accept the gap), apply backpressure (block with a bounded wait instead of dropping) while replaying, and rate-limit the drop warning.

### 4.2 Shutdown drain order (observed on staging 2026-09-03)

On the SIGTERM issued by a rolling deploy, the write flusher's drain path ran after the store had been closed: `"shutdown drain flush failed" batch_size=18 error="begin post batch tx: sql: database is closed"`. The drain added in commit b6a725e only works if the store outlives the flusher; the shutdown sequence in `cmd/hourstats/main.go` closes the database first. Posts buffered at shutdown are lost on every deploy.

## 5. Statistics robustness

| Statistic | How computed | Verdict | Fix |
|---|---|---|---|
| Headline `+x.x%` | mean VADER compound × 100 over hydrated posts (`cmd/hourstats/sentiment.go:15-32`) | precise (SE ≈ 0.15 pp at 110 k) but biased ≈ +1.5 pp (estimate) by excluding deleted/moderated posts before scoring (`cmd/hourstats/analysis.go:83-92`); not a percentage of anything | score sentiment before the hydration filter; publish whole numbers with a CI |
| Category | mean compound vs ±0.3 (`cmd/hourstats/sentiment.go:27-31`) | degenerate: "neutral" in 4,798 of 4,798 runs | delete or rebase on the empirical distribution |
| Mood word | 7 tiers × 100 words on hourly values with daily-fitted thresholds (`internal/formatter/sentiment_100_words.go`) | ~0.45 SE per word; adjacent hours change words on noise | widen tiers, add hysteresis, recalibrate on rolling hourly data |
| Low-confidence guard | `< 500` posts (`cmd/hourstats/analysis.go:21`) | catches only near-total outages | ~10,000 absolute plus a relative check against the same UTC hour's 7-day median |
| English filter | client-declared `langs` contains `en`; no `langs` ⇒ dropped (`cmd/hourstats/jetstream_consumer.go:94-104`) | acceptable, undocumented | document; count the no-langs share |
| Top 5 | `likes+reposts+replies`, unweighted, no quotes; hydrated oldest-first over ~9 min so the oldest post is measured at 60 min of age and the newest at ~9 min | dominated by measurement age; no self/bot exclusion | rank at a fixed age or normalise by age; add quotes; exclude own DID |
| Daily average | unweighted mean of run values; point stamped at cycle completion so the 23:55 cycle lands on the next day (`cmd/hourstats/daily.go:46-54`, `cmd/hourstats/analysis.go:175`) | run-weighted despite a 6.7× diurnal swing; day boundary leaks one hour | weight by posts; stamp at window midpoint |
| Sparkline | time-based x, Gaussian smoothing in index space (`internal/sparkline/generator.go:794-833`); low-confidence points removed then interpolated | bandwidth silently doubled at the 30→60 min change; gaps invisible | time-kernel smoothing; break the line at gaps |
| Yearly chart | daily averages | approximately comparable across the cycle change | annotate the changeover |
| Trending topics | `ln(N/df)·Σ min(tf,3)` over posts with engagement > 0 in the last 2 h (`internal/topics/tfidf.go:66-71`, `internal/store/topic_store.go:69`) | corpus is effectively the previous hour only (engagement exists only for already-hydrated posts) yet labelled "2h"; `df` counts posts not authors, so one account can create a topic; Gemini has no temperature/seed; failure paths return nil and republish the previous snapshot | dedupe `df` by author; relabel; temperature 0; return errors so the post is suppressed |
| `total_firehose_posts` | `Load`-and-delta counter that the analysis cycle also `Swap(0)`s (`internal/stats/collector.go:236-241`, `cmd/hourstats/analysis.go:170`) | loses every post counted between a snapshot and the swap; also counts post-prefilter, not the firehose | monotonic counter with two cursors; persist `EarlyRejectedNonEnglish` |
| `posts_per_minute_avg` | `englishDelta / 30.0` (`internal/stats/collector.go:278`) | 19% wrong on post-cycle snapshots | divide by measured elapsed time |
| Health chart memory | Go `Sys` only (`internal/stats/collector.go:317`) | blind to ~200 MB of SQLite memory; `GCCPUFraction` is cumulative | add RSS from `/proc/self/status` |
| Retention | `PurgeExpiredRuns` and `PurgeExpiredSentimentHistory` have no production caller | `runs` and `sentiment_history` grow unbounded; CLAUDE.md wrong | wire them or fix the docs |

## 6. Recommendations

### Immediate (ops, no code)
1. `swap_size_mb = 512` in `fly.prod.toml` so a spike pages instead of killing the process.
2. Raise prod to 2048 MB (shared-cpu-1x supports it) as a stop-gap, or accept the current risk until the code fixes land.

### Short term (small, measurable code changes; staged on staging first)
3. Delete `extractTopics` and the unused `Topics` field. 145 MB churn and most GC cycles.
4. Two-pass `ComputeTFIDF`: count document frequency first, build author sets only for terms above `MinDocFrequency`. 80–110 MB at the OOM point.
5. Preallocate `analyzed`; hoist `analyzer.New()` to a singleton; narrow the hydration UPDATE.
6. Fixed worker pool in the hydrator instead of a goroutine per batch; own `http.Client` with a 15 s timeout and larger idle pool; 2 jittered retries on 429/5xx; treat the hydrator timeout as an error.
7. Remove `mmap_size` (or cut it sharply) and reduce the read pool from 4 to 3, one change at a time.
8. Then lower `GOMEMLIMIT` to a value that actually binds (~350–450 MiB) once the live set is known.
9. WebSocket read deadline + ping/pong; cursor rewind of a few seconds; `compress=true`; fix the upsert clobber.
10. Run the analysis cycle off the main loop with a re-entrancy guard, and record RSS, `HeapReleased`, `StackInuse` in snapshots.
11. Fix the firehose counter, `posts_per_minute`, and the consumer-restart deltas; make VACUUM conditional on freelist ratio.

### Medium term (architecture)
12. Chunk/stream the cycle (keyset-paginated reads, per-chunk VADER and hydration, running sums, 5-element heap). Removes the linear memory term.
13. Local engagement counting from Jetstream likes/reposts and candidate-only hydration (section 4).
14. Statistics: score sentiment before the hydration filter, publish a confidence interval, retire the dead category, widen the mood tiers, weight the daily mean by posts, fix the day boundary, dedupe TF-IDF by author.

## 7. Staging experiment plan (authorised 2026-09-03)

Tracked as hs-q37.1 … hs-q37.5. Staging runs with prod-equivalent memory settings but `ANALYSIS_OFFSET_MINUTES=25` so its hydration never overlaps prod's :55 window (prod and staging may share a Fly egress IP and the per-IP PDS budget).

0. In-cycle memory sampler (RSS, heap, goroutines every 500 ms; peak logged and stored as a `cycle_memory_peak` stats event).
1. Baseline: HEAD + sampler, 2+ full cycles.
2. Allocation fixes batch A (items 3–6), measure.
3. SQLite memory and config (item 7, swap, GOMEMLIMIT), one change at a time.
4. Hydration client and firehose correctness (items 6 and 9), measure cycle time and error counts.

Success criterion: peak RSS per cycle at ≥100 k posts comfortably below 700 MB on a 1024 MB VM, with no increase in unhydrated exclusions.

## 8. Staging experiment results (2026-09-02/03)

All runs on hourstats-staging: shared-cpu-1x, 1024 MB, `GOMEMLIMIT=800MiB`, `GOGC=75`, hourly cycle at :25, real posting to the staging account. Baseline is HEAD `9d3d34e` plus the in-cycle memory sampler. Batch A adds the allocation fixes (dead `extractTopics` removed, two-pass TF-IDF, preallocation, analyzer singleton, bounded hydrator worker pool, narrowed hydration UPDATE). Batch B adds batch A plus `SQLITE_MMAP_MB=0`, `SQLITE_READ_CONNS=3`, `swap_size_mb=512`. All code is on branch `exp/memory-staging`, uncommitted at the time of writing. Published output (label, net sentiment, hydration rate, error profile) was unchanged in every run.

| Cycle (UTC) | Build | Posts | Goroutines peak | Heap in use peak | Heap reserved max | Heap burst at start | RSS at 20–30 s | Post-hydration RSS peak | RSS between cycles | Cycle time |
|---|---|---|---|---|---|---|---|---|---|---|
| 01:25 | baseline | 92,036 | 3,702 | 217 MB | 259 MB | +119 MB (1.29 KB/post) | 351–355 MB | 620 MB | 308 MB | 1,073 s* |
| 02:25 | batch A | 84,906 | 86 | 126 MB | 171 MB | +58 MB (0.68 KB/post) | 260 MB | 617 MB | 327 MB | 879 s* |
| 03:25 | batch A | 71,683 | 74 | 114 MB | 171 MB | +33 MB (0.46 KB/post) | 407–411 MB | 588 MB | 311 MB | 787 s* |
| 04:25 | batch B | 59,743 | 56 | 96 MB | 127 MB | +27 MB (0.45 KB/post) | 154 MB | 229 MB | 167 MB | 350 s |
| 05:25 | batch B | 49,024 | 100 | 83 MB | 127 MB | +19 MB (0.39 KB/post) | 222 MB | 219 MB (+11) | 167 MB | 300 s |

\* The first three cycles were slowed by purging ~1.4 M rows replayed after the cold start (section 4.1); their post-hydration phases are not comparable in time. Cycle sizes fall through the night; per-post figures are the fair comparison for the cycle-start burst, and batch B has not yet been measured at a 100 k+ cycle.

What the runs established:

1. **The cycle-start burst is the Go side and batch A halves it.** Goroutines at cycle start went from one per hydration batch (3,702) to a fixed pool; heap-in-use peak fell 42–47%; the heap burst per post fell from 1.29 KB to ~0.45 KB; and Go's reserved heap stopped ratcheting between cycles (259 → 171 → 171 MB). This is the phase in which prod was killed.
2. **The post-hydration peak and the idle floor are SQLite, and batch B removes most of both.** With the 128 MB per-connection mmap gone and one fewer read connection, the post-hydration RSS rise fell from ~+400 MB to +11–90 MB, the in-cycle RSS peak from ~600 MB to ~229 MB, and the between-cycle floor from ~310 MB to ~167 MB. Cycle time did not suffer: the post-hydration phase took about a minute.
3. **Swap was never used** (0 of 512 MB across both batch B cycles); it is a safety net, not a working set.
4. **Side findings reproduced twice:** a rolling deploy's SIGTERM runs the flusher's drain after the store is closed (posts lost on every deploy, section 4.2), and a stale cursor floods the writer and drops posts (section 4.1).

### Recommendation for production

Promote in this order, without changing prod's machine size:

1. **Deploy the branch (sampler + batch A + batch B code) with `swap_size_mb = 512`, `SQLITE_MMAP_MB=0`, `SQLITE_READ_CONNS=3` in `fly.prod.toml`, during a low-volume hour (07:00–10:00 UTC).** Expected: idle floor from ~588 MB to roughly 200–300 MB, cycle-start burst halved, and peak RSS at a 114 k-post cycle in the 450–550 MB range instead of the ~986 MB high-water mark. Watch `cycle_memory_peak` events for the first day.
2. **After a week of `cycle_memory_peak` data, lower `GOMEMLIMIT`** to a value that binds (about 450 MiB) so the Go heap has a real ceiling under the SQLite allocations.
3. **Keep the stop-gap in reserve:** if a peak-hour cycle still exceeds ~700 MB after step 1, raise the machine to 2048 MB rather than tuning further.

Still untested or unmeasured, in rough priority:

- Batch B at prod-scale cycles (100–143 k posts). Staging should run through a US daytime peak (14:00–21:00 UTC) before promotion, or accept the extrapolation above.
- Hydration client hygiene and firehose correctness (hs-q37.5): public AppView host, worker-pool timeouts and retries, treating the hydrator deadline as an error, the upsert clobber, WebSocket read deadline and ping, cursor rewind and clamp, zstd. These fix the first scaling wall (the 12-minute hydrator timeout) and the silent data loss paths; none were exercised here.
- Fixing the shutdown drain order (section 4.2) so deploys stop losing buffered posts.
- Running the analysis cycle off the main loop with a re-entrancy guard, and recording RSS in stats snapshots.
- The statistics fixes in section 5 (sentiment before the hydration filter, dead category, counter defects, TF-IDF author dedupe, daily weighting); none change memory and none were touched.
- `SQLITE_TEMP_STORE=FILE` and `SQLITE_READ_CACHE_MB` are now configurable but were left at their defaults.
