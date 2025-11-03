package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

const (
	expectedRunsPer24Hours = 48 // Every 30 minutes = 48 runs per day
	region                  = "us-east-1"
)

func main() {
	var (
		command = flag.String("cmd", "status", "Command to run: status, runs, current, errors, validate, tail, all")
		tailFunc = flag.String("function", "", "Lambda function name for tail command (orchestrator, fetcher, processor, sparkline-poster)")
		filter   = flag.String("filter", "all", "Filter for tail command: all, errors, success")
		limit    = flag.Int("limit", 10, "Number of recent runs to show")
	)
	flag.Parse()

	ctx := context.Background()

	// Initialize state manager
	stateManager, err := state.NewStateManager(ctx, "hourstats-state")
	if err != nil {
		log.Fatalf("Failed to create state manager: %v", err)
	}

	switch *command {
	case "status":
		showStatus(ctx, stateManager, *limit)
	case "runs":
		showRecentRuns(ctx, stateManager, *limit)
	case "current":
		showCurrentRunState(ctx, stateManager)
	case "errors":
		detectErrors(ctx, stateManager, *limit)
	case "validate":
		validateRunCount(ctx, stateManager)
	case "tail":
		if *tailFunc == "" {
			fmt.Println("Usage: go run cmd/diagnostics/main.go -cmd tail -function <orchestrator|fetcher|processor|sparkline-poster> [-filter all|errors|success]")
			os.Exit(1)
		}
		tailCloudWatch(*tailFunc, *filter)
	case "all":
		showAllDiagnostics(ctx, stateManager, *limit)
	default:
		showUsage()
		os.Exit(1)
	}
}

func showUsage() {
	fmt.Println("HourStats Diagnostics Tool")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  go run cmd/diagnostics/main.go -cmd <command> [options]")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  status    - Show overall system status (default)")
	fmt.Println("  runs      - Show recent runs")
	fmt.Println("  current   - Show current run state")
	fmt.Println("  errors    - Show all errors")
	fmt.Println("  validate  - Validate run count for last 24 hours")
	fmt.Println("  tail      - Tail CloudWatch logs (requires -function)")
	fmt.Println("  all       - Run all diagnostics")
	fmt.Println("")
	fmt.Println("Options:")
	fmt.Println("  -limit <n>       Number of recent runs to show (default: 10)")
	fmt.Println("  -function <name> Lambda function for tail (orchestrator, fetcher, processor, sparkline-poster)")
	fmt.Println("  -filter <type>   Filter for tail (all, errors, success) (default: all)")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  go run cmd/diagnostics/main.go -cmd status")
	fmt.Println("  go run cmd/diagnostics/main.go -cmd runs -limit 20")
	fmt.Println("  go run cmd/diagnostics/main.go -cmd tail -function orchestrator -filter errors")
}

func showStatus(ctx context.Context, stateManager *state.StateManager, limit int) {
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("📊 HourStats System Status")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()
	
	// Show recent runs
	fmt.Println("📋 Recent Runs:")
	fmt.Println("───────────────────────────────────────────────────────────────")
	showRecentRuns(ctx, stateManager, limit)
	fmt.Println()
	
	// Show current run state
	fmt.Println("🔄 Current Run State:")
	fmt.Println("───────────────────────────────────────────────────────────────")
	showCurrentRunState(ctx, stateManager)
	fmt.Println()
	
	// Validate run count
	fmt.Println("✅ Run Count Validation (Last 24 Hours):")
	fmt.Println("───────────────────────────────────────────────────────────────")
	validateRunCount(ctx, stateManager)
	fmt.Println()
	
	// Show errors summary
	fmt.Println("⚠️  Recent Errors:")
	fmt.Println("───────────────────────────────────────────────────────────────")
	detectErrors(ctx, stateManager, 5)
}

func showRecentRuns(ctx context.Context, stateManager *state.StateManager, limit int) {
	runIDs, err := stateManager.ListRuns(ctx, int32(limit*2)) // Get more to sort by time
	if err != nil {
		fmt.Printf("❌ Failed to list runs: %v\n", err)
		return
	}

	if len(runIDs) == 0 {
		fmt.Println("No runs found.")
		return
	}

	// Get stats for all runs and sort by creation time
	type runInfo struct {
		runID   string
		stats   *state.RunStats
		created time.Time
	}

	var runs []runInfo
	for _, runID := range runIDs {
		stats, err := stateManager.GetRunStats(ctx, runID)
		if err != nil {
			continue
		}
		runs = append(runs, runInfo{
			runID:   runID,
			stats:   stats,
			created: stats.CreatedAt,
		})
	}

	// Sort by creation time (most recent first)
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].created.After(runs[j].created)
	})

	// Limit to requested number
	if len(runs) > limit {
		runs = runs[:limit]
	}

	// Print header
	fmt.Printf("%-30s %-12s %-12s %-8s %-12s %-20s\n",
		"Run ID", "Status", "Step", "Posts", "Sentiment", "Created")
	fmt.Println(strings.Repeat("-", 100))

	// Print runs
	for i, run := range runs {
		statusIcon := getStatusIcon(run.stats.Status)
		sentiment := run.stats.OverallSentiment
		if sentiment == "" {
			sentiment = "N/A"
		}
		createdStr := run.created.Local().Format("2006-01-02 15:04:05")
		
		fmt.Printf("%-30s %s %-11s %-12s %-8d %-12s %-20s\n",
			truncate(run.runID, 30),
			statusIcon,
			run.stats.Status,
			run.stats.Step,
			run.stats.TotalPostsRetrieved,
			sentiment,
			createdStr)
		
		if i < len(runs)-1 && i%5 == 4 {
			fmt.Println() // Add spacing every 5 runs
		}
	}
}

func showCurrentRunState(ctx context.Context, stateManager *state.StateManager) {
	// Get most recent run
	runIDs, err := stateManager.ListRuns(ctx, 1)
	if err != nil || len(runIDs) == 0 {
		fmt.Println("❌ No runs found.")
		return
	}

	runID := runIDs[0]
	
	// Get stats for overview
	stats, err := stateManager.GetRunStats(ctx, runID)
	if err != nil {
		fmt.Printf("❌ Failed to get run stats: %v\n", err)
		return
	}

	fmt.Printf("Run ID: %s\n", runID)
	fmt.Printf("Created: %s\n", stats.CreatedAt.Local().Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("Updated: %s\n", stats.UpdatedAt.Local().Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("Time Range: %s to %s\n",
		stats.CutoffTime.Local().Format("2006-01-02 15:04:05 MST"),
		time.Now().Local().Format("2006-01-02 15:04:05 MST"))
	fmt.Println()

	// Check each step
	steps := []string{"orchestrator", "fetcher", "processor", "aggregator", "analyzer"}
	
	fmt.Println("Step Status:")
	fmt.Println("───────────────────────────────────────────────────────────────")
	for _, step := range steps {
		runState, err := stateManager.GetRun(ctx, runID, step)
		if err != nil {
			fmt.Printf("  %-15s %s Not found\n", step+":", "❌")
			continue
		}

		statusIcon := getStatusIcon(runState.Status)
		fmt.Printf("  %-15s %s %s", step+":", statusIcon, runState.Status)
		
		if runState.ErrorMessage != "" {
			fmt.Printf(" - Error: %s", truncate(runState.ErrorMessage, 50))
		}
		if runState.TotalPostsRetrieved > 0 {
			fmt.Printf(" (%d posts)", runState.TotalPostsRetrieved)
		}
		fmt.Println()
	}

	fmt.Println()
	fmt.Printf("Overall Status: %s %s\n", getStatusIcon(stats.Status), stats.Status)
	if stats.OverallSentiment != "" {
		fmt.Printf("Sentiment: %s\n", stats.OverallSentiment)
	}
	fmt.Printf("Posts Retrieved: %d\n", stats.TotalPostsRetrieved)
	fmt.Printf("Top Posts: %d\n", stats.TopPostsCount)
}

func detectErrors(ctx context.Context, stateManager *state.StateManager, limit int) {
	runIDs, err := stateManager.ListRuns(ctx, int32(limit*2))
	if err != nil {
		fmt.Printf("❌ Failed to list runs: %v\n", err)
		return
	}

	type errorInfo struct {
		runID      string
		step       string
		message    string
		errorTime  time.Time
		createdAt  time.Time
	}

	var errors []errorInfo
	steps := []string{"orchestrator", "fetcher", "processor", "aggregator", "analyzer"}

	for _, runID := range runIDs {
		for _, step := range steps {
			runState, err := stateManager.GetRun(ctx, runID, step)
			if err != nil {
				continue
			}

			if runState.ErrorMessage != "" {
				errors = append(errors, errorInfo{
					runID:     runID,
					step:      step,
					message:   runState.ErrorMessage,
					errorTime: runState.LastErrorTime,
					createdAt: runState.CreatedAt,
				})
			}
		}
	}

	if len(errors) == 0 {
		fmt.Println("✅ No errors found in recent runs.")
		return
	}

	// Sort by error time (most recent first)
	sort.Slice(errors, func(i, j int) bool {
		if errors[i].errorTime.IsZero() && errors[j].errorTime.IsZero() {
			return errors[i].createdAt.After(errors[j].createdAt)
		}
		if errors[i].errorTime.IsZero() {
			return false
		}
		if errors[j].errorTime.IsZero() {
			return true
		}
		return errors[i].errorTime.After(errors[j].errorTime)
	})

	// Limit to requested number
	if len(errors) > limit {
		errors = errors[:limit]
	}

	fmt.Printf("Found %d error(s):\n\n", len(errors))
	for i, errInfo := range errors {
		fmt.Printf("%d. Run: %s\n", i+1, truncate(errInfo.runID, 40))
		fmt.Printf("   Step: %s\n", errInfo.step)
		fmt.Printf("   Error: %s\n", errInfo.message)
		if !errInfo.errorTime.IsZero() {
			fmt.Printf("   Time: %s\n", errInfo.errorTime.Local().Format("2006-01-02 15:04:05 MST"))
		}
		fmt.Println()
	}
}

func validateRunCount(ctx context.Context, stateManager *state.StateManager) {
	// Get all runs from last 24 hours
	twentyFourHoursAgo := time.Now().Add(-24 * time.Hour)
	
	runIDs, err := stateManager.ListRuns(ctx, 100) // Get enough to check 24 hours
	if err != nil {
		fmt.Printf("❌ Failed to list runs: %v\n", err)
		return
	}

	// Filter runs from last 24 hours
	var recentRuns []string
	var runTimes []time.Time
	for _, runID := range runIDs {
		stats, err := stateManager.GetRunStats(ctx, runID)
		if err != nil {
			continue
		}
		if stats.CreatedAt.After(twentyFourHoursAgo) {
			recentRuns = append(recentRuns, runID)
			runTimes = append(runTimes, stats.CreatedAt)
		}
	}

	actualCount := len(recentRuns)
	expectedCount := expectedRunsPer24Hours
	
	fmt.Printf("Expected runs (last 24h): %d\n", expectedCount)
	fmt.Printf("Actual runs (last 24h):   %d\n", actualCount)
	
	if actualCount >= expectedCount {
		fmt.Printf("✅ Status: PASS (sufficient runs)\n")
	} else {
		missing := expectedCount - actualCount
		fmt.Printf("⚠️  Status: WARNING (%d missing runs)\n", missing)
	}

	// Calculate time gaps
	if len(runTimes) > 1 {
		sort.Slice(runTimes, func(i, j int) bool {
			return runTimes[i].Before(runTimes[j])
		})
		
		fmt.Println()
		fmt.Println("Time gaps between runs:")
		var maxGap time.Duration
		var maxGapStart time.Time
		for i := 1; i < len(runTimes); i++ {
			gap := runTimes[i].Sub(runTimes[i-1])
			if gap > maxGap {
				maxGap = gap
				maxGapStart = runTimes[i-1]
			}
			
			if gap > 35*time.Minute { // More than 5 minutes over expected 30 min
				fmt.Printf("  ⚠️  %s - Gap: %s (between %s and %s)\n",
					getGapSeverity(gap),
					gap.Round(time.Minute),
					runTimes[i-1].Local().Format("15:04:05"),
					runTimes[i].Local().Format("15:04:05"))
			}
		}
		
		if maxGap > 35*time.Minute {
			fmt.Printf("\n  Largest gap: %s (starting at %s)\n",
				maxGap.Round(time.Minute),
				maxGapStart.Local().Format("2006-01-02 15:04:05 MST"))
		}
	}
}

func tailCloudWatch(functionName, filter string) {
	logGroup := fmt.Sprintf("/aws/lambda/hourstats-%s", functionName)
	
	// Validate function name
	validFunctions := map[string]bool{
		"orchestrator":     true,
		"fetcher":          true,
		"processor":        true,
		"sparkline-poster": true,
	}
	
	if !validFunctions[functionName] {
		fmt.Printf("❌ Invalid function name: %s\n", functionName)
		fmt.Println("Valid functions: orchestrator, fetcher, processor, sparkline-poster")
		os.Exit(1)
	}

	// Build AWS CLI command
	args := []string{
		"logs", "tail", logGroup,
		"--follow",
		"--format", "short",
		"--region", region,
	}

	// Add filter if specified
	if filter == "errors" {
		args = append(args, "--filter-pattern", "ERROR OR Failed OR error OR timeout")
	} else if filter == "success" {
		args = append(args, "--filter-pattern", "Successfully OR Posted OR Completed")
	}

	fmt.Printf("Tailing CloudWatch logs for: %s\n", logGroup)
	fmt.Printf("Filter: %s\n", filter)
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("───────────────────────────────────────────────────────────────")
	
	cmd := exec.Command("aws", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		fmt.Printf("\n❌ Error tailing logs: %v\n", err)
		os.Exit(1)
	}
}

func showAllDiagnostics(ctx context.Context, stateManager *state.StateManager, limit int) {
	showStatus(ctx, stateManager, limit)
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("For detailed CloudWatch logs, use:")
	fmt.Println("  go run cmd/diagnostics/main.go -cmd tail -function <name>")
	fmt.Println("═══════════════════════════════════════════════════════════════")
}

// Helper functions

func getStatusIcon(status string) string {
	switch strings.ToLower(status) {
	case "completed", "success", "posted":
		return "✅"
	case "failed", "error":
		return "❌"
	case "in-progress", "processing", "fetching":
		return "🔄"
	default:
		return "⏳"
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func getGapSeverity(gap time.Duration) string {
	if gap > 2*time.Hour {
		return "CRITICAL"
	} else if gap > 1*time.Hour {
		return "HIGH"
	} else if gap > 45*time.Minute {
		return "MEDIUM"
	}
	return "LOW"
}

