package runner

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"context-LLM/internal/config"
	"context-LLM/internal/provider"
)

func TestCalibration(t *testing.T) {
	actual := 189294
	result := Calibrate(200000, 6.25, &actual)
	if result == nil || result.RecommendedCharsPerToken != 6.2073 {
		t.Fatalf("unexpected calibration: %#v", result)
	}
}

func TestCalibrateFromTrialsUsesMedian(t *testing.T) {
	trials := []Trial{
		{PromptTokens: intPointer(100)},
		{PromptTokens: intPointer(110)},
		{PromptTokens: intPointer(120)},
	}
	result := CalibrateFromTrials(200000, 6.25, trials)
	if result == nil || result.ActualPromptTokens != 110 {
		t.Fatalf("expected median 110, got %#v", result)
	}
}

func TestNextSeedIsUnique(t *testing.T) {
	runner := New(config.Config{}, t.TempDir(), io.Discard)
	seen := map[int64]bool{}
	for i := 0; i < 100; i++ {
		seed, err := runner.nextSeed()
		if err != nil {
			t.Fatal(err)
		}
		if seen[seed] {
			t.Fatalf("duplicate seed %d", seed)
		}
		seen[seed] = true
	}
}

func TestNextSeedIsSafeAcrossGoroutines(t *testing.T) {
	runner := New(config.Config{}, t.TempDir(), io.Discard)
	seeds := make([]int64, 100)
	var wait sync.WaitGroup
	for index := range seeds {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			seed, err := runner.nextSeed()
			if err != nil {
				t.Errorf("nextSeed: %v", err)
				return
			}
			seeds[index] = seed
		}(index)
	}
	wait.Wait()
	seen := map[int64]bool{}
	for _, seed := range seeds {
		if seen[seed] {
			t.Fatalf("duplicate concurrent seed %d", seed)
		}
		seen[seed] = true
	}
}

func TestRequestThrottleHonorsInterval(t *testing.T) {
	runner := New(config.Config{RequestIntervalMS: 20}, t.TempDir(), io.Discard)
	started := time.Now()
	runner.throttle()
	runner.throttle()
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond {
		t.Fatalf("throttle returned too early: %s", elapsed)
	}
}

func TestRetryDelayUsesBackoffAndRetryAfter(t *testing.T) {
	if got := retryDelay(1, provider.Result{}); got != 500*time.Millisecond {
		t.Fatalf("unexpected first delay: %s", got)
	}
	if got := retryDelay(3, provider.Result{}); got != 2*time.Second {
		t.Fatalf("unexpected third delay: %s", got)
	}
	if got := retryDelay(1, provider.Result{RetryAfter: 4 * time.Second}); got != 4*time.Second {
		t.Fatalf("Retry-After was not honored: %s", got)
	}
	if got := retryDelay(1, provider.Result{RetryAfter: time.Minute}); got != 30*time.Second {
		t.Fatalf("Retry-After was not capped: %s", got)
	}
	if got := retryDelay(1, provider.Result{RetryClass: "rate_limit"}); got != time.Second {
		t.Fatalf("rate-limit delay was not increased: %s", got)
	}
	if got := retryDelay(1, provider.Result{RetryClass: "model_cooldown"}); got != 15*time.Second {
		t.Fatalf("model-cooldown delay was not increased: %s", got)
	}
	if got := retryDelay(1, provider.Result{RetryClass: "model_cooldown", RetryAfter: time.Minute}); got != time.Minute {
		t.Fatalf("model-cooldown Retry-After was not honored: %s", got)
	}
}

func TestResumeTrialReusesExistingResult(t *testing.T) {
	runner := New(config.Config{}, t.TempDir(), io.Discard)
	existing := Trial{Tier: 200000, Label: "primary_1", Seed: 42}
	state := Checkpoint{Trials: []Trial{existing}}
	resumed, err := runner.resumeTrial(&state, 200000, "primary_1")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Seed != existing.Seed || len(state.Trials) != 1 {
		t.Fatalf("existing trial was not reused: %#v", resumed)
	}
}

func TestCheckpointRoundTripAndConfigGuard(t *testing.T) {
	cfg := config.Config{
		BaseURL: "https://example.test/v1",
		Model:   "model-a",
		Tiers:   []int{200000},
	}
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	checkpoint := Checkpoint{Version: checkpointVersion, Phase: "configured", ConfigSignature: ConfigSignature(cfg)}
	if err := SaveCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCheckpoint(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != "configured" || loaded.Complete {
		t.Fatalf("unexpected checkpoint: %#v", loaded)
	}
	changed := cfg
	changed.Model = "model-b"
	if _, err := LoadCheckpoint(path, changed); err == nil {
		t.Fatal("expected changed config to be rejected")
	}
}

func TestFindLatestCheckpointSkipsCompleteRuns(t *testing.T) {
	runsDir := t.TempDir()
	completeDir := filepath.Join(runsDir, "run_a")
	pendingDir := filepath.Join(runsDir, "run_b")
	if err := os.MkdirAll(completeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveCheckpoint(filepath.Join(completeDir, "checkpoint.json"), Checkpoint{Version: checkpointVersion, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err := SaveCheckpoint(filepath.Join(pendingDir, "checkpoint.json"), Checkpoint{Version: checkpointVersion, Phase: "configured"}); err != nil {
		t.Fatal(err)
	}
	found, err := FindLatestCheckpoint(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(found) != pendingDir {
		t.Fatalf("found %q, want %q", found, pendingDir)
	}
}

func intPointer(value int) *int {
	return &value
}

func TestCleanupOnlyRemovesTrialDirectory(t *testing.T) {
	root := t.TempDir()
	corpus := filepath.Join(root, "200k_primary")
	if err := os.Mkdir(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "context-LLM-outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	trials := []Trial{{Corpus: corpus}, {Corpus: outside}}
	deleted := CleanGeneratedFiles(root, trials)
	if len(deleted) != 1 || deleted[0] != "200k_primary" || !trials[0].TestFilesCleaned {
		t.Fatalf("unexpected cleanup: %#v, %#v", deleted, trials)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("cleanup touched outside directory: %v", err)
	}
}

func TestOutputLimitExpandsWithAnchorCount(t *testing.T) {
	if got := OutputLimitFor(13, 2500); got != 2500 {
		t.Fatalf("small anchor set should retain configured minimum, got %d", got)
	}
	if got := OutputLimitFor(50, 2500); got != 512+50*64 {
		t.Fatalf("large anchor set should expand, got %d", got)
	}
	if got := OutputLimitFor(50, 4000); got != 4000 {
		t.Fatalf("configured minimum should win, got %d", got)
	}
}
