package lambda

import (
	"context"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/christophergentle/hourstats-bsky/internal/config"
)

// SSMConfigLoader handles loading configuration from SSM Parameter Store
type SSMConfigLoader struct {
	client *ssm.Client
}

// NewSSMConfigLoader creates a new SSM configuration loader
func NewSSMConfigLoader(ctx context.Context) (*SSMConfigLoader, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	return &SSMConfigLoader{
		client: ssm.NewFromConfig(cfg),
	}, nil
}

// LoadConfig loads configuration from SSM Parameter Store
func (s *SSMConfigLoader) LoadConfig(ctx context.Context) (*config.Config, error) {
	// Define parameter names
	parameterNames := []string{
		"/hourstats/bluesky/handle",
		"/hourstats/bluesky/password",
		"/hourstats/settings/analysis_interval_minutes",
		"/hourstats/settings/top_posts_count",
		"/hourstats/settings/min_engagement_score",
		"/hourstats/settings/dry_run",
	}

	// Get parameters from SSM
	result, err := s.client.GetParameters(ctx, &ssm.GetParametersInput{
		Names:          parameterNames,
		WithDecryption: true,
	})
	if err != nil {
		return nil, err
	}

	// Check for any invalid parameters
	if len(result.InvalidParameters) > 0 {
		return nil, &ConfigError{
			Message: "Invalid parameters found",
			Details: result.InvalidParameters,
		}
	}

	// Create parameter map
	params := make(map[string]string)
	for _, param := range result.Parameters {
		if param.Name != nil && param.Value != nil {
			params[*param.Name] = *param.Value
		}
	}

	// Validate required parameters
	if handle, ok := params["/hourstats/bluesky/handle"]; !ok || handle == "" {
		return nil, &ConfigError{
			Message: "Missing required parameter: /hourstats/bluesky/handle",
		}
	}

	if password, ok := params["/hourstats/bluesky/password"]; !ok || password == "" {
		return nil, &ConfigError{
			Message: "Missing required parameter: /hourstats/bluesky/password",
		}
	}

	// Parse numeric parameters with defaults
	analysisIntervalMinutes := parseIntWithDefault(params["/hourstats/settings/analysis_interval_minutes"], 30)
	topPostsCount := parseIntWithDefault(params["/hourstats/settings/top_posts_count"], 5)
	minEngagementScore := parseIntWithDefault(params["/hourstats/settings/min_engagement_score"], 10)

	// Parse boolean parameter with default
	dryRun := parseBoolWithDefault(params["/hourstats/settings/dry_run"], false)

	// Create and return config
	return &config.Config{
		Bluesky: config.BlueskyConfig{
			Handle:   params["/hourstats/bluesky/handle"],
			Password: params["/hourstats/bluesky/password"],
		},
		Settings: config.SettingsConfig{
			AnalysisIntervalMinutes: analysisIntervalMinutes,
			TopPostsCount:          topPostsCount,
			MinEngagementScore:     minEngagementScore,
			DryRun:                 dryRun,
		},
	}, nil
}

// parseIntWithDefault parses an integer with a default value
func parseIntWithDefault(value string, defaultValue int) int {
	if value == "" {
		return defaultValue
	}
	
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	
	return parsed
}

// parseBoolWithDefault parses a boolean with a default value
func parseBoolWithDefault(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}
	
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	
	return parsed
}

// ConfigError represents a configuration error
type ConfigError struct {
	Message string
	Details []string
}

func (e *ConfigError) Error() string {
	if len(e.Details) > 0 {
		return e.Message + ": " + strconv.Itoa(len(e.Details)) + " invalid parameters"
	}
	return e.Message
}
