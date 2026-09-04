# Sentiment Mood-Word Calibration Review (September 2026)

**Review date:** 2026-09-04
**Supersedes thresholds in:** [SENTIMENT_CALIBRATION_ANALYSIS.md](SENTIMENT_CALIBRATION_ANALYSIS.md) (January 2026)
**Code:** `internal/formatter/sentiment_100_words.go`
**Data:** prod essential-tables backup `s3://hourstats-sqlite-backups/prod/2026-09-04T000444Z.db`, exported to
`analysis/hourly_sentiment_2026.csv` (5,488 cycles, 2026-01-30 → 2026-09-04) and
`analysis/daily_sentiment_2026-09.csv` (352 days, 2025-09-14 → 2026-09-02).

## Summary

The January 2026 tiers were fitted to the 30-minute-cycle era, when each
cycle sampled ~8k posts and intraday sentiment swung by a median 7.2 points
per day. Since February prod runs hourly cycles of ~80k posts and the
per-cycle distribution is far tighter (median daily spread 2.6 points,
standard deviation 1.34). Against that distribution the old tiers were
mis-centred and mis-scaled:

- The median hour (10.60%) sat on the Below Average / Typical boundary
  (10.5%), so half of all posts were labelled below average or worse.
- **"melancholy" was the single most-posted mood word** (8.5% of all
  hourly posts since March) and "subdued" the second (4.7%). Both live in
  the "Unusually Low" tier, which actually fired for 16.4% of hours and, in
  March 2026, for 51.5% of hours.
- **Extreme Positive (≥18%) never fired** in seven months. The best hour of
  2026 so far reached 16.58%. Extreme Negative (<0%) also never fired.
- An off-by-one in the within-tier interpolation scaled position by
  `numWords-1`, so the top word of every half-open tier
  (`despondent`, `solemn`, `settled`, `bright`, `buzzing`, `dreadful`) was
  unreachable. Only 77 of the 100 words ever appeared.
- Word order inside tiers was not monotonic in intensity: at 9.4% the bot
  said "melancholy" while 0.5% would have said "anxious", and at 18.0% it
  said "euphoric" while "celebratory" required 30%.

## 1. Per-cycle distribution, hourly era (Mar–Sep 2026, n = 4,446)

| Statistic | Value |
|-----------|-------|
| Mean | 10.60% |
| Std dev | 1.34 |
| Min / max | 2.20% (a 30-post cycle on 2026-08-16; `daily_sentiment` excludes such tiny cycles, hence 3.68% in the era table below) / 16.58% |
| p1 / p5 / p10 | 6.93 / 8.48 / 9.05 |
| p25 / p50 / p75 | 9.85 / 10.60 / 11.35 |
| p90 / p95 / p99 | 12.07 / 12.67 / 14.48 |
| p99.5 / p99.9 | 15.02 / 15.94 |

Monthly means drift within roughly one standard deviation: March 9.40%,
June 11.59%. There is also a stable intraday cycle, with 07–13 UTC hours
averaging ~11.2% and 22–03 UTC hours ~10.1%. Fixed thresholds therefore
produce more "above average" words in the European morning and more
"below average" words overnight US time. That is a real signal, not a
table defect, and a rolling baseline was considered and rejected as it
would hide month-scale mood shifts such as March 2026.

### Era comparison (from `daily_sentiment`)

| | 30-min era (Sep 2025–Jan 2026, 140 days) | Hourly era (Mar–Sep 2026, 186 days) |
|---|---|---|
| Daily-average mean / sd | 10.51 / 1.88 | 10.60 / 1.06 |
| Daily-average min / max | 5.73 / 19.77 (Christmas) | 7.27 / 14.48 (2026-06-01) |
| Median intraday spread | 7.23 | 2.64 |
| Lowest / highest cycle | -4.53 / 26.64 | 3.68 / 16.58 |

The 2025 holiday extremes (Christmas 19.77% daily average, New Year 26.64%
peak) came from small 30-minute samples. Comparable days under hourly
sampling will land lower. The only 2026 hours above 15% so far fell on
2026-02-09, Valentine's Day, 2026-02-27, 2026-05-11, 2026-05-14 and a
cluster of days from 2026-05-31 to 2026-06-14, peaking at 16.58%.

## 2. Old tiers against hourly-era data

| Tier | Old range | Share of hours | Words that actually appeared |
|------|-----------|----------------|------------------------------|
| 1 Extreme Negative | < 0 | 0.0% | none |
| 2 Unusually Low | 0 – 9.5 | 16.4% | melancholy 8.5%, subdued 4.7%, weary 1.4%, somber 0.9%, … |
| 3 Below Average | 9.5 – 10.5 | 30.2% | distracted, uncertain, reflective, reserved, … |
| 4 Typical | 10.5 – 12.5 | 47.4% | chill, peaceful, content, steady, … |
| 5 Above Average | 12.5 – 14 | 4.5% | happy 0.8%, upbeat, cheerful, … |
| 6 Unusually High | 14 – 18 | 1.6% | excited, vibrant, energetic, … |
| 7 Extreme Positive | ≥ 18 | 0.0% | none |

Tier 2's interpolation bounds were 0–9.5 while observed Tier 2 values were
almost all 7–9.5, so ten of its fifteen words appeared but five of them
(melancholy, subdued, weary, somber, sullen) carried ~99% of the tier, and
the mildest sentiment in the tier drew the darkest reachable word
("melancholy"; "despondent" was unreachable).

## 3. New tiers

Boundaries are placed on hourly-era percentiles so the tier names describe
how unusual an hour actually is:

| Tier | New range | Percentile | Share Mar–Sep 2026 | Share Jun–Sep 2026 |
|------|-----------|------------|--------------------|--------------------|
| 1 Extreme Negative | < 0 | never observed | 0.0% | 0.0% |
| 2 Unusually Low | 0 – 8.5 | < p5 | 5.1% | 2.4% |
| 3 Below Average | 8.5 – 9.75 | p5 – p22 | 17.1% | 14.1% |
| 4 Typical | 9.75 – 11.5 | p22 – p78 | 56.9% | 61.6% |
| 5 Above Average | 11.5 – 12.75 | p78 – p95 | 16.3% | 16.3% |
| 6 Unusually High | 12.75 – 15 | p95 – p99.5 | 4.1% | 4.7% |
| 7 Extreme Positive | ≥ 15 | top 0.5% | 0.5% (23 hours on 10 days) | 0.8% |

Interpolation bounds for the open-ended tiers were tightened to the range
that occurs: Tier 2 clamps at 3.5% (the lowest full-size hourly cycle is
3.68% on 2026-04-07 with 139k posts, and eight hourly-era cycles fell between
3.5% and 5%; the only lower values came from a 30-post cycle and the 30-min
era), Tier 7 clamps at 20%, Tier 1 keeps -10%.

### Interpolation fix

Position within a tier is now divided into `numWords` equal-width slots
(`int(position * numWords)`, clamped), so each word owns a slice of the
range and the last word is reachable. `TestGetMoodWord100_EveryWordReachable`
and `TestGetMoodWord100_Monotonic` guard this.

### Word reordering

The 100-word lexicon is unchanged; four tiers were reordered so that word
intensity rises monotonically with sentiment and neighbours across a tier
boundary are close in mood:

- Tier 1: hostile → angry → dreadful → grim → miserable (miserable now borders "despondent").
- Tier 2: despondent → glum → sullen → somber → melancholy → pessimistic → cynical → anxious → agitated → irritable → tense → uneasy → restless → weary → subdued (subdued now borders "flat").
- Tier 3: flat → downbeat → tired → sluggish → solemn → wary → skeptical → cautious → uncertain → ambivalent → distracted → reserved → pensive → quiet → reflective (reflective now borders "calm").
- Tier 7: celebratory → jubilant → elated → ecstatic → euphoric (a 15% hour is "celebratory"; "euphoric" needs ≥19%).

Tiers 4–6 keep their January 2026 order.

## 4. Result on the same data

Replaying Mar–Sep 2026 through the new mapping
(`HS_HOURLY_CSV=$PWD/analysis/hourly_sentiment_2026.csv HS_SINCE=2026-03-01 go test ./internal/formatter -run TestMoodWordDistribution -v`, run from the repo root; the test also asserts at least 80 distinct words and no word above 5%):

| | Old mapping | New mapping |
|---|---|---|
| Most frequent word | melancholy 8.5% | witty 2.4% |
| Distinct words used | 77 / 100 | 92 / 100 |
| Hours labelled below Typical | 46.5% | 22.2% |
| Hours labelled above Typical | 6.1% | 20.9% |
| Never-used words | 23, incl. all of Tier 7 | 8: all of Tier 1, plus elated, ecstatic, euphoric |

The remaining unused words are the extremes that the data has not reached
since the cadence change (Tier 1) or that need a holiday-scale hour
(≥17% for "elated").

## 5. Known limits

- Thresholds are fixed and calibrated to hourly cycles of ~80k posts. If
  `ANALYSIS_INTERVAL_MINUTES` returns to 30, per-cycle variance roughly
  doubles and the outer tiers will fire much more often. Re-run the
  distribution test after any cadence or sampling change.
- Christmas / New Year 2026 will be the first holiday season under hourly
  sampling; expect daily averages in the 13–16% range and peak hours
  clearing 15% for most of Christmas Day. If the holiday peak stays under
  17%, only the first two Tier 7 words will appear; that is acceptable.
- No negative cycle has been observed since 2025-12-22 (30-min era). Tier 1
  stays as a safety net.
