package awsutil

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// GetBlueskyCredentials retrieves the Bluesky handle and app password from SSM
// Parameter Store using a single batch call.
func GetBlueskyCredentials(ctx context.Context, client *ssm.Client) (handle, password string, err error) {
	result, err := client.GetParameters(ctx, &ssm.GetParametersInput{
		Names: []string{
			"/hourstats/bluesky/handle",
			"/hourstats/bluesky/password",
		},
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to get bluesky credentials: %w", err)
	}

	params := make(map[string]string, len(result.Parameters))
	for _, p := range result.Parameters {
		params[*p.Name] = *p.Value
	}

	handle, ok := params["/hourstats/bluesky/handle"]
	if !ok {
		return "", "", fmt.Errorf("bluesky handle parameter not found")
	}

	password, ok = params["/hourstats/bluesky/password"]
	if !ok {
		return "", "", fmt.Errorf("bluesky password parameter not found")
	}

	return handle, password, nil
}

// IsDryRunMode checks if dry run mode is enabled via SSM Parameter Store.
func IsDryRunMode(ctx context.Context, client *ssm.Client) (bool, error) {
	result, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/hourstats/settings/dry_run"),
		WithDecryption: aws.Bool(false),
	})
	if err != nil {
		return false, fmt.Errorf("failed to get dry run parameter: %w", err)
	}

	return *result.Parameter.Value == "true", nil
}
