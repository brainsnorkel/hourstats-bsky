package state

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// RunState represents the state of a single analysis run
type RunState struct {
	RunID                   string    `json:"runId" dynamodbav:"runId"`
	PostID                  string    `json:"postId" dynamodbav:"postId"` // For RunState, PostID = Step
	Step                    string    `json:"step" dynamodbav:"step"`
	Status                  string    `json:"status" dynamodbav:"status"`
	AnalysisIntervalMinutes int       `json:"analysisIntervalMinutes" dynamodbav:"analysisIntervalMinutes"`
	CutoffTime              time.Time `json:"cutoffTime" dynamodbav:"cutoffTime"`
	CurrentCursor           string    `json:"currentCursor,omitempty" dynamodbav:"currentCursor,omitempty"`
	TotalPostsRetrieved     int       `json:"totalPostsRetrieved" dynamodbav:"totalPostsRetrieved"`
	HasMorePosts            bool      `json:"hasMorePosts" dynamodbav:"hasMorePosts"`
	OverallSentiment        string    `json:"overallSentiment,omitempty" dynamodbav:"overallSentiment,omitempty"`
	NetSentimentPercentage  float64   `json:"netSentimentPercentage,omitempty" dynamodbav:"netSentimentPercentage,omitempty"`
	TopPosts                []Post    `json:"topPosts,omitempty" dynamodbav:"topPosts,omitempty"`
	CreatedAt               time.Time `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt" dynamodbav:"updatedAt"`
	TTL                     int64     `json:"ttl" dynamodbav:"ttl"`

	// Error tracking fields
	ErrorMessage  string    `json:"errorMessage,omitempty" dynamodbav:"errorMessage,omitempty"`
	RetryCount    int       `json:"retryCount" dynamodbav:"retryCount"`
	LastErrorTime time.Time `json:"lastErrorTime,omitempty" dynamodbav:"lastErrorTime,omitempty"`
	LastErrorStep string    `json:"lastErrorStep,omitempty" dynamodbav:"lastErrorStep,omitempty"`

	// Performance monitoring fields
	ProcessingTimeMs int64   `json:"processingTimeMs" dynamodbav:"processingTimeMs"`
	PostsPerSecond   float64 `json:"postsPerSecond" dynamodbav:"postsPerSecond"`
	MemoryUsageMB    int64   `json:"memoryUsageMB" dynamodbav:"memoryUsageMB"`
}

// Post represents a single post in the state
type Post struct {
	URI             string  `json:"uri" dynamodbav:"uri"`
	CID             string  `json:"cid" dynamodbav:"cid"`
	Text            string  `json:"text" dynamodbav:"text"`
	Author          string  `json:"author" dynamodbav:"author"`
	Likes           int     `json:"likes" dynamodbav:"likes"`
	Reposts         int     `json:"reposts" dynamodbav:"reposts"`
	Replies         int     `json:"replies" dynamodbav:"replies"`
	Sentiment       string  `json:"sentiment" dynamodbav:"sentiment"`
	EngagementScore float64 `json:"engagementScore" dynamodbav:"engagementScore"`
	CreatedAt       string  `json:"createdAt" dynamodbav:"createdAt"`
}

// PostItem represents a post stored separately in DynamoDB
type PostItem struct {
	RunID     string    `json:"runId" dynamodbav:"runId"`
	Step      string    `json:"step" dynamodbav:"step"`     // Required for DynamoDB composite key
	PostID    string    `json:"postId" dynamodbav:"postId"` // runId#postIndex
	Post      Post      `json:"post" dynamodbav:"post"`
	CreatedAt time.Time `json:"createdAt" dynamodbav:"createdAt"`
	TTL       int64     `json:"ttl" dynamodbav:"ttl"`
}

// PostBatch represents a batch of posts stored together in DynamoDB for cost efficiency
type PostBatch struct {
	RunID     string    `json:"runId" dynamodbav:"runId"`
	Step      string    `json:"step" dynamodbav:"step"`     // Required for DynamoDB composite key
	PostID    string    `json:"postId" dynamodbav:"postId"` // runId#batchIndex
	Posts     []Post    `json:"posts" dynamodbav:"posts"`
	CreatedAt string    `json:"createdAt" dynamodbav:"createdAt"`
	TTL       int64     `json:"ttl" dynamodbav:"ttl"`
}

// StateManager handles DynamoDB state operations
type StateManager struct {
	client    *dynamodb.Client
	tableName string
}

// NewStateManager creates a new state manager
func NewStateManager(ctx context.Context, tableName string) (*StateManager, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &StateManager{
		client:    dynamodb.NewFromConfig(cfg),
		tableName: tableName,
	}, nil
}

// CreateRun creates a new analysis run state
func (sm *StateManager) CreateRun(ctx context.Context, runID string, analysisIntervalMinutes int) (*RunState, error) {
	now := time.Now()
	ttl := now.Add(2 * 24 * time.Hour).Unix() // 2 days TTL

	// Calculate cutoff time once for consistency across all processes
	cutoffTime := now.Add(-time.Duration(analysisIntervalMinutes) * time.Minute)

	state := &RunState{
		RunID:                   runID,
		PostID:                  "orchestrator", // For RunState, PostID = Step
		Step:                    "orchestrator",
		Status:                  "initializing",
		AnalysisIntervalMinutes: analysisIntervalMinutes,
		CutoffTime:              cutoffTime,
		TotalPostsRetrieved:     0,
		HasMorePosts:            true,
		CreatedAt:               now,
		UpdatedAt:               now,
		TTL:                     ttl,
	}

	item, err := attributevalue.MarshalMap(state)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal state: %w", err)
	}

	_, err = sm.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(sm.tableName),
		Item:      item,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create run state: %w", err)
	}

	return state, nil
}

// UpdateRun updates an existing run state
func (sm *StateManager) UpdateRun(ctx context.Context, state *RunState) error {
	state.UpdatedAt = time.Now()

	item, err := attributevalue.MarshalMap(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	_, err = sm.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(sm.tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("failed to update run state: %w", err)
	}

	return nil
}

// GetRun retrieves a run state by runID and step
func (sm *StateManager) GetRun(ctx context.Context, runID, step string) (*RunState, error) {
	result, err := sm.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(sm.tableName),
		Key: map[string]types.AttributeValue{
			"runId":  &types.AttributeValueMemberS{Value: runID},
			"postId": &types.AttributeValueMemberS{Value: step}, // For RunState, postId = step
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get run state: %w", err)
	}

	if result.Item == nil {
		return nil, fmt.Errorf("run state not found: %s/%s", runID, step)
	}

	var state RunState
	err = attributevalue.UnmarshalMap(result.Item, &state)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return &state, nil
}

// GetLatestRun retrieves the latest run state for a given runID
func (sm *StateManager) GetLatestRun(ctx context.Context, runID string) (*RunState, error) {
	// Get the run state (orchestrator step)
	return sm.GetRun(ctx, runID, "orchestrator")
}

// AddPosts adds posts to the run state by storing them in batches for cost efficiency
func (sm *StateManager) AddPosts(ctx context.Context, runID string, posts []Post) error {
	// Try to get fetcher step first, fall back to orchestrator step
	state, err := sm.GetRun(ctx, runID, "fetcher")
	if err != nil {
		// If fetcher step doesn't exist, get orchestrator step
		state, err = sm.GetRun(ctx, runID, "orchestrator")
		if err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
	}

	// Store posts in batches of 100 for cost efficiency
	// This reduces the number of DynamoDB items by 99% (100 posts per item vs 1 post per item)
	const postsPerBatch = 100
	batchIndex := 0
	
	for i := 0; i < len(posts); i += postsPerBatch {
		end := i + postsPerBatch
		if end > len(posts) {
			end = len(posts)
		}

		// Create a batch of posts
		postBatch := PostBatch{
			RunID:     runID,
			Step:      "fetcher", // All posts are stored under the fetcher step
			PostID:    fmt.Sprintf("%s#batch%d", runID, batchIndex),
			Posts:     posts[i:end],
			CreatedAt: time.Now().Format(time.RFC3339),
			TTL:       time.Now().Add(2 * 24 * time.Hour).Unix(), // 2 days TTL
		}

		item, err := attributevalue.MarshalMap(postBatch)
		if err != nil {
			return fmt.Errorf("failed to marshal post batch: %w", err)
		}

		// Store the batch as a single item
		_, err = sm.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(sm.tableName),
			Item:      item,
		})
		if err != nil {
			return fmt.Errorf("failed to store post batch: %w", err)
		}

		batchIndex++
	}

	// Update the run state with new totals
	state.TotalPostsRetrieved += len(posts)
	state.Step = "fetcher"
	state.Status = "fetching"

	err = sm.UpdateRun(ctx, state)
	if err != nil {
		return err
	}
	return nil
}

// GetAllPosts retrieves all posts for a run
func (sm *StateManager) GetAllPosts(ctx context.Context, runID string) ([]Post, error) {

	// Query all posts for this run using the posts-index GSI
	result, err := sm.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(sm.tableName),
		IndexName:              aws.String("posts-index"),
		KeyConditionExpression: aws.String("runId = :runId AND begins_with(postId, :postIdPrefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":runId":        &types.AttributeValueMemberS{Value: runID},
			":postIdPrefix": &types.AttributeValueMemberS{Value: runID + "#"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query posts: %w", err)
	}

	var posts []Post
	for _, item := range result.Items {
		// Try to unmarshal as PostBatch first (new format)
		var postBatch PostBatch
		err := attributevalue.UnmarshalMap(item, &postBatch)
		if err == nil && strings.Contains(postBatch.PostID, "#batch") {
			// This is a batched post item
			posts = append(posts, postBatch.Posts...)
			continue
		}

		// Fallback to individual PostItem (legacy format)
		var postItem PostItem
		err = attributevalue.UnmarshalMap(item, &postItem)
		if err != nil {
			log.Printf("Warning: failed to unmarshal post item: %v", err)
			continue
		}
		// Only include posts that have a postId with # (filter out run state items)
		if strings.Contains(postItem.PostID, "#") && !strings.Contains(postItem.PostID, "#batch") {
			posts = append(posts, postItem.Post)
		}
	}

	return posts, nil
}

// UpdateCursor updates the cursor for the next fetch
func (sm *StateManager) UpdateCursor(ctx context.Context, runID, cursor string, hasMorePosts bool) error {
	// Try to get fetcher step first, fall back to orchestrator step
	state, err := sm.GetRun(ctx, runID, "fetcher")
	if err != nil {
		// If fetcher step doesn't exist, get orchestrator step
		state, err = sm.GetRun(ctx, runID, "orchestrator")
		if err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
	}

	// Update cursor and status
	state.CurrentCursor = cursor
	state.HasMorePosts = hasMorePosts
	state.Step = "fetcher"
	state.Status = "fetching"

	return sm.UpdateRun(ctx, state)
}

// SetAnalysisComplete marks the analysis as complete
func (sm *StateManager) SetAnalysisComplete(ctx context.Context, runID string, overallSentiment string, topPosts []Post) error {
	state, err := sm.GetLatestRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to get current state: %w", err)
	}

	state.OverallSentiment = overallSentiment
	state.TopPosts = topPosts
	state.Step = "aggregator"
	state.Status = "analyzed"

	return sm.UpdateRun(ctx, state)
}

// SetPostingComplete marks the posting as complete
func (sm *StateManager) SetPostingComplete(ctx context.Context, runID string) error {
	state, err := sm.GetLatestRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to get current state: %w", err)
	}

	state.Step = "poster"
	state.Status = "completed"

	return sm.UpdateRun(ctx, state)
}

// ListRuns retrieves all run IDs from DynamoDB
func (sm *StateManager) ListRuns(ctx context.Context, limit int32) ([]string, error) {
	// Use scan to get all run states (RunState items have postId = "orchestrator")
	result, err := sm.client.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(sm.tableName),
		FilterExpression: aws.String("#postId = :postId"),
		ExpressionAttributeNames: map[string]string{
			"#postId": "postId",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":postId": &types.AttributeValueMemberS{Value: "orchestrator"},
		},
		Limit: aws.Int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan runs: %w", err)
	}

	var runIDs []string
	for _, item := range result.Items {
		var state RunState
		err := attributevalue.UnmarshalMap(item, &state)
		if err != nil {
			log.Printf("Warning: failed to unmarshal run state: %v", err)
			continue
		}
		runIDs = append(runIDs, state.RunID)
	}

	// Sort by creation time (most recent first)
	// Note: This is a simple approach - for better performance with large datasets,
	// consider using a different GSI or query strategy
	return runIDs, nil
}

// GetRunStats returns statistics about a run
func (sm *StateManager) GetRunStats(ctx context.Context, runID string) (*RunStats, error) {
	// Get the run state
	state, err := sm.GetLatestRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to get run state: %w", err)
	}

	// Get all posts for this run
	posts, err := sm.GetAllPosts(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to get posts: %w", err)
	}

	return &RunStats{
		RunID:                   state.RunID,
		Status:                  state.Status,
		Step:                    state.Step,
		AnalysisIntervalMinutes: state.AnalysisIntervalMinutes,
		CutoffTime:              state.CutoffTime,
		TotalPostsRetrieved:     state.TotalPostsRetrieved,
		ActualPostsCount:        len(posts),
		CreatedAt:               state.CreatedAt,
		UpdatedAt:               state.UpdatedAt,
		OverallSentiment:        state.OverallSentiment,
		TopPostsCount:           len(state.TopPosts),
	}, nil
}

// RunStats represents statistics about a run
type RunStats struct {
	RunID                   string    `json:"runId"`
	Status                  string    `json:"status"`
	Step                    string    `json:"step"`
	AnalysisIntervalMinutes int       `json:"analysisIntervalMinutes"`
	CutoffTime              time.Time `json:"cutoffTime"`
	TotalPostsRetrieved     int       `json:"totalPostsRetrieved"`
	ActualPostsCount        int       `json:"actualPostsCount"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
	OverallSentiment        string    `json:"overallSentiment,omitempty"`
	TopPostsCount           int       `json:"topPostsCount"`
}
