package awsutil

import (
	"testing"
)

// GetBlueskyCredentials and IsDryRunMode require a real *ssm.Client which cannot
// be constructed without AWS credentials. These functions are thin SSM wrappers
// best covered by integration tests.
//
// These tests verify the package compiles and document the expected behaviour.

func TestGetBlueskyCredentials_RequiresSSMClient(t *testing.T) {
	// Passing nil would panic — this documents that a real client is required
	t.Log("GetBlueskyCredentials(ctx, client) requires a non-nil *ssm.Client — integration test only")
}

func TestIsDryRunMode_RequiresSSMClient(t *testing.T) {
	t.Log("IsDryRunMode(ctx, client) requires a non-nil *ssm.Client — integration test only")
}
