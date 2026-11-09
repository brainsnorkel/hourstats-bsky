# Exemplar validator failures — analysis, 2026-11-09

Bead: hs-aqa.11 (child of epic hs-aqa). Related: hs-aqa.8 (exemplar coverage), hs-aqa.10 (thinkingBudget question).

## Verdict

The exemplar validator has been timing out on about one hour in twenty since the
grouping model switched to `gemini-3.8-flash` on 2026-09-28, with a slower tail
on another one in fourteen. The cause is a missing thinking setting: the
validation request sends a JSON schema but no `thinkingConfig`, and Gemini 3.8
Flash thinks at the `medium` level by default. Replaying a real batch, the model
spent 5,000–7,000 thought tokens and 45–61 s on a 717-token prompt; with
`thinkingLevel: "low"` the same batch took 3.5–4.1 s and returned the same
verdicts. The grouping call to the same model is unaffected because it sets
`thinkingBudget: 1024`, which 3.8 Flash still honours.

A timed-out batch fails open: every topic publishes its top-ranked candidate
unvalidated. The failure writes one Warn line and no stats event, which is why
six weeks passed without notice.

## 1. What the validator does

One Gemini call per cycle, in the posting path after the sparkline
(`internal/topics/analyzer.go:348` → `ExemplarHydrator.HydrateExemplars` →
`validatePicks` at `exemplar.go:381` → `Grouper.ValidateExemplars` at
`grouper.go:1151`).

- Input: up to 9 pairs (`TopTopics × exemplarTopK`, round-robin by rank so
  every topic's first candidate is judged before any second), each pair a
  topic label and the post text cut to 300 runes. Candidates have already
  passed the feature gate, so no gated-out user's text reaches Gemini.
- Prompt (`buildValidationPrompt`, `grouper.go:1245`): an untrusted-data
  preface, "is the post genuinely about the topic", each post in a `<post>`
  block with delimiters stripped from the text. ~2,750 runes, 717 tokens for
  the 04:55 batch.
- Request: `responseMimeType: application/json`, `responseSchema` (array of
  `{id, topic_label, is_relevant}`), **no `thinkingConfig`**. Same endpoint,
  key and `http.Client` (45 s timeout, `groupingCallTimeout`) as grouping. No
  fallback model, no retry, no attempt log line.
- Output: `mapVerdicts` trusts ids only when they are exactly 1..n once each,
  accepts a 0-based echo in order, otherwise reports the batch unvalidated.
- Semantics: a **successful** call can leave a topic with no exemplar (all
  candidates rejected → bare line, `exemplar_dropped` event). A **failed**
  call keeps every topic's top-ranked pick (`keepUnvalidated`). Validation
  therefore only ever removes links; its failure only ever adds them.

## 2. Failure modes in the code

| Cause | Where | Handling | Visible as |
|---|---|---|---|
| HTTP client timeout (45 s awaiting headers) | `grouper.go:1182` | `ErrValidationUnavailable` → `keepUnvalidated` | Info per topic + one Warn `exemplar: validator unavailable`; no event |
| Non-200 (429, 5xx, 402 credit) | `grouper.go:1188` | same | same |
| Empty / no text / JSON parse | `grouper.go:1205-1218` | same | same |
| Verdict count ≠ pairs, unusable ids | `mapVerdicts` | same | same (+ Warn on 0-based ids) |
| Daily budget (150 calls, shared with grouping) | `grouper.go:1156` | same; a timed-out call still spends one | Warn `gemini: daily call budget exhausted` once |
| Success, all candidates rejected | `exemplar.go:475` | topic published without link | Warn + `exemplar_dropped` stats event |
| Pair budget left a rank unsent | `exemplar.go:445,470` | unvalidated pick | Info |

No validator outcome reaches `internal/alerts`; only `exemplar_dropped` is an
event, and it is not alerted either.

## 3. Measured history from the public feed

Method: the trending reply is posted after exemplar selection, the gate and
validation, so the gap between the sparkline's and the trending reply's
`createdAt` is a proxy for validation time plus a floor of roughly 8–11 s
(candidate query, gate, post). 1,381 trending replies from 2026-09-09 to
2026-11-09 05:01 UTC, pulled with `app.bsky.feed.getAuthorFeed`.

| Week (Mon) | cycles | gap p50 | gap p90 | ≥40 s | bare lines |
|---|---|---|---|---|---|
| 09-07 | 105 | 18 s | 28 s | 0 | 10.5% |
| 09-14 | 149 | 13 s | 25 s | 0 | 14.4% |
| 09-21 | 136 | 12 s | 18 s | 0 | 18.0% |
| **09-28** (3.8-flash) | 168 | 14 s | 29 s | 7 | 19.7% |
| 10-05 | 167 | 18 s | 36 s | 13 | 17.6% |
| 10-12 | 149 | 19 s | 46 s | 21 | 15.3% |
| 10-19 | 168 | 18 s | 34 s | 11 | 15.6% |
| 10-26 | 165 | 19 s | 50 s | 28 | 20.5% |
| 11-02 | 164 | 20 s | 41 s | 18 | 27.4% |
| 11-09 (6 cycles) | 6 | 55 s | 61 s | 3 | 11.1% |

Gap histogram since 09-28: a main mode at 10–24 s, a tail to 49 s (36 cycles)
and a second cluster at 50–64 s (65 cycles, ~4.5%), which is the 45 s timeout
plus the floor. Before 09-28 (2.5-pro, 30 s timeout then) the maximum gap was
39 s and nothing sat at the 40–46 s timeout signature: the validator did not
time out on 2.5-pro.

By UTC hour since 09-28, the ≥40 s share is 18–22% at 01, 03 and 05 UTC and
17% at 16 UTC, against 2–7% at 07–11 UTC: US evening and morning peaks, when
per-token latency is presumably worst.

Today (deploy v55 at 02:12 UTC): 01:03 58 s (pre-deploy), 02:02 16 s,
03:03 61 s, 04:02 35 s, 05:01 55 s. The 02:55 and 04:55 timeouts are
confirmed in the process log. Three slow cycles in five post-deploy is within
the hour-of-day pattern's spread but worth watching; nothing in v55 touched
the request.

Comparison, the grouping call on the same model: hours with a sparkline and no
trending reply (the public signature of a failed grouping) number 2 in six
weeks outside the 2026-10-15 credit outage.

Caveats: the gap conflates the candidate query with validation, so the
2026-11-04 election-night slowdown (p50 35 s) may be partly database time; the
bare-line rate measures rejections, not failures; the prod `/stats/events`
read over ssh was denied in this session, so the grouping comparison rests on
the feed.

## 4. Reproduction

`.omc/research/validator_probe_test.go.txt` (build tag `probe`, env
`HS_PROBE_GEMINI_KEY`, `HS_PROBE_CANDIDATES`) rebuilds the 04:55 UTC cycle's
exact nine-pair prompt with `buildValidationPrompt` from the nine gated
candidates (`.omc/research/validator_probe_candidates_20261109_0455.json`) and
posts it to the live endpoint with a 100 s client timeout.

| Model | thinkingConfig | elapsed (3 runs) | thought tokens | verdicts |
|---|---|---|---|---|
| gemini-3.8-flash | none (production) | 61.4 s, 45.1 s, 54.8 s | 7,095 / 5,257 / 6,079 | T T **F F** T T T T T |
| gemini-3.8-flash | `thinkingLevel: low` | 3.5 s, 4.1 s, 4.1 s | 0 | identical |
| gemini-3.8-flash | `thinkingBudget: 1024` (grouper's) | 4.8 s, 3.4 s, 4.1 s | 0 | identical |
| gemini-3.8-flash | `thinkingLevel: minimal` | 400 "not supported for this model" | — | — |
| gemini-3.5-flash-lite | none (default minimal) | 1.9 s, 1.4 s, 2.3 s | 0 | all T |
| gemini-3.5-flash-lite | `thinkingLevel: low` | 1.9 s, 2.1 s, 2.0 s | 0 | all T |

Input 717 tokens, output 327 tokens in every run. The production shape never
finished inside 45 s on this batch. The three 3.8-flash settings that answered
agreed on all nine verdicts: medium thinking changed nothing here. The lite
model approved every pair, including the two 3.8-flash rejects on every run,
so it is not a drop-in validator fallback.

Google's documentation (`ai.google.dev/gemini-api/docs/thinking`,
`whats-new-gemini-3.8`): 3.8 Flash default thinking "On (medium)", levels
low/medium/high, `minimal` unsupported; migration guidance is to replace
`thinking_budget` with `thinking_level`. The probe shows the budget knob is
still honoured today.

Cost of the status quo: ~6,000 thought tokens per call at $3.75/M output is
about 2 cents per timed-out hour, on prepaid credit; the successful slow
calls cost the same for nothing.

## 5. Root cause

The validation request was written in February 2026 (commit 27928cb) for
Gemini 2.5, without a thinking setting, six days after the grouping request
gained `thinkingBudget: 1024` (f67ddb7). On 2.5-pro the default thinking was
fast enough for a 30 s timeout. The 2026-09-28 switch to 3.8-flash (1e87f45)
raised the client timeout to 45 s for grouping and left the validator on the
model's new medium-thinking default, where a nine-pair relevance judgement runs
45–61 s at peak hours. Nothing records the outcome, so the regression surfaced
only when the first v55 cycle was read line by line.

## 6. Impact

1. **Latency**: the trending reply is held 45 s on timed-out hours, inside the
   cycle's budget (the 04:55 cycle completed in 486 s). Harmless.
2. **Unvalidated exemplars**: on those hours the top-ranked pick is published
   unchecked. In the 04:55 cycle that was a post the validator rejects on
   every run (pair 3, "Donald Trump Administration": a Trump quote about
   forgiveness); a completed validation would have promoted rank 2. The
   2026-11-09 quality review found ranker picks on topic 45 of 50 times, so
   the quality cost is real but modest.
3. **Invisibility**: no event, no alert, no attempt line; the failure is only
   in the process log, whose retention on Fly is minutes.
4. **Budget**: each timed-out call still counts against the 150-call day;
   at ~3 calls an hour this is far from binding.
5. **Not a cause of bare lines**: bare lines (15–27% of topic lines) come
   from successful validations that rejected every candidate (R10); a
   timeout makes a bare line less likely, not more.

## 7. Recommendations (bead hs-aqa.11)

1. Set `thinkingLevel: "low"` on the validation request. Expected: 3–5 s per
   call, no change in verdicts (verify on three batches against the default).
   Consider moving the grouper from `thinkingBudget` to `thinkingLevel` as
   well, since the budget knob is documented as unsupported on Gemini 3+.
2. Log one `validator attempt` line per call with the grouper's fields
   (`model`, `status`, `elapsed_ms`, `thoughts_tokens`, `output_tokens`,
   `class`).
3. Write an `exemplar_validation_failed` stats event carrying the class and
   elapsed time, and let `internal/alerts` raise it as a non-actionable warn
   when it recurs within a snapshot window.
4. Do not fall back to `gemini-3.5-flash-lite` for validation: it approved
   all nine pairs including both 3.8-flash rejects.
5. Keep the fail-open semantics unless hs-aqa.8 changes them deliberately;
   with the timeout fixed, they will rarely matter.

Acceptance (on the bead): over 48 post-deploy cycles, ≥95% `class=ok`,
p95 elapsed under 15 s, no sparkline-to-trending gap ≥50 s in the public feed,
an event for every failure.

## 8. Not verified

- The typical batch's thought-token count (only a batch that timed out was
  replayed; the feed says most batches finish in 5–10 s).
- The post-v55 failure rate (five cycles).
- Prod's `grouping_failed` event list for the same period (ssh read denied;
  the feed proxy was used instead).

## Data

`/tmp/hs-validator/feed.jsonl` (4,400 posts), `cycles.json`, `probe.log`,
`probe_lite.log`; copies of the probe and its input under `.omc/research/`.
