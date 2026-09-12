# Like/repost stream cost on staging (hs-wsp.7)

Measured on `hourstats-staging` (shared-cpu-1x, 1024 MB) from 2026-09-11 00:04 UTC, running branch `modernisation-2026-09` on Jetstream v2 with dictionary zstd and `JETSTREAM_EXTRA_COLLECTIONS=app.bsky.feed.like,app.bsky.feed.repost` in count-only mode (frames matched by a byte scan and dropped before any parse). Baseline is the same machine on 2026-09-10 02:10–02:20 UTC, previous build, posts only, between cycles.

## Volume (first 30 minutes, 00:00–00:30 UTC, a quiet hour for Bluesky)

| Stream | Events / 30 min | Events / s | Compressed bytes / min |
|---|---|---|---|
| `app.bsky.feed.post` (firehose total) | 65,331 | 36 | — |
| `app.bsky.feed.like` | 364,726 | 203 | ~8.6–9.1 MB |
| `app.bsky.feed.repost` | 66,665 | 37 | ~1.5–2.0 MB |
| All frames received | — | — | 132 MB compressed / 329 MB decompressed per 30 min (2.5×) |

Likes and reposts together are about 6.6× the post event rate by count and roughly 80% of the bytes on the wire.

**Caveat on the post row:** from about 01:00 UTC the v2 live tail began delivering repo backfills as ordinary creates with old `createdAt` (see hs-wsp.5 notes), so post counts after the first half hour are inflated by up to 2×; the like and repost rows are unaffected. The 00:00–00:30 window above predates the backfill.

## Cost (Fly Prometheus, 10-minute rates)

| Sample | Build | CPU busy (% of 1 vCPU) | Net in | Memory used |
|---|---|---|---|---|
| 2026-09-10 02:20 | previous, posts only, idle | 1.8% | 38 KB/s | 430 MB (post-cycle) |
| 2026-09-11 00:20 | this branch + likes/reposts, idle | 4.3% | 107 KB/s | 229 MB |
| 2026-09-11 00:36 | same, window includes the 00:25 cycle | 6.0% | 134 KB/s | 312 MB |

Count-only cost of the two extra collections plus v2 decompression: about +2.5 percentage points of one shared vCPU at idle and +70 KB/s inbound. Memory is unchanged (the drop is the smaller post-restart buffer, not the transport).

## Peak-hour re-read (2026-09-11 15:00–21:00 UTC, idle windows between cycles)

| Sample (UTC) | Staging CPU busy, % of 1 vCPU (posts + likes + reposts count-only + diagnostics) | Prod CPU busy per vCPU (posts only) | Staging net in | Prod net in |
|---|---|---|---|---|
| 15:20 | 5.4% | 1.6% | 183 KB/s | 78 KB/s |
| 16:20 | 5.7% | 1.6% | 187 KB/s | 64 KB/s |
| 17:20 | 11.0% | 1.6% | 611 KB/s | 82 KB/s |
| 18:20 | 6.2% | 2.2% | 258 KB/s | 158 KB/s |
| 19:20 | 5.2% | 1.4% | 160 KB/s | 58 KB/s |
| 20:20 | 5.7% | 1.4% | 162 KB/s | 59 KB/s |

At the US daytime peak the count-only like/repost stream costs about 4 percentage points of one shared vCPU and 100–130 KB/s inbound; the 17:20 sample coincides with an upstream re-delivery burst (611 KB/s) and is not representative. Prod's figure is per vCPU on a shared-cpu-2x.

## Recommendation

Go. Even if parsing the like/repost subject URI and incrementing an in-memory counter costs three times the count-only figure, local engagement counting stays around 12% of one vCPU at the daytime peak and under 10% off-peak. Two caveats before hs-0k9 starts:

1. This is a quiet hour. Re-read the same three Prometheus queries after the US daytime peak (14:00–21:00 UTC) on 2026-09-11; the like rate there is typically 2–3× higher.
2. The subject URI of a like is inside the record, so the counting design must extract it with a bounded byte scan (as the collection match does today), not a full JSON decode, to keep the cost near the measured figure.

Counters are on the 00:30 `stats snapshot taken` line (`app.bsky.feed.like.events`, `bytes_received`, `bytes_decompressed`) and in the once-a-minute `jetstream extra collection volume` log line.
