package main

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// StepFunctionsEvent represents the event from Step Functions
type StepFunctionsEvent struct {
	RunID                   string `json:"runId"`
	AnalysisIntervalMinutes int    `json:"analysisIntervalMinutes"`
	Status                  string `json:"status"`
}

// Response represents the Lambda response
type Response struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
	Posted     bool   `json:"posted"`
}

// PosterHandler handles the poster Lambda function
type PosterHandler struct {
	stateManager *state.StateManager
	ssmClient    *ssm.Client
}

// NewPosterHandler creates a new poster handler
func NewPosterHandler(ctx context.Context) (*PosterHandler, error) {
	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	// Initialize SSM client
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ssmClient := ssm.NewFromConfig(cfg)

	return &PosterHandler{
		stateManager: stateManager,
		ssmClient:    ssmClient,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *PosterHandler) HandleRequest(ctx context.Context, event StepFunctionsEvent) (Response, error) {
	log.Printf("Poster received event: %+v", event)

	// Get current run state
	runState, err := h.stateManager.GetLatestRun(ctx, event.RunID)
	if err != nil {
		log.Printf("Failed to get run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get run state: " + err.Error(),
		}, err
	}

	// Check if dry run mode is enabled
	dryRun, err := h.isDryRunMode(ctx)
	if err != nil {
		log.Printf("Failed to check dry run mode: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to check dry run mode: " + err.Error(),
		}, err
	}

	if dryRun {
		log.Printf("Dry run mode enabled, skipping post for run: %s", event.RunID)
		return Response{
			StatusCode: 200,
			Body:       "Dry run mode - post skipped",
			Posted:     false,
		}, nil
	}

	// Get Bluesky credentials
	handle, password, err := h.getBlueskyCredentials(ctx)
	if err != nil {
		log.Printf("Failed to get Bluesky credentials: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get credentials: " + err.Error(),
		}, err
	}

	// Create Bluesky client
	blueskyClient := client.New(handle, password)
	if err := blueskyClient.Authenticate(); err != nil {
		log.Printf("Failed to authenticate with Bluesky: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to authenticate: " + err.Error(),
		}, err
	}

	// Convert state posts to client posts
	clientPosts := h.convertToClientPosts(runState.TopPosts)

	// Post the summary
	if err := blueskyClient.PostTrendingSummary(clientPosts, runState.OverallSentiment, event.AnalysisIntervalMinutes); err != nil {
		log.Printf("Failed to post summary: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to post summary: " + err.Error(),
		}, err
	}

	// Mark posting as complete
	if err := h.stateManager.SetPostingComplete(ctx, event.RunID); err != nil {
		log.Printf("Failed to mark posting complete: %v", err)
		// Don't fail the entire operation for this
	}

	log.Printf("Successfully posted summary for run: %s", event.RunID)
	return Response{
		StatusCode: 200,
		Body:       "Summary posted successfully",
		Posted:     true,
	}, nil
}

// isDryRunMode checks if dry run mode is enabled
func (h *PosterHandler) isDryRunMode(ctx context.Context) (bool, error) {
	result, err := h.ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/hourstats/settings/dry_run"),
		WithDecryption: aws.Bool(false),
	})
	if err != nil {
		return false, fmt.Errorf("failed to get dry run parameter: %w", err)
	}

	return *result.Parameter.Value == "true", nil
}

// getBlueskyCredentials retrieves credentials from SSM
func (h *PosterHandler) getBlueskyCredentials(ctx context.Context) (string, string, error) {
	parameterNames := []string{
		"/hourstats/bluesky/handle",
		"/hourstats/bluesky/password",
	}

	result, err := h.ssmClient.GetParameters(ctx, &ssm.GetParametersInput{
		Names:          parameterNames,
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to get parameters: %w", err)
	}

	params := make(map[string]string)
	for _, p := range result.Parameters {
		params[*p.Name] = *p.Value
	}

	handle, ok := params["/hourstats/bluesky/handle"]
	if !ok {
		return "", "", fmt.Errorf("handle parameter not found")
	}

	password, ok := params["/hourstats/bluesky/password"]
	if !ok {
		return "", "", fmt.Errorf("password parameter not found")
	}

	return handle, password, nil
}

// convertToClientPosts converts state posts to client posts
func (h *PosterHandler) convertToClientPosts(posts []state.Post) []client.Post {
	clientPosts := make([]client.Post, len(posts))
	for i, post := range posts {
		clientPosts[i] = client.Post{
			URI:       post.URI,
			Text:      post.Text,
			Author:    post.Author,
			Likes:     post.Likes,
			Reposts:   post.Reposts,
			Replies:   post.Replies,
			CreatedAt: post.CreatedAt,
			Sentiment: post.Sentiment,
		}
	}
	return clientPosts
}

func main() {
	ctx := context.Background()
	handler, err := NewPosterHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create poster handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}
