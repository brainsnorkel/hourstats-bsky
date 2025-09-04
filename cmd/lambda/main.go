package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	lambdapkg "github.com/christophergentle/hourstats-bsky/internal/lambda"
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
}

// HandleRequest is the main Lambda handler
func HandleRequest(ctx context.Context, event Event) (Response, error) {
	log.Printf("Received event: %+v", event)

	// Load configuration from SSM Parameter Store
	configLoader, err := lambdapkg.NewSSMConfigLoader(ctx)
	if err != nil {
		log.Printf("Failed to create SSM config loader: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to initialize configuration loader",
		}, nil
	}

	cfg, err := configLoader.LoadConfig(ctx)
	if err != nil {
		log.Printf("Failed to load configuration: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Failed to load configuration from SSM",
		}, nil
	}

	// Initialize and run analysis
	analyzer := lambdapkg.NewHourStatsAnalyzer(cfg)
	result, err := analyzer.RunAnalysis(ctx)
	if err != nil {
		log.Printf("Analysis failed: %v", err)
		return Response{
			StatusCode: 500,
			Body:       "Analysis failed: " + err.Error(),
		}, nil
	}

	log.Printf("Analysis completed successfully: %+v", result)
	return Response{
		StatusCode: 200,
		Body:       "Analysis completed successfully",
	}, nil
}

func main() {
	lambda.Start(HandleRequest)
}
