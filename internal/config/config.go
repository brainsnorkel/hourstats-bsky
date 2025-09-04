package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Bluesky  BlueskyConfig  `yaml:"bluesky"`
	Settings SettingsConfig `yaml:"settings"`
}

type BlueskyConfig struct {
	Handle   string `yaml:"handle"`
	Password string `yaml:"password"`
}

type SettingsConfig struct {
	AnalysisIntervalHours int  `yaml:"analysis_interval_hours"`
	TopPostsCount         int  `yaml:"top_posts_count"`
	MinEngagementScore    int  `yaml:"min_engagement_score"`
	DryRun                bool `yaml:"dry_run"`
}

// LoadConfig loads configuration from config.yaml file
func LoadConfig() (*Config, error) {
	// Look for config.yaml in current directory
	configPath := "config.yaml"
	
	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("config.yaml not found. Please copy config.example.yaml to config.yaml and fill in your credentials")
	}
	
	// Read the file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	
	// Parse YAML
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	
	// Validate required fields
	if config.Bluesky.Handle == "" || config.Bluesky.Handle == "your-handle.bsky.social" {
		return nil, fmt.Errorf("please set your Bluesky handle in config.yaml")
	}
	
	if config.Bluesky.Password == "" || config.Bluesky.Password == "your-app-password" {
		return nil, fmt.Errorf("please set your Bluesky app password in config.yaml")
	}
	
	// Set defaults for optional fields
	if config.Settings.AnalysisIntervalHours == 0 {
		config.Settings.AnalysisIntervalHours = 1
	}
	if config.Settings.TopPostsCount == 0 {
		config.Settings.TopPostsCount = 5
	}
	if config.Settings.MinEngagementScore == 0 {
		config.Settings.MinEngagementScore = 10
	}
	
	return &config, nil
}

// LoadConfigFromEnv loads configuration from environment variables (fallback)
func LoadConfigFromEnv() *Config {
	return &Config{
		Bluesky: BlueskyConfig{
			Handle:   os.Getenv("BLUESKY_HANDLE"),
			Password: os.Getenv("BLUESKY_PASSWORD"),
		},
		Settings: SettingsConfig{
			AnalysisIntervalHours: 1,
			TopPostsCount:         5,
			MinEngagementScore:    10,
			DryRun:                os.Getenv("DRY_RUN") == "true",
		},
	}
}

// GetConfigPath returns the path to the config file
func GetConfigPath() string {
	// Try current directory first
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}
	
	// Try executable directory
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		configPath := filepath.Join(dir, "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return configPath
		}
	}
	
	return "config.yaml"
}
