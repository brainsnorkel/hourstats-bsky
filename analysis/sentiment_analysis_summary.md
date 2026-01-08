# Bluesky Sentiment Analysis Summary

Generated: 2026-01-09 07:34:06 AEDT

## Overview

This document analyzes the historical sentiment data from Bluesky to understand the typical range of sentiment and inform better sentiment word calibration.

## Data Coverage

- **Daily Data Points**: 116 days
- **Date Range**: 2025-09-14 to 2026-01-07
- **Hourly Data Points**: 598 records
- **Hourly Date Range**: 2025-12-26 18:31 to 2026-01-08 17:01

## Daily Sentiment Statistics

### Average Sentiment (Daily Means)

| Metric | Value | Date |
|--------|-------|------|
| Overall Average | 10.82% | - |
| Lowest Daily Avg | 6.14% | 2026-01-03 |
| Highest Daily Avg | 19.77% | 2025-12-25 |
| Range (Avg) | 13.63% | - |

### Extreme Sentiment Values (Min/Max per day)

| Metric | Value | Date |
|--------|-------|------|
| Absolute Minimum | -4.53% | 2025-12-22 |
| Absolute Maximum | 26.64% | 2026-01-01 |
| Total Range | 31.17% | - |

## Hourly Sentiment Statistics

| Metric | Value | Time |
|--------|-------|------|
| Overall Average | 11.74% | - |
| Lowest Hourly | 1.02% | 2026-01-08 00:02 |
| Highest Hourly | 26.64% | 2026-01-01 05:02 |
| Range | 25.62% | - |

## Sentiment Distribution (Daily Averages)

| Category | Count | Percentage |
|----------|-------|------------|
| Very Negative (<-50%) | 0 | 0.0% |
| Negative (-50% to -20%) | 0 | 0.0% |
| Slightly Negative (-20% to -5%) | 0 | 0.0% |
| Neutral (-5% to 5%) | 0 | 0.0% |
| Slightly Positive (5% to 20%) | 116 | 100.0% |
| Positive (20% to 50%) | 0 | 0.0% |
| Very Positive (>50%) | 0 | 0.0% |

## Key Insights

### Observed Sentiment Range

Based on the historical data, Bluesky sentiment typically operates in a **narrow positive band**:

- **Typical Range**: 6.1% to 19.8% (daily averages)
- **Extreme Range**: -4.5% to 26.6% (intraday extremes)
- **Central Tendency**: Around 10.8% (slightly positive)

### Implications for Sentiment Word Calibration

The current 100-word sentiment scale spans -100% to +100%, but Bluesky sentiment rarely ventures outside the -10% to 32% range. This means:

1. **Most negative words are never used** - Words for sentiment below -10% are essentially unused
2. **Most positive words are never used** - Words for sentiment above 32% are essentially unused
3. **Word variety is limited** - Only a small subset of the 100 words are ever selected

### Recommendations

1. **Recalibrate the word scale** to focus on the actual observed range (approximately -15% to 37%)
2. **Add more nuanced words** in the slightly positive range (5%-15%) where most sentiment falls
3. **Use relative positioning** - Map words to percentiles of observed data rather than absolute percentages
4. **Consider seasonal/event variation** - Some days show wider swings than others

## Raw Data Files

- `daily_sentiment.csv` - Daily aggregated sentiment data
- `daily_sentiment.json` - Daily data in JSON format
- `hourly_sentiment.csv` - Hourly sentiment data (14-day rolling window)
- `hourly_sentiment.json` - Hourly data in JSON format
