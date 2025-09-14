# Yearly Sentiment Feature Design Document

## Overview

This document outlines the design and implementation of a new yearly sentiment feature for the HourStats Bluesky bot. The feature will create daily sentiment averages and generate monthly yearly sentiment charts with enhanced visualizations.

## Requirements Summary

### Daily Processing
- **Trigger**: Each day after midnight UTC
- **Action**: Calculate average sentiment from all 30-minute runs during the previous 24 hours
- **Storage**: New DynamoDB table for daily sentiment averages
- **TTL**: 3 years for daily average entries
- **Data**: Store average, minimum, and maximum sentiment for each day

### Monthly Processing
- **Trigger**: 1st day of each month at 1:00 AM UTC
- **Action**: Generate yearly sentiment chart with 365 days of data
- **Visualization**: 25% larger canvas than current sparkline (1500x1000 vs 1200x800)
- **Content**: "Yearly Sentiment (UTC)" title and text
- **Axis**: Month ticks on horizontal axis
- **Posting**: Pin the yearly post to the account

## Architecture Design

### 1. DynamoDB Schema Design

#### New Table: `hourstats-daily-sentiment`

```yaml
TableName: hourstats-daily-sentiment
BillingMode: PAY_PER_REQUEST
PrimaryKey:
  - HashKey: date (S) - Format: "2025-01-05" (YYYY-MM-DD)
  - RangeKey: runId (S) - For potential multiple runs per day

Attributes:
  - date: String (YYYY-MM-DD format)
  - runId: String (daily run identifier)
  - averageSentiment: Number (average sentiment percentage)
  - minSentiment: Number (minimum sentiment percentage)
  - maxSentiment: Number (maximum sentiment percentage)
  - totalRuns: Number (number of 30-minute runs included)
  - totalPosts: Number (total posts analyzed)
  - createdAt: String (ISO timestamp)
  - ttl: Number (3 years from creation)

Global Secondary Indexes:
  - date-index:
    - HashKey: date
    - RangeKey: createdAt
    - ProjectionType: ALL
  - sentiment-range-index:
    - HashKey: "SENTIMENT_RANGE" (constant)
    - RangeKey: date
    - ProjectionType: INCLUDE
    - NonKeyAttributes: [averageSentiment, minSentiment, maxSentiment]
```

#### Updated Table: `hourstats-sentiment-history`
- Add new item type for daily averages
- Extend existing schema to support both 30-minute and daily data points

### 2. Lambda Functions Architecture

#### New Lambda: `hourstats-daily-aggregator`
```yaml
FunctionName: hourstats-daily-aggregator
Runtime: provided.al2023
Memory: 256MB
Timeout: 5 minutes
Schedule: cron(0 0 * * ? *) # Daily at midnight UTC
Environment:
  - DAILY_SENTIMENT_TABLE: hourstats-daily-sentiment
  - SENTIMENT_HISTORY_TABLE: hourstats-sentiment-history
  - STATE_TABLE: hourstats-state
```

**Responsibilities:**
- Query all sentiment data from previous 24 hours
- Calculate daily averages, min, max
- Store daily sentiment record
- Handle edge cases (missing data, partial days)

#### New Lambda: `hourstats-yearly-poster`
```yaml
FunctionName: hourstats-yearly-poster
Runtime: provided.al2023
Memory: 512MB
Timeout: 10 minutes
Schedule: cron(0 1 1 * ? *) # 1st day of month at 1:00 AM UTC
Environment:
  - DAILY_SENTIMENT_TABLE: hourstats-daily-sentiment
  - SENTIMENT_HISTORY_TABLE: hourstats-sentiment-history
  - BLUESKY_HANDLE: /hourstats/bluesky/handle
  - BLUESKY_PASSWORD: /hourstats/bluesky/password
```

**Responsibilities:**
- Retrieve 365 days of daily sentiment data
- Generate yearly sparkline chart (1500x1000 canvas)
- Post to Bluesky with "Yearly Sentiment (UTC)" title
- Pin the post to the account
- Handle insufficient data scenarios

### 3. Sparkline Generator Enhancements

#### New Configuration: `YearlySparklineConfig`
```go
type YearlySparklineConfig struct {
    Width        int     // 1500 (25% larger than 1200)
    Height       int     // 1000 (25% larger than 800)
    Padding      int     // 100 (scaled proportionally)
    LineWidth    float64 // 4.0 (scaled proportionally)
    PointRadius  float64 // 1.0 (scaled proportionally)
    // ... other configs scaled proportionally
}
```

#### New Methods:
- `GenerateYearlySentimentSparkline(dataPoints []DailySentimentDataPoint) ([]byte, error)`
- `drawMonthMarkers()` - Draw month ticks on horizontal axis
- `drawYearlyLabels()` - Custom labels for yearly view

### 4. Data Flow Design

#### Daily Aggregation Flow
```mermaid
graph TD
    A[EventBridge Daily Trigger<br/>cron(0 0 * * ? *)] --> B[Daily Aggregator Lambda]
    B --> C[Query 24h Sentiment History]
    C --> D[Calculate Daily Averages]
    D --> E[Store in Daily Sentiment Table]
    E --> F[Log Daily Statistics]
```

#### Monthly Yearly Posting Flow
```mermaid
graph TD
    A[EventBridge Monthly Trigger<br/>cron(0 1 1 * ? *)] --> B[Yearly Poster Lambda]
    B --> C[Query 365 Days Daily Data]
    C --> D[Generate Yearly Sparkline]
    D --> E[Post to Bluesky]
    E --> F[Pin Post to Account]
    F --> G[Log Posting Success]
```

### 5. Data Models

#### Daily Sentiment Data Point
```go
type DailySentimentDataPoint struct {
    Date            string    `json:"date" dynamodbav:"date"`                    // "2025-01-05"
    RunID           string    `json:"runId" dynamodbav:"runId"`                 // "daily-2025-01-05"
    AverageSentiment float64  `json:"averageSentiment" dynamodbav:"averageSentiment"`
    MinSentiment     float64  `json:"minSentiment" dynamodbav:"minSentiment"`
    MaxSentiment     float64  `json:"maxSentiment" dynamodbav:"maxSentiment"`
    TotalRuns        int      `json:"totalRuns" dynamodbav:"totalRuns"`
    TotalPosts       int      `json:"totalPosts" dynamodbav:"totalPosts"`
    CreatedAt        time.Time `json:"createdAt" dynamodbav:"createdAt"`
    TTL              int64    `json:"ttl" dynamodbav:"ttl"`
}
```

#### Yearly Sparkline Data Point
```go
type YearlySparklineDataPoint struct {
    Date            string    `json:"date"`
    AverageSentiment float64  `json:"averageSentiment"`
    MinSentiment     float64  `json:"minSentiment"`
    MaxSentiment     float64  `json:"maxSentiment"`
    // For visualization purposes
    Timestamp       time.Time `json:"timestamp"`
    NetSentimentPercent float64 `json:"netSentimentPercent"` // Alias for AverageSentiment
}
```

### 6. Error Handling and Edge Cases

#### Daily Aggregation Edge Cases
- **No data for 24h period**: Skip daily aggregation, log warning
- **Partial data**: Calculate with available data, log data quality
- **Multiple runs per day**: Use latest run data, log conflicts
- **DynamoDB errors**: Retry with exponential backoff, alert on persistent failures

#### Yearly Posting Edge Cases
- **Insufficient data**: Post message about building yearly history
- **Missing daily data**: Interpolate or skip missing days
- **Bluesky API errors**: Retry posting, fallback to text-only post
- **Pinning failures**: Log error but don't fail the entire operation

### 7. Monitoring and Alerting

#### CloudWatch Metrics
- `DailyAggregationSuccess` - Count of successful daily aggregations
- `DailyAggregationFailure` - Count of failed daily aggregations
- `YearlyPostingSuccess` - Count of successful yearly posts
- `YearlyPostingFailure` - Count of failed yearly posts
- `DataQualityScore` - Percentage of complete daily data

#### CloudWatch Alarms
- Daily aggregation failure rate > 10%
- Yearly posting failure
- Data quality score < 80%

### 8. Implementation Phases

#### Phase 1: Infrastructure Setup
1. Create DynamoDB table for daily sentiment
2. Add IAM policies for new table access
3. Create EventBridge rules for daily/monthly triggers
4. Update Terraform configuration

#### Phase 2: Daily Aggregation
1. Implement daily aggregator Lambda
2. Create daily sentiment data models
3. Add data quality validation
4. Implement error handling and retries

#### Phase 3: Yearly Visualization
1. Extend sparkline generator for yearly charts
2. Implement month marker drawing
3. Create yearly-specific styling
4. Add 25% larger canvas support

#### Phase 4: Yearly Posting
1. Implement yearly poster Lambda
2. Add Bluesky pinning functionality
3. Create yearly post formatting
4. Implement fallback scenarios

#### Phase 5: Testing and Monitoring
1. Create comprehensive test suite
2. Add CloudWatch monitoring
3. Implement alerting
4. Performance optimization

### 9. Cost Analysis

#### Additional AWS Costs (Monthly)
- **DynamoDB**: ~$2-5 (365 daily records + queries)
- **Lambda**: ~$1-2 (2 new functions, minimal execution time)
- **EventBridge**: ~$0.50 (2 scheduled rules)
- **CloudWatch**: ~$1 (logs and metrics)
- **Total**: ~$4.50-8.50/month

#### Storage Requirements
- **Daily sentiment table**: ~50KB/year (365 records × ~140 bytes)
- **3-year retention**: ~150KB total storage
- **Minimal impact** on existing costs

### 10. Security Considerations

#### IAM Permissions
- Least privilege access to new DynamoDB table
- Separate IAM policies for daily aggregator and yearly poster
- No cross-account access required

#### Data Privacy
- No PII in daily sentiment data
- Aggregated data only (no individual post content)
- TTL ensures automatic data cleanup

### 11. Testing Strategy

#### Unit Tests
- Daily aggregation logic
- Yearly sparkline generation
- Data model validation
- Error handling scenarios

#### Integration Tests
- End-to-end daily aggregation flow
- Yearly posting with mock Bluesky API
- DynamoDB operations
- EventBridge trigger testing

#### Load Tests
- Daily aggregation with large datasets
- Yearly chart generation performance
- DynamoDB query performance

### 12. Deployment Plan

#### Development Environment
1. Deploy to development AWS account
2. Test with historical data
3. Validate sparkline generation
4. Test posting functionality

#### Staging Environment
1. Deploy to staging with production-like data
2. Run for 30 days to validate daily aggregation
3. Test monthly yearly posting
4. Performance and cost validation

#### Production Deployment
1. Deploy infrastructure via Terraform
2. Deploy Lambda functions
3. Enable EventBridge rules
4. Monitor for 7 days before full activation

### 13. Rollback Plan

#### Immediate Rollback
- Disable EventBridge rules
- Stop Lambda function execution
- No data loss (read-only operations)

#### Data Rollback
- Daily sentiment table can be safely deleted
- No impact on existing sentiment history
- Easy to recreate from historical data

### 14. Future Enhancements

#### Potential Features
- Quarterly sentiment reports
- Sentiment trend analysis
- Custom time range charts
- Interactive web dashboard
- Sentiment prediction models

#### Technical Improvements
- Caching for frequently accessed data
- CDN for generated images
- Real-time sentiment streaming
- Machine learning integration

## Conclusion

This design provides a comprehensive solution for yearly sentiment analysis while maintaining the existing system's reliability and performance. The phased implementation approach ensures minimal risk while delivering valuable new functionality to users.

The solution is cost-effective, scalable, and follows AWS best practices for serverless architectures. The 3-year data retention provides long-term historical analysis capabilities while keeping storage costs minimal.
