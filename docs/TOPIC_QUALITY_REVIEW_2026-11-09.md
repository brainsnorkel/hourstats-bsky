# Topic picking quality review, 2026-11-09

Scope: the trending reply (3 labels plus exemplar links), the rank-1 label stored as `sentiment_history.top_topic` and shown in the 7-day footer, and the weekly "Stickiest topic" line. Evidence: the public feed 2026-10-31..11-08 (194 trending replies, 520 label-hours Nov 1-8), a prod backup to 2026-11-08T00:04Z (166 `topic_snapshots` rows over 48 cycles, 194 `topic_identity` rows, `topic_daily` since Oct 1, `sentiment_history`), and the code at `main` 7d543db. Five stage reviews were verified adversarially; only confirmed or upgraded findings appear, at verified severity. Scripts and extracted data lived under /tmp on the review machine and are not preserved; every number is reproducible from the public feed and the daily S3 backup as described in the appendix.

## 1. Executive summary

The bot is right about what Bluesky is talking about and unreliable about how it names it and when. Four things damage what a reader sees.

1. Wrong year. The 2026 midterms were published as "2024 US Presidential Election" and variants: 133 of 895 rank-1 hours since Oct 1, a third of all hours in the weeks of Oct 19 and Oct 26, and 20 of 185 rank-1 slots in election week. The prompt has no date and the tokenizer drops every year.
2. One hour late. The TF-IDF corpus requires engagement, which only hydration writes, and the topic goroutine starts before hydration. Every trending reply describes the previous hour; exemplar links were a median 118 minutes old at posting, 79% older than an hour.
3. The weekly line names the wrong subject. Replaying `GetTopTopicForRange` over five weeks, two print "Halloween, trending 76 of 168 hours" while the election held rank 1 for 123 hours, and the current week credits the midterms with 61 of at least 115 rank-1 hours. Cause: label churn (48 election labels in 109 posts, relabelled on 75% of consecutive appearances) on top of identity fragmentation (47 live election identities).
4. Bare top slots. 91 of 471 non-meme topic lines (19%) and 39 of 180 rank-1 lines (22%) have no sample post; year-bearing labels lose the link three times as often.

## 2. Scorecard

| Stage | Works | Where quality is lost | Evidence |
|---|---|---|---|
| Extraction | Spam gates hold; bigrams repair name splits | IDF computed on the window it scores; corpus is the previous hour; years dropped; bigrams cross erased punctuation | `tfidf.go:28,55`; `topic_store.go:70`; `tokenizer.go:14,23,32,41`; Wordle in 35 labels on 7 of 8 days |
| Grouping and labelling | Big stories named promptly (Miller at rank 1 within 2 h); fallback chain works | No date, prior labels, temperature or naming rule; "merge aggressively" yields 27% compound labels and a political catch-all; phrases published verbatim | `grouper.go:842-898,871,876-881,866`; 139/520 labels with and/&; 'trump' in 39 of 48 rank-1 keyword sets |
| Ranking | Within-hour keyword sets disjoint (0/239 pairs) | Any-single-keyword author hit; generic unigrams lift catch-alls and mislabelled clusters; no publication floor | `ranker.go:47-52`; 'Cinema and Film Watching' 699 authors on film/movie/star/seen; 'Utah' 200 on utah/god/war |
| Identity tracking | Stable once a keyword set is large (104 continuations, median J 0.55) | Jaccard includes unmatched multiword synonyms; strict > 0.3 on 2-4-term sets; pool never merges; canonical_label overwritten hourly, never shown | `tracker.go:14,63-65,82,91-93`; `topic_store.go:323`; 47 live election identities |
| Exemplars | 45 of 50 sampled links on topic; 0 replies or thin posts | 1 in 5 lines bare; top 3 by engagement, one-shot validation on label text; 6 h pool for a 2 h topic; pick never persisted | `exemplar_rank.go:137,338`; `exemplar.go:92,112`; `grouper.go:1204`; `topic_store.go:307` uncalled |
| Aggregates and weekly | Two-axis aggregation is a sensible patch; weekly year guard | top_topic unguarded; topic_daily keyed by fragmented ID or exact label; counts top-5 hours while 3 are shown | `analysis.go:245-249`; `extremes.go:28,33`; `internal/store/reports.go:44,131-135`; 35/166 snapshot rows are rank 4-5 |

## 3. Confirmed findings, by reader impact

### R1. Wrong-year labels (high; fix small)
Evidence: `tokenizer.go:14,32` drop pure-digit words before bigrams form at `:38-44`; `buildPrompt` `grouper.go:842-898` states no date and its "Good labels" at `:871` are 2024-era events; `validLabelPattern` `:366` admits digits; the only year check is `weekly.go:107,116`. Data: 133/895 rank-1 labels since Oct 1 contain 2024 (ISO weeks Oct 19: 55/168, Oct 26: 56/168, Nov 2: 15/144); Nov 1-8 feed 21/520 labels, 20 at rank 1; a 2024 and a 2026 election label published on 5 of 8 days; identity '2024 US Election and Midterms' lived Oct 1 to Nov 3 with keyword 'midterms'; exemplars under these labels are 2026 posts. Year-bearing election labels also lose their exemplar at 37% vs 11%, because `buildValidationPrompt` `grouper.go:1204` sends the label text only.
Effect: a two-year-old election shown as the top topic in one hour in three for two weeks, and in the footer for 7-day stretches (R9).
Root cause: time-blind prompt plus year-stripping tokenizer; the guard sits on the least visible surface.
Fix: "Today is YYYY-MM-DD UTC; no year in a label unless it appears in the terms" in `buildPrompt`, undated examples, keep `^(19|20)\d\d$` as a token, and apply `labelYear` once in `validateClusters` so the hourly list, `top_topic` and weekly all inherit it.

### R2. The corpus is the previous hour (high; fix small, with a caveat)
Evidence: `GetTopicTokensSinceLimit` `topic_store.go:65-72` requires `likes+reposts+replies > 0`; ingest inserts zeros (`write_batch.go:118`) and only `UpdatePostEngagement` `post_buffer.go:168-170`, called from `hydrator.go:360`, writes engagement; the topic goroutine starts at `analysis.go:398` before `h.Hydrate` at `:409` under "no dependency on hydration" (`:383`); the 2 h cutoff `analyzer.go:160` and `LIMIT 20000` `:131,171` then yield the previous cycle's hydrated window. Recorded unfixed in `docs/ARCHITECTURE_REVIEW_2026-09-03.md:106`. Proxy: 394 exemplar links, age at posting p25 72, median 118, p75 177, max 239 min.
Effect: "Trending topic samples" under hour H is about H-1; breaking stories surface a cycle late.
Caveat: dropping the join alone shrinks the window: Nov hourly windows average 98,987 posts, so `LIMIT 20000` newest-first would keep 20-30 minutes.
Fix: start `RunAnalysisCycle` after `Hydrate` (3.8-flash costs ~5 s), or drop the join with an interval cutoff and a raised or sampled limit; log min/max `created_at` on the "loaded tokens" line.

### R3. Weekly "Stickiest topic" names a secondary subject or halves the dominant one (high; fix medium)
Evidence: replaying `topTopicByIDQuery`/`topTopicByLabelQuery` `internal/store/reports.go:150-199` with the choice rule `:131-135` (`verify/weekly_replay.py`): Oct 19-25 prints "Halloween, 76 hours" while rank-1 that week was election-family 123/168 and Halloween 2; Oct 26-Nov 1 prints Halloween 85 vs election 103/168; Nov 2-8 (rows to Nov 6) prints '2026 Midterm Election Results' 61 (one ID) vs election 115/145 across 39 IDs. Appearances count all 5 stored ranks (`internal/store/reports.go:44`) while 3 are published (`formatter.go:32`); Halloween's 76 came entirely from best_rank 2-4.
Effect: the report's one factual topic line wrong or understated in 3 of 5 replayed weeks.
Root cause: neither topic_id (R5) nor exact label (R4) is a subject; the by-label axis rewards label stability, so a steadily labelled second-tier topic beats a subject carried under 30-48 labels.
Fix: aggregate by subject (union-find the week's IDs on keyword overlap or shared label content words, show the modal label), or group `sentiment_history.top_topic` rank-1 hours by content-word key; add `AND t.rank <= 3` to the rollup.

### R4. Label churn with no canonical name (medium; fix medium)
Evidence: `geminiGenConfig` `grouper.go:161-165` sets no temperature (0.2 added in 67fde83, removed in f67ddb7); `buildPrompt` gets no prior labels and has no naming rule; `UpsertTopicIdentity` overwrites `canonical_label` hourly (`topic_store.go:323`, from `tracker.go:93`); nothing displays it and `FormatTrendingPost`'s `previous` parameter (`formatter.go:52`) is unused. Data: election family 48 labels in 109 posts, changed on 81 of 108 transitions; Halloween 76%, Wordle 74%, Stephen Miller 85%; topic_id 990d1159 carried 8 labels in 10 snapshots.
Effect: consecutive hours read as near-duplicates; the footer caption depends on which variant the extreme hour drew; R3 follows.
Fix: temperature 0.2-0.3; pass up to 30 live identity labels with "reuse exactly when the subject is the same"; naming rule (common English name, one subject, no year unless in terms); publish the modal label once an identity has 3 appearances.

### R5. Identity fragmentation (medium; fix medium)
Evidence: `tracker.go:63-65` compares keywords plus synonyms; `:82` requires strictly `> 0.3` (`:14`); `:70-78` takes one best match; nothing merges. 352 of 374 synonyms contain a space and never match a `_`-joined token (`tokenizer.go:42`), yet dilute Jaccard: in all 5 adjacent-hour election splits keywords-only Jaccard was higher (0.45 vs 0.28, 0.58 vs 0.39). 86 of 194 identities have 4 or fewer terms; one Miller split scored exactly 0.30. Pool at Nov 7: 47 live election identities (7 labelled 'Donald Trump'), 8 Wordle, 9 Halloween.
Effect: indirect (topic_id is never shown), through R3.
Fix: keywords only (or tokenised synonyms); overlap coefficient or `>=` with a size-aware threshold; merge identities when a cluster exceeds threshold against two (keep the oldest ID, alias the rest).

### R6. Any-keyword ranking and the political catch-all (medium; fix medium)
Evidence: `grouper.go:876-881` ("aggressively merge", "every input term must appear in exactly one group"); `ranker.go:47-52` counts an author on the first matching token. 'trump' in 39 of 48 rank-1 keyword sets; 20 of 48 rank-1 clusters hold 10-18 keywords including 'party' (22), 'president' (18), 'blue' (14); 'Cinema and Film Watching' (a meme prompt, is_meme=0) took rank 1 at 2026-11-06T19:55 with 699 authors on film/movie/star/seen/watched/favorite and became the 7-day-low caption in 48 footers; 'Utah' (200 authors) on utah/god/war. Caveat: Trump does dominate most hours and the effect size needs `topic_tokens`.
Effect: rank 1 is a padded umbrella; specific subjects sit unseen at rank 4-5; mislabelled clusters reach the footer.
Fix: count a post on 2 or more distinct keyword hits or a compound hit, or weight by `DocFreqStats` (`exemplar_rank.go:91`); allow `__discard__`; drop 'Donald Trump' from the examples.

### R7. Compound labels (medium; fix small)
139 of 520 labels (27%) and 51 of 185 rank-1 labels contain ' & ' or ' and ' (the stage review's 104 missed every ampersand). No merge signature in any snapshot, so these follow `grouper.go:876-881`. 'Donald Trump and US Elections' is a third topic to the footer and weekly. Fix: "a label names exactly one subject; never join with and/&", drop the exactly-one-group rule, log the compound count.

### R8. Copypasta chains become labels (medium; fix small)
`tokenizer.go:23` erases punctuation and `:41` keeps a bigram with one stopword side, so 'canned_post' spans a full stop; `detectOverlappingPhrases` `grouper.go:756-831` chains up to 8 (`:754`) through any boundary word; `:866` says use the phrase as the label; `:366` accepts a lowercase leading conjunction. Snapshot 2026-11-07T11:55 keywords the_cybertruck, cybertruck_getting, getting_canned, canned_post, post_vehicle, vehicle_women weld two memes through 'post'. Published: 'and vote today' at rank 2-3 three times on election day, 'monitoring the results' in three spellings, 'The Cybertruck Getting Canned Post Vehicle Women'; 7 of 520 labels start lowercase; the Cybertruck copypasta held a top-3 slot 17 of 22 hours under 4 labels. Fix: split on `[.!?\n;:]` before bigramming, both bigram words non-stopword, chains capped at 4 and never through a stopword, strip leading stopwords, tell the model to name the subject and set is_meme.

### R9. Footer and top_topic have no guard (medium; fix small, folds into R1)
`recordTopTopic` `analysis.go:245-249` stores rank 1 verbatim; `weekExtremes` `extremes.go:28,33` copies it. Replaying extremes hour by hour: 71/912 footers since Oct 1 named a 2024 election as the week's high or low (44 consecutive hours once), 266/912 a fused label; Nov 1-8 feed: 0 wrong-year, 69/386 fused, 'Cinema and Film Watching' 43. Fix: guard at label validation (R1), fall back to rank 2 when rank 1 fails.

### R10. One topic line in five has no sample post (medium; fix medium)
`isLabelAnchor` `exemplar_rank.go:137` precedes the generic test and `:338` waives the relevance floor on a strong anchor, so for thin clusters ('Texas Longhorns College Football', keywords [texas, games]) the 3 sent to Gemini are the most-engaged 'texas' posts; `exemplarTopK=3` `exemplar.go:92`; all rejected clears the URI with no second round (`:474-482`); validation sees the label only (`grouper.go:1204`). Data: 91/471 non-meme lines bare, rank 1 39/180; Wordle 12/35, election 36/140, Trump 5/70. Fix: tokenise synonyms into anchors, send Description with the label, one more validation round over the next 3, require two label words for a generic anchor.

### R11. Exemplar pool is 6 h for a 2 h topic and picks repeat (medium; fix small)
`exemplar.go:112` cutoff 6 h (bounded by the 3 h purge) vs `analyzer.go:160` 2 h; score is relevance x log engagement with no recency. Age median 1.84 h, 44% over 2 h; 66 of 380 slots (17%) repeat an earlier hour's post, two posts linked 4 hours running. Engagement is frozen at each post's one hydration, so an early mid-engagement post keeps winning. Fix: cutoff equals the token window, mild recency factor, re-score picks with the gate's live counts, skip the previous snapshot's URI once it is persisted.

### R12. Thin hours and silent filters (medium; fix small)
27 of 185 posts list 1-2 topics and the snapshots hold only 1-2 clusters (Nov 6 01:55 and 02:55: one 15-keyword cluster each; 18:55 and 19:55: a 1,300-2,400-author political cluster present before and after is absent). `filterGenericClusters` `grouper.go:721-741` is the only stage with no log line and bars 'news', 'social', 'media', 'movie'; `grouper.go:319-321` truncates to 10 in model order before dropping `__discard__`. Fix: log per-stage cluster counts and each generic drop; filter the sentinel before truncating.

### R13. Frequency, not trend (medium; fix medium)
`tfidf.go:28,55` compute IDF over the rows being scored, so the score is monotone in df below N/e and nothing contrasts the hour with yesterday. Wordle-family labels on 63 of 65 days, 35 published Nov 1-8 at 07-09 and 14-15 UTC; exact 'Donald Trump' in 24 of 179 hours, any Trump label in 70. Real subjects, so medium. Fix: score df against the prior 24 h already in `topic_tokens` (burst ratio or z-score), keeping the df>=10 and 3-author floors.

### Low findings (confirmed)
Fixed 50-term budget, no publication floor (17 of 131 top-3 rows under 100 authors; `types.go:9`; CLAUDE.md's "156-term prompt" is wrong). ASCII-only tokens ('Dončić' -> 'don'; AI/UK/EU never terms; `tokenizer.go:13,29`). Only US headlines reach the prompt (38 per feed, cap 30 at `headlines.go:69-71`); failures logged at Debug (`:52`). Keyword overwrite lets identities drift for months (`tracker.go:91-93`), though no replayed week was affected. Chosen exemplar never persisted (`UpdateSnapshotExemplar` `topic_store.go:307` uncalled; 166/166 rows empty); `exemplar_dropped` carries no reason. Meme search links are two-word compounds with stopwords ('monitoring the'; `formatter.go:286-309`). Dead exemplar guards (root boost, hashtag penalty, IsRepetitive) since ingest filters first (`jetstream_consumer.go:270-278`). Footer labels cut mid-word in 19/185 posts (`formatter.go:38-40`). 51/185 posts give 2-3 slots to the US-politics family. `IsRepetitive` counts stopwords, excluding 15% of posts over 40 words (`tokenizer.go:139-143`); the `#` gate excludes 17.6% of English root posts. The corpus-too-small guard counts unfiltered rows against a filtered read (`topic_store.go:100-109` vs `:70`).

## 4. Root-cause map

- Date-blind prompt + year-stripping tokenizer -> wrong-year labels (R1) -> wrong footer captions (R9) -> the weekly guard drops the line rather than fixing it; label-only exemplar validation turns the same wrong or over-specific year into bare top slots (R10).
- Engagement join + pre-hydration read -> previous-hour corpus (R2) -> stale, repeated exemplars (R11) and late breaking news.
- Stateless prompt, default temperature, canonical_label overwritten -> label churn (R4) -> by-label weekly axis fails; synonym-diluted Jaccard, strict threshold, no merge -> identity fragmentation (R5) -> by-ID axis fails -> weekly names Halloween or halves the midterms (R3); counting ranks 4-5 pads "N of 168 hours".
- Windowed IDF (R13) + "every term in exactly one group" -> generic unigrams attached to the biggest cluster -> any-keyword ranking (R6) -> political catch-all at rank 1, compound labels (R7), mislabelled memes in the footer; silent discard -> thin hours (R12).
- Punctuation-blind bigrams + one-stopword rule + phrase chaining + "use the phrase as the label" -> fragment labels, welded memes (R8), two-word meme search queries.

## 5. Recommendations in priority order

Items 1, 2, 4 and 10 are prompt, config or small code fixes; the rest are design changes.

| # | Change | Effort | Metric moved | Measure from |
|---|---|---|---|---|
| 1 | Date line and undated examples in `buildPrompt`; keep 4-digit years as tokens; `labelYear` guard in `validateClusters` with rank-2 fallback | small | wrong-year rank-1 share (133/895; target 0) | `sentiment_history.top_topic` matching a past year; feed labels |
| 2 | Topic goroutine after `Hydrate`, or drop the engagement join with interval cutoff and raised limit; log corpus `created_at` range | small | corpus lag (exemplar age median 118 min; target under 60) | "loaded tokens" log line; exemplar `createdAt` vs cycle start |
| 3 | Naming rules (one subject, no and/&, reuse a listed recent label); temperature 0.2-0.3; pass 30 live identity labels | medium | compound share (27%); relabel rate (75%) | feed labels: and/& share; same-family label change rate across consecutive replies |
| 4 | Tokenizer sentence split, both bigram words non-stopword; chains capped at 4, never through a stopword; meme label names the subject | small | fragment labels (7 lowercase, 3 'and vote today'); welded memes | `topic_snapshots` keywords with a stopword side; labels starting with a stopword |
| 5 | Exemplars: tokenised synonym anchors, Description in validation, second round, 2 h cutoff, recency factor, live-count re-score, persist the pick | medium | bare non-meme lines (19%, rank 1 22%); repeat slots (17%) | feed facets per label line; `topic_snapshots.exemplar_uri` once written |
| 6 | Weekly aggregation by subject component; `rank <= 3` in the rollup | medium | weekly line vs rank-1 majority (wrong 3 of 5 weeks) | replay `GetTopTopicForRange` against `sentiment_history` rank-1 family counts each Monday |
| 7 | Identity: keywords-only Jaccard, `>=` with size-aware threshold, merge on double match, modal label displayed | medium | live identities per subject (47 election, 8 Wordle; target 1-2) | `topic_identity` grouped by label content words |
| 8 | Ranking: 2-hit or df-weighted author counting, `__discard__` allowed, ~100-author publication floor, per-stage counts and generic-drop logging | medium | generic unigrams in rank-1 keyword sets (34/48); thin hours (27/185) | `topic_snapshots` rank-1 keywords vs a df table; rows per snapshot |
| 9 | Baseline-contrast scoring over the prior 24 h of `topic_tokens` | medium | ritual share (Wordle 35 labels/week); perennial share (Trump 70/179 hours) | label family counts per week from the feed |
| 10 | Per-feed headline cap and Warn on total failure; Unicode fold; `trending_post_failed` event | small | observability | log lines |

## 6. What to measure going forward

1. Wrong-year rate: `sentiment_history.top_topic` and `topic_snapshots.label` matching `(19|20)\d\d` below the current year, per week.
2. Relabel rate: share of consecutive `topic_snapshots` rows with the same `topic_id` whose label differs; distinct labels per identity over 7 days.
3. Subject concentration: live `topic_identity` rows per label-content-word family (election, Wordle, Halloween) at each daily rollup.
4. Exemplar coverage: once `UpdateSnapshotExemplar` is called, share of rank 1-3 non-meme snapshot rows with a non-empty `exemplar_uri`, plus `exemplar_dropped` by reason.
5. Corpus freshness and shape: min/max `created_at` of loaded tokens, rows per snapshot, clusters removed per stage, from cycle log lines.

## 7. Appendix

Reader's-eye verdict (185 replies, Nov 1-8): 74% of rank-1 slots were news events, 10% persons, 9% calendar observances, 8% daily rituals, 7% memes, 6% sport; the midterms took 94 of 185 top slots, which was the week. Big stories landed promptly and correctly; 42 of 48 fetched exemplars were on topic. The defects a follower meets daily are the year, the hourly renaming, fragment labels, bare top slots, and for two days a footer attributing the week's unhappiest hour to "Cinema and Film Watching".

Data sources: `/tmp/hs-topics/data/` (trending_labels.csv, topic_snapshots.csv, topic_identity.csv, topic_daily_since_oct.csv, sentiment_history_top_topic.csv, feed.json, backup.db); public `app.bsky.feed.getPosts` for exemplar text and age; `/tmp/hs-topics/gofetch` replica of `headlines.go`; a 45 s Jetstream capture (728 English root posts) for tokenizer replicas. Scripts: `/tmp/hs-topics/scripts/*.py`, `/tmp/hs-topics/work/{categorise.py,toptopic.sql,sh.sql}`, `/tmp/hs-topics/verify/weekly_replay.py`, `/tmp/hs-verify/*.py`.

Refuted or artefactual claims, not to be re-raised:
- G7: the 11:55 cycles on Nov 5 and 6 and the 12:55 cycle on Nov 8 did post trending replies (indexed at :00 of the next hour); the week's one real miss is 2026-11-06T06:55, which failed in the instrumented grouping path (`top_topic` NULL).
- The brief's five "6-topic hours" and three "no trending reply" hours are indexedAt bucketing of two cycles in one clock hour; bucket by run or thread root.
- F3 as a ranking inversion: real in mechanism, but no close-margin hour showed a padded cluster beating a specific one; it stands only through the mislabelled-cluster cases in R6.
- EX-2's "older posts accumulate engagement": stored engagement is frozen at hydration.
- TE-1's "114 days" and "49 days" were row counts (32 days each); Wordle was on 63 of 65 days, not every day.
- T6's "a third of slots repeat": 17% once first appearances are excluded.
- "Sir Patrick Stewart" as the 7-day low (Oct 29 21:06) was a correct label.
