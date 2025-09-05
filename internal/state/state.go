package state

import (
	"context"
	"fmt"
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
	Step                    string    `json:"step" dynamodbav:"step"`
	Status                  string    `json:"status" dynamodbav:"status"`
	AnalysisIntervalMinutes int       `json:"analysisIntervalMinutes" dynamodbav:"analysisIntervalMinutes"`
	CutoffTime              time.Time `json:"cutoffTime" dynamodbav:"cutoffTime"`
	CurrentCursor           string    `json:"currentCursor,omitempty" dynamodbav:"currentCursor,omitempty"`
	TotalPostsRetrieved     int       `json:"totalPostsRetrieved" dynamodbav:"totalPostsRetrieved"`
	HasMorePosts            bool      `json:"hasMorePosts" dynamodbav:"hasMorePosts"`
	Posts                   []Post    `json:"posts,omitempty" dynamodbav:"posts,omitempty"`
	OverallSentiment        string    `json:"overallSentiment,omitempty" dynamodbav:"overallSentiment,omitempty"`
	TopPosts                []Post    `json:"topPosts,omitempty" dynamodbav:"topPosts,omitempty"`
	CreatedAt               time.Time `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt" dynamodbav:"updatedAt"`
	TTL                     int64     `json:"ttl" dynamodbav:"ttl"`
}

// Post represents a single post in the state
type Post struct {
	URI             string  `json:"uri" dynamodbav:"uri"`
	Text            string  `json:"text" dynamodbav:"text"`
	Author          string  `json:"author" dynamodbav:"author"`
	Likes           int     `json:"likes" dynamodbav:"likes"`
	Reposts         int     `json:"reposts" dynamodbav:"reposts"`
	Replies         int     `json:"replies" dynamodbav:"replies"`
	Sentiment       string  `json:"sentiment" dynamodbav:"sentiment"`
	EngagementScore float64 `json:"engagementScore" dynamodbav:"engagementScore"`
	CreatedAt       string  `json:"createdAt" dynamodbav:"createdAt"`
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
	ttl := now.Add(7 * 24 * time.Hour).Unix() // 7 days TTL
	
	// Calculate cutoff time once for consistency across all processes
	cutoffTime := now.Add(-time.Duration(analysisIntervalMinutes) * time.Minute)

	state := &RunState{
		RunID:                   runID,
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
			"runId": &types.AttributeValueMemberS{Value: runID},
			"step":  &types.AttributeValueMemberS{Value: step},
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
	// Query for all steps of this run, ordered by step
	result, err := sm.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(sm.tableName),
		KeyConditionExpression: aws.String("runId = :runId"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":runId": &types.AttributeValueMemberS{Value: runID},
		},
		ScanIndexForward: aws.Bool(false), // Descending order
		Limit:            aws.Int32(1),    // Get only the latest
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query run state: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("run state not found: %s", runID)
	}

	var state RunState
	err = attributevalue.UnmarshalMap(result.Items[0], &state)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return &state, nil
}

// AddPosts adds posts to the run state
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

	// Add new posts
	state.Posts = append(state.Posts, posts...)
	state.TotalPostsRetrieved = len(state.Posts)
	state.Step = "fetcher"
	state.Status = "fetching"

	return sm.UpdateRun(ctx, state)
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

	// Preserve existing posts and total count
	existingPosts := state.Posts
	existingTotal := state.TotalPostsRetrieved

	state.CurrentCursor = cursor
	state.HasMorePosts = hasMorePosts
	state.Step = "fetcher"
	state.Status = "fetching"

	// Restore the posts and total count
	state.Posts = existingPosts
	state.TotalPostsRetrieved = existingTotal

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
