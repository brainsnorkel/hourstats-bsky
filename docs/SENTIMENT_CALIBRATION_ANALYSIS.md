# Bluesky Sentiment Word Calibration Analysis

**Document Version:** 1.0
**Analysis Date:** January 9, 2026
**Data Coverage:** September 14, 2025 - January 7, 2026 (116 days)
**Branch:** `recalibrate-sentiment-words`

## Executive Summary

This document presents a comprehensive analysis of historical Bluesky sentiment data to inform a recalibration of the sentiment word system. The key finding is that **Bluesky sentiment operates in a narrow positive band** (typically 6% to 20%), meaning the current 100-word scale mapped to -100% to +100% wastes approximately 80% of its vocabulary on ranges that never occur.

### Key Recommendations

1. **Adopt percentile-based mapping** instead of absolute percentage mapping
2. **Concentrate word variety** in the 9-14% range where most sentiment falls
3. **Use collective atmosphere words** rather than intense personal emotions
4. **Define 7 sentiment tiers** with data-driven boundaries

---

## 1. Data Analysis

### 1.1 Data Sources

| Source | Records | Date Range | Notes |
|--------|---------|------------|-------|
| Daily Sentiment | 116 days | Sep 14, 2025 - Jan 7, 2026 | 3-year retention |
| Hourly Sentiment | 598 records | Dec 26, 2025 - Jan 8, 2026 | 14-day rolling window |

### 1.2 Platform Growth Event

A significant platform growth event occurred in early November 2025:

| Period | Avg Daily Posts | Avg Sentiment |
|--------|-----------------|---------------|
| Pre-November (Sep 14 - Nov 1) | ~35,000 | 9.78% |
| Post-November (Nov 2 - Jan 7) | ~398,000 | 11.25% |

This represents a **>10x increase in daily post volume** without significant change to sentiment distribution, suggesting the sentiment patterns are stable and representative.

### 1.3 Observed Sentiment Distribution

#### Daily Average Sentiment Statistics

| Metric | Value | Date |
|--------|-------|------|
| Overall Mean | 10.82% | - |
| Standard Deviation | ~2.5% | - |
| Minimum Daily Avg | 6.14% | Jan 3, 2026 |
| Maximum Daily Avg | 19.77% | Dec 25, 2025 (Christmas) |
| Range | 13.63% | - |

#### Intraday Extreme Values

| Metric | Value | Date |
|--------|-------|------|
| Absolute Minimum | -4.53% | Dec 22, 2025 |
| Absolute Maximum | 26.64% | Jan 1, 2026 (New Year's) |
| Total Range | 31.17% | - |

#### Distribution by Category (Current System)

| Category | Count | Percentage |
|----------|-------|------------|
| Very Negative (<-50%) | 0 | 0.0% |
| Negative (-50% to -20%) | 0 | 0.0% |
| Slightly Negative (-20% to -5%) | 0 | 0.0% |
| Neutral (-5% to 5%) | 0 | 0.0% |
| **Slightly Positive (5% to 20%)** | **116** | **100.0%** |
| Positive (20% to 50%) | 0 | 0.0% |
| Very Positive (>50%) | 0 | 0.0% |

**Critical Finding:** 100% of daily average sentiment values fall within a single category of the current system, demonstrating severe underutilization of the vocabulary.

### 1.4 Notable Events

| Date | Sentiment | Event |
|------|-----------|-------|
| Dec 25, 2025 | 19.77% (daily avg) | Christmas Day - Highest daily average |
| Jan 1, 2026 | 26.64% (peak) | New Year's Day - Highest intraday peak |
| Jan 1, 2026 | 18.88% (daily avg) | New Year's Day - Second highest daily average |
| Dec 22, 2025 | -4.53% (min) | Only day with negative intraday sentiment |
| Jan 3, 2026 | 6.14% (daily avg) | Lowest daily average |
| Sep 18, 2025 | 6.80% (daily avg) | Early low point |

---

## 2. Problem Statement

### 2.1 Current System Limitations

The existing `sentiment_100_words.go` implementation maps 100 words across a -100% to +100% range:

```
-100% to -90%: hopeless, devastated, shattered, destroyed, ruined
-90% to -80%:  distressed, anguished, tormented, crushed, broken
...
+80% to +90%:  transcendent, blissful, divine, heavenly, sublime
+90% to +100%: euphoric, magnificent, glorious, radiant, exalted
```

**Problems:**

1. **~80% of words are never used** - Sentiment never reaches ranges like -50% or +50%
2. **Limited expressiveness** - Only ~10-15 words from the middle range ever appear
3. **Poor differentiation** - Days with 8% vs 12% sentiment may use the same word
4. **Inappropriate vocabulary** - Words like "hopeless" or "euphoric" don't fit collective mood descriptions
5. **Fixed absolute mapping** - Doesn't account for Bluesky's specific sentiment baseline

### 2.2 Current Mapping Function

The current `normalCurveMapping` function attempts to concentrate words in the middle but still operates on the full -100% to +100% scale:

```go
// Current: Maps 0-1 normalized sentiment to 0-99 word index
// Problem: Most of the scale (below ~45 and above ~65) is never accessed
```

---

## 3. Proposed Solution: Percentile-Based Tiered Mapping

### 3.1 Design Principles

1. **Data-driven boundaries** - Tiers based on actual observed percentiles
2. **Collective vocabulary** - Words describe network atmosphere, not personal emotions
3. **Concentrated variety** - More words in common ranges, fewer in extremes
4. **Natural language fit** - Words work in "Bluesky is feeling ___" context

### 3.2 Proposed Tier Structure

| Tier | Sentiment Range | Word Count | Description |
|------|-----------------|------------|-------------|
| **Extreme Negative** | < 0.0% | 5 | Rare intraday negative events |
| **Unusually Low** | 0.0% to < 9.5% | 15 | Noticeably subdued or disengaged |
| **Below Average** | 9.5% to < 10.5% | 15 | Quiet, calm, unremarkable day |
| **Typical** | 10.5% to < 12.5% | 30 | Normal everyday mood (baseline) |
| **Above Average** | 12.5% to < 14.0% | 15 | Distinctly energetic and positive |
| **Unusually High** | 14.0% to < 18.0% | 15 | Significant positive energy |
| **Extreme Positive** | >= 18.0% | 5 | Major events (holidays, milestones) |

### 3.3 Threshold Constants

Based on the post-November data analysis:

```go
const (
    ThresholdExtremeNegative = 0.0    // Below this: Extreme Negative
    ThresholdUnusuallyLow    = 9.5    // Below this: Unusually Low
    ThresholdBelowAverage    = 10.5   // Below this: Below Average
    ThresholdTypical         = 12.5   // Below this: Typical
    ThresholdAboveAverage    = 14.0   // Below this: Above Average
    ThresholdUnusuallyHigh   = 18.0   // Below this: Unusually High
    // >= 18.0: Extreme Positive
)
```

---

## 4. Proposed 100-Word Lexicon

### 4.1 Word Selection Criteria

- Describes collective mood/atmosphere, not individual emotions
- Natural in context: "Bluesky is feeling ___" or "The mood is ___"
- Avoids overly dramatic language for middle ranges
- Provides variety within each tier (not just synonyms)
- Ordered from lower to higher sentiment within each tier

### 4.2 Complete Word List by Tier

#### Tier 1: Extreme Negative (5 words) - Sentiment < 0.0%
```
1.  strained
2.  tense
3.  sour
4.  discordant
5.  antagonistic
```

#### Tier 2: Unusually Low (15 words) - Sentiment 0.0% to < 9.5%
```
6.  stagnant
7.  lethargic
8.  subdued
9.  muted
10. drab
11. listless
12. apathetic
13. indifferent
14. reserved
15. guarded
16. wary
17. hesitant
18. cautious
19. reflective
20. pensive
```

#### Tier 3: Below Average (15 words) - Sentiment 9.5% to < 10.5%
```
21. quiet
22. still
23. unremarkable
24. neutral
25. unassuming
26. composed
27. measured
28. steady
29. placid
30. mild
31. observant
32. contemplative
33. calm
34. tranquil
35. serene
```

#### Tier 4: Typical (30 words) - Sentiment 10.5% to < 12.5%
```
36. average
37. normal
38. standard
39. regular
40. typical
41. consistent
42. everyday
43. familiar
44. balanced
45. harmonious
46. agreeable
47. constructive
48. pleasant
49. sociable
50. amicable
51. engaged
52. interactive
53. responsive
54. conversational
55. communal
56. positive
57. good-natured
58. genial
59. upbeat
60. cheerful
61. welcoming
62. supportive
63. encouraging
64. heartening
65. optimistic
```

#### Tier 5: Above Average (15 words) - Sentiment 12.5% to < 14.0%
```
66. hopeful
67. bright
68. sunny
69. warm
70. lively
71. animated
72. buoyant
73. inspired
74. spirited
75. vibrant
76. dynamic
77. energetic
78. enthusiastic
79. joyful
80. delighted
```

#### Tier 6: Unusually High (15 words) - Sentiment 14.0% to < 18.0%
```
81. excited
82. thrilled
83. gleeful
84. vivacious
85. elated
86. effervescent
87. ebullient
88. exuberant
89. radiant
90. glowing
91. proud
92. rapturous
93. exhilarated
94. triumphant
95. overjoyed
```

#### Tier 7: Extreme Positive (5 words) - Sentiment >= 18.0%
```
96. festive
97. jubilant
98. celebratory
99. ecstatic
100. euphoric
```

---

## 5. Implementation Approach

### 5.1 Algorithm Overview

```go
func getCalibratedMoodWord(sentiment float64) string {
    // Determine tier
    tier := determineTier(sentiment)

    // Get word range for tier
    startIdx, endIdx := getTierWordRange(tier)

    // Linear interpolation within tier to select specific word
    tierMin, tierMax := getTierBounds(tier)
    position := (sentiment - tierMin) / (tierMax - tierMin)
    wordIdx := startIdx + int(position * float64(endIdx - startIdx))

    // Clamp to valid range
    if wordIdx < startIdx { wordIdx = startIdx }
    if wordIdx > endIdx { wordIdx = endIdx }

    return calibratedWords[wordIdx]
}
```

### 5.2 Key Implementation Details

1. **Tier determination** uses simple threshold comparisons
2. **Word selection within tier** uses linear interpolation for granularity
3. **Edge case handling** for values outside expected range
4. **Constants for thresholds** enable easy tuning

---

## 6. Expected Outcomes

### 6.1 Improved Word Variety

| Scenario | Current System | Proposed System |
|----------|---------------|-----------------|
| Sentiment: 8% | "calm" (always) | Could be "cautious", "reflective", "pensive" |
| Sentiment: 11% | "calm" (always) | Could be "pleasant", "engaged", "upbeat" |
| Sentiment: 14% | "pleased" (rarely) | Could be "excited", "thrilled", "vivacious" |
| Sentiment: 20% | "cheerful" (never used) | "jubilant", "celebratory" |

### 6.2 Better Event Detection

- **Holiday detection**: Christmas/New Year will show "festive", "celebratory"
- **Low mood detection**: Subdued days will show "muted", "reserved"
- **Normal days**: Will cycle through 30 "typical" words for variety

### 6.3 User Experience

- More engaging, varied posts
- Better reflection of actual mood shifts
- Natural-sounding collective descriptions

---

## 7. Data Files Reference

Generated analysis files in `/analysis/`:

| File | Description |
|------|-------------|
| `daily_sentiment.csv` | All 116 days of daily aggregated data |
| `daily_sentiment.json` | Same data in JSON format |
| `hourly_sentiment.csv` | 598 hourly data points (14-day window) |
| `hourly_sentiment.json` | Same data in JSON format |
| `sentiment_analysis_summary.md` | Auto-generated statistical summary |

---

## 8. Next Steps

1. **Implement new word mapping function** in `internal/formatter/sentiment_100_words.go`
2. **Add threshold constants** as configurable values
3. **Update tests** to verify tier boundaries
4. **Deploy and monitor** word variety in production
5. **Iterate** based on user feedback and additional data

---

## Appendix A: Raw Daily Sentiment Data Sample

```csv
date,average_sentiment,min_sentiment,max_sentiment,total_runs,total_posts
2025-12-24,14.8564,11.5019,17.7459,48,430164
2025-12-25,19.7653,15.3660,24.3271,34,288272  # Christmas - Highest avg
2025-12-26,14.3234,8.4049,22.4111,11,7920
2025-12-31,15.5485,11.0332,20.8106,48,371609
2026-01-01,18.8783,11.3759,26.6413,48,398201  # New Year - Peak intraday
2026-01-03,6.1387,1.3608,14.5458,39,321224    # Lowest daily avg
```

---

## Appendix B: Current vs Proposed Word Mapping

### Current System Word Indices Used

For sentiment range 6% to 20% (observed daily averages):
- Current system maps this to word indices ~53 to ~60 out of 100
- Only about 7-8 words ever appear in practice

### Proposed System Word Distribution

For the same sentiment range:
- Proposed system uses words 6-95 (tiers 2-6)
- 90 words available for observed range
- 30 words concentrated in the most common tier (10.5%-12.5%)

---

*Document generated as part of the sentiment word recalibration initiative.*
