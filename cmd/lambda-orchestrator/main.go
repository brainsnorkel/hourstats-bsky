package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/stepfunctions"
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
	stateManager    *state.StateManager
	stepFunctions   *stepfunctions.Client
	stateMachineArn string
}

// NewOrchestratorHandler creates a new orchestrator handler
func NewOrchestratorHandler(ctx context.Context) (*OrchestratorHandler, error) {
	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		return nil, fmt.Errorf("failed to create state manager: %w", err)
	}

	// Initialize Step Functions client
	cfg, err := stepfunctionsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	stepFunctions := stepfunctions.NewFromConfig(cfg)

	return &OrchestratorHandler{
		stateManager:    stateManager,
		stepFunctions:   stepFunctions,
		stateMachineArn: "arn:aws:states:us-east-1:ACCOUNT:stateMachine:hourstats-workflow", // TODO: Get from environment
	}, nil
}

// HandleRequest is the main Lambda handler
func (h *OrchestratorHandler) HandleRequest(ctx context.Context, event Event) (Response, error) {
	log.Printf("Orchestrator received event: %+v", event)

	// Generate unique run ID
	runID := fmt.Sprintf("run-%d", time.Now().Unix())
	log.Printf("Starting new analysis run: %s", runID)

	// Create new run state
	runState, err := h.stateManager.CreateRun(ctx, runID, 30) // TODO: Get from SSM
	if err != nil {
		log.Printf("Failed to create run state: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to create run state: " + err.Error(),
		}, err
	}

	// Start Step Functions workflow
	executionInput := map[string]interface{}{
		"runId":                   runID,
		"analysisIntervalMinutes": 30,
		"status":                  "initializing",
	}

	inputJSON, err := json.Marshal(executionInput)
	if err != nil {
		log.Printf("Failed to marshal execution input: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to marshal execution input: " + err.Error(),
		}, err
	}

	_, err = h.stepFunctions.StartExecution(ctx, &stepfunctions.StartExecutionInput{
		StateMachineArn: &h.stateMachineArn,
		Name:            &runID,
		Input:           aws.String(string(inputJSON)),
	})
	if err != nil {
		log.Printf("Failed to start Step Functions workflow: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to start workflow: " + err.Error(),
		}, err
	}

	log.Printf("Successfully started workflow for run: %s", runID)
	return Response{
		StatusCode: 200,
		Body:       "Workflow started successfully",
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
