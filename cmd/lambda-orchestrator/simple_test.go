package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEventStructure(t *testing.T) {
	// Test that our event structure is correct
	event := Event{
		Source: "aws.events",
		Time:   "2024-01-01T00:00:00Z",
	}

	assert.Equal(t, "aws.events", event.Source)
	assert.Equal(t, "2024-01-01T00:00:00Z", event.Time)
}

func TestResponseStructure(t *testing.T) {
	// Test that our response structure is correct
	response := Response{
		StatusCode: 200,
		Body:       "Success",
		RunID:      "run-1234567890",
	}

	assert.Equal(t, 200, response.StatusCode)
	assert.Equal(t, "Success", response.Body)
	assert.Equal(t, "run-1234567890", response.RunID)
}

func TestRunIDGeneration(t *testing.T) {
	// Test that we can generate run IDs
	runID1 := generateRunID()
	time.Sleep(1 * time.Millisecond) // Ensure different timestamps
	runID2 := generateRunID()

	assert.Contains(t, runID1, "run-")
	assert.Contains(t, runID2, "run-")
	assert.NotEqual(t, runID1, runID2) // Should be different
}

func generateRunID() string {
	return fmt.Sprintf("run-%d", time.Now().UnixNano())
}
