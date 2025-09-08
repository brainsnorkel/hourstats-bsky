# Bluesky HourStats - Sequence Diagrams

This document contains detailed sequence diagrams showing the data flow and interactions between components in the Bluesky HourStats system.

## Main Analysis Workflow

```mermaid
sequenceDiagram
    participant EB as EventBridge
    participant SF as Step Functions
    participant OR as Orchestrator Lambda
    participant FE as Fetcher Lambda
    participant AN as Analyzer Lambda
    participant AG as Aggregator Lambda
    participant PO as Poster Lambda
    participant SP as Sparkline Poster Lambda
    participant DB as DynamoDB
    participant S3 as S3 Bucket
    participant BS as Bluesky API

    EB->>SF: Trigger every 30 minutes
    SF->>OR: Start workflow
    
    OR->>DB: Create run state
    OR->>SF: Return run ID
    
    loop Parallel Fetching
        SF->>FE: Invoke fetcher with cursor
        FE->>BS: Fetch posts (100 per batch)
        BS-->>FE: Return posts
        FE->>DB: Store posts + cursor
        FE->>SF: Return completion status
    end
    
    SF->>OR: Check completion
    OR->>DB: Query all posts
    DB-->>OR: Return collected posts
    
    alt All data collected
        SF->>AN: Invoke analyzer
        AN->>DB: Load all posts
        DB-->>AN: Return posts
        AN->>AN: Analyze sentiment
        AN->>DB: Update posts with sentiment
        AN->>SF: Return completion
        
        SF->>AG: Invoke aggregator
        AG->>DB: Load analyzed posts
        DB-->>AG: Return posts
        AG->>AG: Rank posts by engagement
        AG->>AG: Calculate overall sentiment
        AG->>DB: Store final results
        AG->>SF: Return completion
        
        SF->>PO: Invoke poster
        PO->>DB: Load final results
        DB-->>PO: Return results
        PO->>BS: Post summary to Bluesky
        BS-->>PO: Return success
        PO->>SF: Return completion
        
        SF->>SP: Invoke sparkline poster
        SP->>DB: Query sentiment history
        DB-->>SP: Return 48h sentiment data
        SP->>SP: Generate PNG sparkline
        SP->>S3: Upload image
        S3-->>SP: Return image URL
        SP->>BS: Post sparkline with image
        BS-->>SP: Return success
        SP->>SF: Return completion
        
        SF->>EB: Workflow complete
    else Incomplete data
        SF->>OR: Retry fetching
        OR->>SF: Continue workflow
    end
```

## Sparkline Generation Workflow

```mermaid
sequenceDiagram
    participant SP as Sparkline Poster Lambda
    participant DB as DynamoDB
    participant GG as Go Graphics Library
    participant S3 as S3 Bucket
    participant BS as Bluesky API

    SP->>DB: Query sentiment history (48h)
    DB-->>SP: Return sentiment data points
    
    alt Sufficient data (≥24 points)
        SP->>GG: Generate sparkline chart
        Note over GG: Create 400x200 PNG<br/>with sentiment line graph
        GG-->>SP: Return PNG bytes
        
        SP->>S3: Upload PNG image
        Note over S3: Store with public read access<br/>Key: sparklines/{timestamp}.png
        S3-->>SP: Return image URL
        
        SP->>BS: Post with embedded image
        Note over BS: "📊 Sentiment for the last 48 hours"<br/>+ embedded sparkline image
        BS-->>SP: Return success
        
    else Insufficient data (<24 points)
        SP->>SP: Skip sparkline generation
        Note over SP: Log: "Insufficient data for sparkline"
    end
```

## Error Handling Workflow

```mermaid
sequenceDiagram
    participant SF as Step Functions
    participant LAMBDA as Any Lambda
    participant CW as CloudWatch
    participant DB as DynamoDB

    SF->>LAMBDA: Invoke Lambda
    LAMBDA->>LAMBDA: Process request
    
    alt Success
        LAMBDA->>SF: Return success
        SF->>SF: Continue workflow
    else Error
        LAMBDA->>CW: Log error
        LAMBDA->>DB: Update error status
        LAMBDA->>SF: Return error
        
        SF->>SF: Check retry policy
        
        alt Retryable error
            SF->>LAMBDA: Retry invocation
        else Fatal error
            SF->>SF: Mark workflow as failed
            SF->>CW: Log workflow failure
        end
    end
```

## Data Flow Architecture

```mermaid
graph TD
    A[EventBridge Trigger] --> B[Step Functions Workflow]
    B --> C[Orchestrator Lambda]
    C --> D[Parallel Fetcher Lambdas]
    D --> E[DynamoDB: Run State]
    E --> F[Analyzer Lambda]
    F --> G[DynamoDB: Sentiment Analysis]
    G --> H[Aggregator Lambda]
    H --> I[DynamoDB: Final Results]
    I --> J[Poster Lambda]
    J --> K[Bluesky API: Main Post]
    K --> L[Sparkline Poster Lambda]
    L --> M[DynamoDB: Sentiment History]
    M --> N[Go Graphics: PNG Generation]
    N --> O[S3: Image Storage]
    O --> P[Bluesky API: Sparkline Post]
    
    subgraph "Data Storage"
        E
        G
        I
        M
        O
    end
    
    subgraph "External APIs"
        K
        P
    end
```

## Cost Optimization Flow

```mermaid
graph TD
    A[Analysis Request] --> B{Data Volume Check}
    B -->|Low Volume| C[Standard Processing]
    B -->|High Volume| D[Optimized Processing]
    
    C --> E[1GB Lambda Memory]
    D --> F[3GB Lambda Memory]
    
    E --> G[Standard DynamoDB]
    F --> H[Provisioned DynamoDB]
    
    G --> I[Standard S3 Storage]
    H --> J[S3 with Lifecycle Policies]
    
    I --> K[Standard CloudWatch Logs]
    J --> L[Optimized CloudWatch Logs]
    
    K --> M[Cost: $15-25/month]
    L --> N[Cost: $50-100/month]
```

## Monitoring and Alerting Flow

```mermaid
sequenceDiagram
    participant LAMBDA as Lambda Function
    participant CW as CloudWatch
    participant ALARM as CloudWatch Alarm
    participant SNS as SNS Topic
    participant EMAIL as Email Notification

    LAMBDA->>CW: Send metrics
    Note over CW: Duration, Memory, Errors, Throttles
    
    CW->>ALARM: Check alarm conditions
    
    alt Alarm triggered
        ALARM->>SNS: Send notification
        SNS->>EMAIL: Send alert email
        Note over EMAIL: High error rate, Long duration, etc.
    else Normal operation
        ALARM->>ALARM: Continue monitoring
    end
```

## Security and Access Control

```mermaid
graph TD
    A[Lambda Function] --> B[IAM Role]
    B --> C[IAM Policies]
    C --> D[DynamoDB Access]
    C --> E[S3 Access]
    C --> F[CloudWatch Logs]
    
    G[SSM Parameter Store] --> H[Encrypted Parameters]
    H --> I[Bluesky Credentials]
    H --> J[Configuration Settings]
    
    K[VPC Configuration] --> L[Private Subnets]
    L --> M[Security Groups]
    M --> N[Controlled Outbound Access]
    
    subgraph "Security Layers"
        B
        C
        H
        M
    end
```

## Deployment and CI/CD Flow

```mermaid
sequenceDiagram
    participant DEV as Developer
    participant GH as GitHub
    participant GA as GitHub Actions
    participant TF as Terraform
    participant AWS as AWS Services

    DEV->>GH: Push code to main
    GH->>GA: Trigger workflow
    
    GA->>GA: Build Go binaries
    GA->>GA: Package Lambda functions
    GA->>TF: Run terraform plan
    TF->>AWS: Check current state
    AWS-->>TF: Return state
    TF-->>GA: Return plan
    
    GA->>TF: Run terraform apply
    TF->>AWS: Deploy infrastructure
    AWS-->>TF: Return deployment status
    TF-->>GA: Return success
    
    GA->>GH: Update deployment status
    GH-->>DEV: Notify deployment complete
```

This comprehensive set of sequence diagrams provides a complete view of how the Bluesky HourStats system operates, from the initial trigger through data processing, error handling, monitoring, and deployment.
