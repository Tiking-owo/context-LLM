// context-LLM is a dependency-free long-context benchmark for OpenAI-compatible APIs.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"context-LLM/internal/config"
	"context-LLM/internal/preview"
	"context-LLM/internal/report"
	"context-LLM/internal/runner"
)

func main() {
	configPath := flag.String("config", "", "Optional path to the benchmark configuration file")
	dryRun := flag.Bool("dry-run", false, "Validate configuration without sending requests")
	resume := flag.String("resume", "", "Resume from a run directory, or use 'latest'")
	previewTarget := flag.String("preview", "", "Preview RESULTS.md; use 'latest' or a file path")
	flag.Parse()

	path := *configPath
	if path == "" {
		var err error
		path, err = config.DefaultPath()
		if err != nil {
			fatal("resolve application directory: %v", err)
		}
	}

	if *previewTarget != "" {
		reportPath, err := preview.Resolve(*previewTarget, path)
		if err != nil {
			fatal("resolve report: %v", err)
		}
		if err := preview.Run(reportPath); err != nil {
			fatal("preview report: %v", err)
		}
		return
	}

	cfg, err := config.Load(path, false)
	if errors.Is(err, os.ErrNotExist) {
		if writeErr := config.WriteDefault(path); writeErr != nil {
			fatal("create default configuration: %v", writeErr)
		}
		fmt.Printf("Created default configuration: %s\nEdit it if needed, then run the program again.\n", path)
		return
	}
	if err != nil {
		fatal("Configuration error: %v", err)
	}
	if cfg.APIKey != "" && cfg.APIKey != config.PlaceholderAPIKey {
		fmt.Fprintln(os.Stderr, "Warning: API key is stored in config.txt; keep that file private and do not share it.")
	}
	for _, estimate := range report.EstimateUsage(cfg) {
		fmt.Printf(
			"Estimated usage for %d: input %.1fK, output %.1fK, total %.1fK\n",
			estimate.Tier,
			float64(estimate.InputTokens)/1000,
			float64(estimate.OutputTokens)/1000,
			float64(estimate.TotalTokens)/1000,
		)
	}
	if *dryRun {
		fmt.Println("Configuration valid. No API request was sent.")
		return
	}
	if cfg.APIKey == "" {
		fatal("missing API key: set api_key in config.txt")
	}
	if cfg.APIKey == config.PlaceholderAPIKey {
		fatal("placeholder API key is not valid: replace api_key in config.txt")
	}
	if cfg.EvidenceTolerance > 2 {
		fmt.Printf("Warning: evidence_tolerance=%d weakens evidence validation; values above 2 are not recommended.\n", cfg.EvidenceTolerance)
	}

	var runRoot, checkpointPath string
	if *resume != "" {
		resumeTarget := *resume
		if resumeTarget == "latest" {
			var err error
			resumeTarget, err = runner.FindLatestCheckpoint(filepath.Join(filepath.Dir(path), "runs"))
			if err != nil {
				fatal("find resume checkpoint: %v", err)
			}
		} else if !filepath.IsAbs(resumeTarget) {
			resumeTarget = filepath.Join(filepath.Dir(path), resumeTarget)
		}
		checkpointPath = resumeTarget
		if info, err := os.Stat(checkpointPath); err == nil && info.IsDir() {
			checkpointPath = filepath.Join(checkpointPath, "checkpoint.json")
		}
		runRoot = filepath.Dir(checkpointPath)
		if _, err := os.Stat(checkpointPath); err != nil {
			fatal("resume checkpoint not found: %v", err)
		}
	} else {
		runRoot = filepath.Join(filepath.Dir(path), "runs", "run_"+time.Now().Format("20060102_150405"))
		checkpointPath = filepath.Join(runRoot, "checkpoint.json")
	}
	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		fatal("create run directory: %v", err)
	}
	if *resume == "" {
		if err := report.WriteJSON(filepath.Join(runRoot, "run_config.public.json"), report.PublicConfig(cfg)); err != nil {
			fatal("write public configuration: %v", err)
		}
	}

	benchmarkRunner := runner.New(cfg, runRoot, os.Stdout)
	benchmarkRunner.SetCheckpointPath(checkpointPath)
	trials, err := benchmarkRunner.Run()
	if err != nil {
		fatal("run benchmark: %v", err)
	}
	if *resume != "" {
		if err := report.WriteJSON(filepath.Join(runRoot, "run_config.public.json"), report.PublicConfig(cfg)); err != nil {
			fatal("write public configuration: %v", err)
		}
	}
	if err := report.WriteSummary(runRoot, cfg, trials); err != nil {
		fatal("write benchmark summary: %v", err)
	}
	deleted := runner.CleanGeneratedFiles(runRoot, trials)
	if err := report.WriteSummary(runRoot, cfg, trials); err != nil {
		fatal("finalize benchmark summary: %v", err)
	}
	if len(deleted) > 0 {
		fmt.Printf("Cleaned generated test files: %s\n", strings.Join(deleted, ", "))
	}
	resultsPath := filepath.Join(runRoot, "RESULTS.md")
	fmt.Printf("Finished. Results: %s\n", resultsPath)
	if cfg.PreviewAfterRun {
		fmt.Println("Auto-preview is enabled.")
		if err := preview.Run(resultsPath); err != nil {
			fatal("preview report: %v", err)
		}
	}
}

func fatal(format string, values ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", values...)
	os.Exit(1)
}
