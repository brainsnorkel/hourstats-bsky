package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

// Event represents the EventBridge event structure
type Event struct {
	Source string `json:"source"`
	Time   string `json:"time"`
}

// Response represents the Lambda response
type Response struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
	RunID      string `json:"runId,omitempty"`
}

// OrchestratorHandler handles the orchestrator Lambda function
type OrchestratorHandler struct {
	stateManager *state.StateManager
}

// NewOrchestratorHandler creates a new orchestrator handler
func NewOrchestratorHandler(ctx context.Context) (*OrchestratorHandler, error) {
	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	return &OrchestratorHandler{
		stateManager: stateManager,
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *OrchestratorHandler) HandleRequest(ctx context.Context, event Event) (Response, error) {
	log.Printf("Orchestrator received event: %+v", event)

	// Generate unique run ID
	runID := fmt.Sprintf("run-%d", time.Now().Unix())
	log.Printf("Starting new analysis run: %s", runID)

	// Create new run state
	_, err := h.stateManager.CreateRun(ctx, runID, 30) // TODO: Get from SSM
	if err != nil {
		log.Printf("Failed to create run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to create run state: " + err.Error(),
		}, err
	}

	log.Printf("Successfully created run state: %s", runID)
	return Response{
		StatusCode: 200,
		Body:       "Run state created successfully",
		RunID:      runID,
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