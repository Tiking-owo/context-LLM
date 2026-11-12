package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"context-LLM/internal/config"
)

const checkpointVersion = 1

type Checkpoint struct {
	Version         int     `json:"version"`
	Complete        bool    `json:"complete"`
	ConfigSignature string  `json:"config_signature"`
	Phase           string  `json:"phase"`
	TierIndex       int     `json:"tier_index"`
	LastPassed      int     `json:"last_passed"`
	BinaryLow       int     `json:"binary_low"`
	BinaryHigh      int     `json:"binary_high"`
	ProbeHigh       int     `json:"probe_high"`
	ProbeCount      int     `json:"probe_count"`
	Trials          []Trial `json:"trials"`
}

func ConfigSignature(cfg config.Config) string {
	snapshot := struct {
		BaseURL               string   `json:"base_url"`
		Model                 string   `json:"model"`
		APIMode               string   `json:"api_mode"`
		ReasoningEffort       string   `json:"reasoning_effort"`
		ReasoningSummary      string   `json:"reasoning_summary"`
		Tiers                 []int    `json:"tiers"`
		CharsPerToken         float64  `json:"chars_per_token"`
		MaxOutputTokens       int      `json:"max_output_tokens"`
		TimeoutSeconds        int      `json:"timeout_seconds"`
		TransportRetries      int      `json:"transport_retries"`
		AutoCalibrate         bool     `json:"auto_calibrate"`
		EnhancedAutoTest      bool     `json:"enhanced_auto_test"`
		BinaryPrecisionTokens int      `json:"binary_precision_tokens"`
		ParallelTests         bool     `json:"parallel_tests"`
		RequestIntervalMS     int      `json:"request_interval_ms"`
		AnchorCount           int      `json:"anchor_count"`
		EvidenceTolerance     int      `json:"evidence_tolerance"`
		TestModes             []string `json:"test_modes"`
		ReportLanguage        string   `json:"report_language"`
		PreviewAfterRun       bool     `json:"preview_after_run"`
	}{
		BaseURL:               cfg.BaseURL,
		Model:                 cfg.Model,
		APIMode:               cfg.APIMode,
		ReasoningEffort:       cfg.ReasoningEffort,
		ReasoningSummary:      cfg.ReasoningSummary,
		Tiers:                 append([]int(nil), cfg.Tiers...),
		CharsPerToken:         cfg.CharsPerToken,
		MaxOutputTokens:       cfg.MaxOutputTokens,
		TimeoutSeconds:        int(cfg.Timeout.Seconds()),
		TransportRetries:      cfg.TransportRetries,
		AutoCalibrate:         cfg.AutoCalibrate,
		EnhancedAutoTest:      cfg.EnhancedAutoTest,
		BinaryPrecisionTokens: cfg.BinaryPrecisionTokens,
		ParallelTests:         cfg.ParallelTests,
		RequestIntervalMS:     cfg.RequestIntervalMS,
		AnchorCount:           cfg.AnchorCount,
		EvidenceTolerance:     cfg.EvidenceTolerance,
		TestModes:             append([]string(nil), cfg.TestModes...),
		ReportLanguage:        cfg.ReportLanguage,
		PreviewAfterRun:       cfg.PreviewAfterRun,
	}
	data, _ := json.Marshal(snapshot)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func LoadCheckpoint(path string, cfg config.Config) (Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, err
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("invalid checkpoint %s: %w", path, err)
	}
	if checkpoint.Version != checkpointVersion {
		return Checkpoint{}, fmt.Errorf("unsupported checkpoint version %d", checkpoint.Version)
	}
	if checkpoint.ConfigSignature != ConfigSignature(cfg) {
		return Checkpoint{}, errors.New("checkpoint configuration does not match current config")
	}
	return checkpoint, nil
}

func SaveCheckpoint(path string, checkpoint Checkpoint) error {
	checkpoint.Version = checkpointVersion
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func FindLatestCheckpoint(runsDir string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		path := filepath.Join(runsDir, name, "checkpoint.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var checkpoint Checkpoint
		if err := json.Unmarshal(data, &checkpoint); err != nil {
			continue
		}
		if !checkpoint.Complete {
			return path, nil
		}
	}
	return "", errors.New("no incomplete checkpoint found")
}
