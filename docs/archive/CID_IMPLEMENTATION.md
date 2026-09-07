# CID Implementation for Embed Cards

## Overview

This document outlines the implementation plan for storing and using Content Identifiers (CIDs) to enable embed cards in Bluesky posts. Currently, embed cards are disabled because they require a CID, which is not being stored.

## Background

Bluesky embed cards require both a URI and a CID (Content Identifier) to properly display a post as an embedded card. The current implementation only stores the URI but not the CID, which is why embed cards are commented out.

## Implementation Plan

### 1. Update Data Structures

#### A. Add CID to Post struct in `internal/client/bluesky.go`
```go
type Post struct {
    URI             string
    CID             string  // Add this field
    Text            string
    Author          string
    // ... existing fields
}
```

#### B. Add CID to Post struct in `internal/state/state.go`
```go
type Post struct {
    URI             string
    CID             string  // Add this field
    Text            string
    Author          string
    // ... existing fields
}
```

### 2. Extract CID from Bluesky API

The Bluesky API response includes a CID in the `postView` object. Update the `GetTrendingPosts` function in `internal/client/bluesky.go`:

```go
// In the post processing loop
post := Post{
    URI:             uri,
    CID:             postView.Cid,  // Extract CID from API response
    Text:            postView.Record.Text,
    Author:          postView.Author.Handle,
    // ... other fields
}
```

### 3. Update DynamoDB Storage

#### A. Update PostItem in `internal/state/state.go`
```go
type PostItem struct {
    RunID           string `dynamodbav:"runId"`
    PostID          string `dynamodbav:"postId"`
    URI             string `dynamodbav:"uri"`
    CID             string `dynamodbav:"cid"`  // Add this field
    Text            string `dynamodbav:"text"`
    Author          string `dynamodbav:"author"`
    // ... other fields
}
```

#### B. Update DynamoDB table schema in `terraform/main.tf`
```hcl
resource "aws_dynamodb_table" "hourstats_state" {
  # ... existing configuration
  
  attribute {
    name = "cid"
    type = "S"
  }
  
  # Add CID to global secondary indexes if needed
}
```

### 4. Re-enable Embed Cards

#### A. Uncomment createEmbedCard function in `internal/client/bluesky.go`
```go
func (c *BlueskyClient) createEmbedCard(ctx context.Context, post Post) *bsky.FeedPost_Embed {
    if post.URI == "" || post.CID == "" {
        return nil
    }
    
    return &bsky.FeedPost_Embed{
        EmbedRecord: &bsky.EmbedRecord{
            Record: &atproto.RepoStrongRef{
                Uri: post.URI,
                Cid: post.CID,  // Now we have the CID
            },
        },
    }
}
```

#### B. Enable embed card creation in PostTrendingSummary
```go
// Create embed card for the first post if available
var embed *bsky.FeedPost_Embed
if len(posts) > 0 {
    for _, post := range posts {
        if post.URI != "" && post.CID != "" {
            embed = c.createEmbedCard(ctx, post)
            break
        }
    }
}
```

### 5. Update All Components

Ensure all components that create or process posts include the CID field:

- `cmd/lambda-fetcher/main.go` - Pass CID when creating posts
- `cmd/lambda-processor/main.go` - Handle CID in post processing
- `cmd/local-test/main.go` - Include CID in mock data
- `cmd/query-runs/main.go` - Display CID in run analysis

### 6. Testing

1. **Local Testing**: Run `go run cmd/local-test/main.go 1 live` to test with real data
2. **Verify CID Extraction**: Check logs to ensure CIDs are being extracted from API
3. **Test Embed Cards**: Verify that embed cards appear in posted summaries
4. **DynamoDB Verification**: Check that CIDs are stored in DynamoDB

### 7. Migration Considerations

Since this adds a new field to the DynamoDB schema:
- Existing posts in DynamoDB will have empty CID fields
- This is acceptable as embed cards are optional
- New posts will have CIDs and can display embed cards
- Consider adding a migration script if needed

## Benefits

- **Enhanced User Experience**: Posts will display as rich embed cards
- **Better Engagement**: Embed cards make posts more visually appealing
- **Professional Appearance**: Matches the quality of other Bluesky bots

## Implementation Order

1. Update data structures (Post, PostItem)
2. Extract CID from API response
3. Update DynamoDB storage
4. Update Terraform schema
5. Re-enable embed card functions
6. Test with local environment
7. Deploy and verify in production

## Notes

- CIDs are immutable content identifiers in the AT Protocol
- They ensure the embed card shows the exact version of the post
- If a post is edited, it gets a new CID, which is why we store it
- The CID is required for `atproto.RepoStrongRef` in embed cards
