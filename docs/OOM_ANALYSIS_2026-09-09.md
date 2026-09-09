# OOM kill of 2026-09-09 15:58 UTC: analysis and plan (hs-cbr)

## What happened

flyd recorded `exit_code=137, oom_killed=true` at 15:58:55 UTC, 3.9 minutes into cycle `run-20260909-155500`. Two earlier restarts fit the same pattern: 2026-08-25 19:55:45 and 2026-08-27 19:55:51, both about 45 s into a cycle. In all three cases the killed cycle followed a 110k+ post window and left no `sentiment_history` row.

Fly Prometheus (15 s samples) for the fatal cycle, MB:

| Time | Available | Cached | Swap free |
|------|-----------|--------|-----------|
| 15:55:00 | 561 | 490 | 510 |
| 15:55:30 | 151 | 192 | 509 |
| 15:56:00 | 0 | 1 | 315 |
| 15:57:30 | 1 | 1 | 1 |
| 15:57:45 | 0 | 9 | 245 |
| 15:59:00 | 0 | 10 | 15 |
| 15:59:15 | 772 (restarted) | | 512 |

About 1.3 GB of anonymous memory was allocated within 60 to 90 s of the cycle starting, pinned at the RAM plus swap ceiling for 90 s, released 250 MB once, and climbed again until the kill. The 14:55 cycle one hour earlier, with the same 121,706-post window, cost about 100 MB above idle for its entire run and peaked at 360 MB RSS. The burst is therefore data- or state-dependent, not proportional to window size.

## What has been ruled out

- Window size: 5 to 8 cycles a day exceed 100k posts and survive; the 121k cycle immediately before the kill survived.
- Hydration: bounded worker pool (10 workers), 2 retries through a shared limiter, responses not retained; live footprint about 9 MB. The 4,500-goroutine mode from August is structurally impossible in the current code.
- Topic analysis: token window capped at 20,000 rows, TF-IDF at 50 terms, groups at 10, so the quadratic loops in the offline fallback and cluster merge are bounded at kilobytes. The offline path has not run in the last 48 h of snapshots.
- SQL plans at cycle start: `GetPostsSince` walks `idx_post_buffer_created_at`; the token window query walks `idx_topic_tokens_created_at` with a 20k limit. No large in-memory sort or temp b-tree at cycle start. The unbounded `json_each` exemplar query exists but runs about ten minutes into a cycle, after the sparkline.
- Giant post text: the current buffer has a maximum text length of 589 bytes and 10 MB of text per hour. Disk write rate on the data volume during 14:55 to 15:55 matched neighbouring hours (0.5 MB/s between cycles, 1.6 to 2.0 MB/s during hydration), so a burst of oversized text into `post_buffer` did not happen in that hour.
- Future-dated rows: 123 in the buffer, not enough to matter. (Rows are purged by `inserted_at`, so `created_at >= cutoff` with no upper bound does admit them.)

## What is not ruled out

The runtime configuration allows the kill. `GOMEMLIMIT=800MiB` on a 962 MB machine, plus the modernc.org/sqlite arena that lives outside the Go heap (anonymous mmap, 150 to 250 MB during a cycle), means the process is permitted to reach 1.0 to 1.1 GB before the collector pushes back. Once the Go heap is partly in swap, every GC cycle faults it all back in, which is the plateau seen above. The 2026-09-03 architecture review said this limit was a no-op and scheduled a drop to about 450 MiB; that was never done.

For the heap to reach the limit under `GOGC=75`, the live set had to exceed roughly 460 MB, about four times a normal cycle. The only structure in the cycle that is retained, grows linearly, and is filled at a rate set by a streaming read is the post slice from `GetPostsSince` (no row limit, no text cap, no upper time bound). Nothing measured shows it being fed abnormal data in that hour, so the trigger remains unproven.

## Plan

1. Stop-gap, same day: `shared-cpu-2x` with 2048 MB and `GOMEMLIMIT=1200MiB` (binds at about 1.5 GB of 2.0 GB), `HEALTH_CHART_MEMORY_LIMIT_MB=2048`. If staying on 1 GB, use `GOMEMLIMIT=550MiB`. Roughly +$10 a month.
2. Evidence, half a day: extend the memory sampler into a guard that samples RSS at 1 Hz for the first five minutes, persists ticks to `stats_events`, writes a heap profile and goroutine dump to `/data` at a warn threshold (about 65% of machine memory), and cancels the cycle at a trip threshold so the process survives and the hour is marked `low_confidence`.
3. Bounds, one day: cap `text` at 1,000 bytes at ingest and in `GetPostsSince`; add a row limit and an upper time bound to the window read; purge future-dated `topic_tokens`; compute the emoji shadow mean without a second analysed-post slice; drop text after the top-3 selection.
4. Structural, three to five days, after 1 to 3 are stable: stream the window in chunks of about 5,000 posts with running sums and a bounded top-K heap, so memory is flat at any volume. Land behind a flag and compare `net_sentiment_percent` bit for bit on staging.

Acceptance: RSS peak under 500 MB at 150k posts; a forced trip leaves a heap profile and a surviving process; sentiment outputs unchanged on a replayed fixture; zero OOM kills over 30 days including at least 100 cycles above 100k posts.

Reproduce on staging with a `cmd/loadgen` that fills `post_buffer` with N rows of configurable text size, then a `DRY_RUN` cycle; `make sync-staging` does not copy `post_buffer`.
