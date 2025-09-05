package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// Event represents the EventBridge event structure or Step Functions event
type Event struct {
	Source     string `json:"source"`
	Time       string `json:"time"`
	Action     string `json:"action,omitempty"`
	RunID      string `json:"runId,omitempty"`
	IsComplete bool   `json:"isComplete,omitempty"`
}

// Response represents the Lambda response
type Response struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
	RunID      string `json:"runId,omitempty"`
	IsComplete bool   `json:"isComplete,omitempty"`
}

// OrchestratorHandler handles the orchestrator Lambda function
type OrchestratorHandler struct {
	stateManager *state.StateManager
	lambdaClient *awslambda.Client
}

// NewOrchestratorHandler creates a new orchestrator handler
func NewOrchestratorHandler(ctx context.Context) (*OrchestratorHandler, error) {
	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	// Initialize Lambda client for invoking other functions
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &OrchestratorHandler{
		stateManager: stateManager,
		lambdaClient: awslambda.NewFromConfig(cfg),
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *OrchestratorHandler) HandleRequest(ctx context.Context, event Event) (Response, error) {
	log.Printf("Orchestrator received event: %+v", event)

	// Handle different actions
	switch event.Action {
	case "checkCompletion":
		return h.handleCheckCompletion(ctx, event)
	default:
		return h.handleStartWorkflow(ctx, event)
	}
}

// handleStartWorkflow starts a new analysis workflow
func (h *OrchestratorHandler) handleStartWorkflow(ctx context.Context, event Event) (Response, error) {
	// Generate unique run ID
	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	log.Printf("Starting new analysis run: %s", runID)

	// Create new run state
	_, err := h.stateManager.CreateRun(ctx, runID, 30) // TODO: Get from SSM
	if err != nil {
		log.Printf("Failed to create run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to create run state: " + err.Error(),
			RunID:      runID,
		}, err
	}

	log.Printf("Created run state for continuous fetching: %s", runID)

	return Response{
		StatusCode: 200,
		Body:       "Run state created successfully",
		RunID:      runID,
	}, nil
}

// handleCheckCompletion checks if all fetching is complete
func (h *OrchestratorHandler) handleCheckCompletion(ctx context.Context, event Event) (Response, error) {
	runID := event.RunID
	log.Printf("Checking completion for run: %s", runID)

	// Get the current run state
	state, err := h.stateManager.GetRun(ctx, runID, "orchestrator")
	if err != nil {
		log.Printf("Failed to get run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to get run state: " + err.Error(),
			RunID:      runID,
		}, err
	}

	// Check if all fetcher batches are complete
	// For now, we'll use a simple heuristic: if we've been running for more than 10 minutes, consider it complete
	// In a real implementation, we'd check the status of all fetcher batches
	createdAt := state.CreatedAt

	isComplete := time.Since(createdAt) > 10*time.Minute
	log.Printf("Run %s completion status: %v (running for %v)", runID, isComplete, time.Since(createdAt))

	return Response{
		StatusCode: 200,
		Body:       "Completion check completed",
		RunID:      runID,
		IsComplete: isComplete,
	}, nil
}

func main() {
	ctx := context.Background()
	handler, err := NewOrchestratorHandler(ctx)
	if err != nil {
		log.Fatalf("Failed to create orchestrator handler: %v", err)
	}

	lambda.Start(handler.HandleRequest)
}