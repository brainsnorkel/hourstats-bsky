package state

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// getMapKeys returns the keys of a map for debugging
func getMapKeys(m map[string]types.AttributeValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

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

// PostItem represents a post stored separately in DynamoDB
type PostItem struct {
	RunID     string    `json:"runId" dynamodbav:"runId"`
	Step      string    `json:"step" dynamodbav:"step"`     // Required for DynamoDB composite key
	PostID    string    `json:"postId" dynamodbav:"postId"` // runId#postIndex
	Post      Post      `json:"post" dynamodbav:"post"`
	CreatedAt time.Time `json:"createdAt" dynamodbav:"createdAt"`
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

// AddPosts adds posts to the run state by storing them separately
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

	log.Printf("🔍 STATE DEBUG: Adding %d new posts to run %s", len(posts), runID)

	// Store posts separately in DynamoDB
	for i, post := range posts {
		postItem := PostItem{
			RunID:     runID,
			Step:      "fetcher", // All posts are stored under the fetcher step
			PostID:    fmt.Sprintf("%s#%d", runID, state.TotalPostsRetrieved+i),
			Post:      post,
			CreatedAt: time.Now(),
			TTL:       time.Now().Add(7 * 24 * time.Hour).Unix(), // 7 days TTL
		}

		log.Printf("🔍 STATE DEBUG: Creating PostItem - RunID: %s, Step: %s, PostID: %s", postItem.RunID, postItem.Step, postItem.PostID)

		item, err := attributevalue.MarshalMap(postItem)
		if err != nil {
			return fmt.Errorf("failed to marshal post item: %w", err)
		}

		log.Printf("🔍 STATE DEBUG: Marshaled item keys: %v", getMapKeys(item))

		_, err = sm.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(sm.tableName),
			Item:      item,
		})
		if err != nil {
			log.Printf("🔍 STATE DEBUG: PutItem failed for PostID %s: %v", postItem.PostID, err)
			return fmt.Errorf("failed to store post item: %w", err)
		}
		log.Printf("🔍 STATE DEBUG: Successfully stored PostID %s in table %s", postItem.PostID, sm.tableName)
	}

	// Update the run state with new totals
	state.TotalPostsRetrieved += len(posts)
	state.Step = "fetcher"
	state.Status = "fetching"

	log.Printf("🔍 STATE DEBUG: After adding posts - total posts: %d", state.TotalPostsRetrieved)

	err = sm.UpdateRun(ctx, state)
	if err != nil {
		log.Printf("🔍 STATE DEBUG: Failed to update run state: %v", err)
		return err
	}

	log.Printf("🔍 STATE DEBUG: Successfully stored %d posts separately and updated run state", len(posts))
	return nil
}

// GetAllPosts retrieves all posts for a run
func (sm *StateManager) GetAllPosts(ctx context.Context, runID string) ([]Post, error) {
	log.Printf("🔍 STATE DEBUG: Retrieving all posts for run %s", runID)

	// Query all posts for this run using the GSI
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
		var postItem PostItem
		err := attributevalue.UnmarshalMap(item, &postItem)
		if err != nil {
			log.Printf("Warning: failed to unmarshal post item: %v", err)
			continue
		}
		posts = append(posts, postItem.Post)
	}

	log.Printf("🔍 STATE DEBUG: Retrieved %d posts for run %s", len(posts), runID)
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
	// Use scan to get all run states, then filter by step
	result, err := sm.client.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(sm.tableName),
		FilterExpression: aws.String("#step = :step"),
		ExpressionAttributeNames: map[string]string{
			"#step": "step",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":step": &types.AttributeValueMemberS{Value: "orchestrator"},
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
