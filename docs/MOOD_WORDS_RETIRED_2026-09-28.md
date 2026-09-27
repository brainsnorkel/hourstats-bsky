# Mood words retired 2026-09-28

From January to 2026-09-28 the hourly summary's hashtag ("Bluesky is #___")
was one of 100 words spread over the seven percentile tiers in
`internal/formatter/sentiment_100_words.go`. On 2026-09-28 (bead hs-ghv) the
list was cut to 22 words; the tiers, thresholds and interpolation did not
change. It was retired for two reasons. Many of the words did not describe a
mood at all (curious, witty, ironic, creative, engaged, alert), so the hashtag
often read as a non sequitur. And the words were far narrower than the noise:
in the middle tiers each word covered 0.06–0.08 points, while the realigned
hourly series moves by a median of 0.30 points from one hour to the next (p90
0.78), so the word changed in nearly every hour on noise alone. The 22-word
list gives tiers 3–5 spans of about 0.42–0.44 points, which change the word in
roughly half of hours. See the 2026-09-28 update in
[SENTIMENT_CALIBRATION_REVIEW_2026-09.md](SENTIMENT_CALIBRATION_REVIEW_2026-09.md).

The retired list, by tier, in index order (rising sentiment within each tier):

| Tier | Range (realigned) | Words |
|------|-------------------|-------|
| 1 Extreme Negative (5) | < 0% | hostile, angry, dreadful, grim, miserable |
| 2 Unusually Low (15) | 0% to < 10.25% | despondent, glum, sullen, somber, melancholy, pessimistic, cynical, anxious, agitated, irritable, tense, uneasy, restless, weary, subdued |
| 3 Below Average (15) | 10.25% to < 11.5% | flat, downbeat, tired, sluggish, solemn, wary, skeptical, cautious, uncertain, ambivalent, distracted, reserved, pensive, quiet, reflective |
| 4 Typical (30) | 11.5% to < 13.25% | calm, chill, mellow, relaxed, content, peaceful, grounded, steady, curious, inquisitive, thoughtful, introspective, speculative, sentimental, nostalgic, playful, mischievous, cheeky, ironic, witty, candid, sincere, earnest, easygoing, sociable, engaged, connected, alert, balanced, settled |
| 5 Above Average (15) | 13.25% to < 14.5% | happy, cheerful, upbeat, positive, optimistic, hopeful, encouraged, pleased, amused, friendly, warm, welcoming, lively, supportive, bright |
| 6 Unusually High (15) | 14.5% to < 16.75% | excited, vibrant, energetic, enthusiastic, inspired, creative, joyful, delighted, thrilled, invigorated, passionate, spirited, exuberant, buoyant, buzzing |
| 7 Extreme Positive (5) | >= 16.75% | celebratory, jubilant, elated, ecstatic, euphoric |
