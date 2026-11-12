package runner

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"context-LLM/internal/config"
	"context-LLM/internal/corpus"
	"context-LLM/internal/provider"
	"context-LLM/internal/scoring"
)

type Calibration struct {
	TargetPromptTokens       int     `json:"target_prompt_tokens"`
	ActualPromptTokens       int     `json:"actual_prompt_tokens"`
	CharsPerTokenUsed        float64 `json:"chars_per_token_used"`
	RecommendedCharsPerToken float64 `json:"recommended_chars_per_token"`
	DifferencePercent        float64 `json:"difference_percent"`
}

type Trial struct {
	Tier                    int              `json:"tier"`
	Label                   string           `json:"label"`
	Seed                    int64            `json:"seed"`
	Corpus                  string           `json:"corpus"`
	ResponseFile            string           `json:"response_file"`
	Attempts                int              `json:"attempts"`
	MaxOutputTokensUsed     int              `json:"max_output_tokens_used"`
	OutputRetries           int              `json:"output_retries,omitempty"`
	CharsPerTokenUsed       float64          `json:"chars_per_token_used"`
	PromptTokens            *int             `json:"prompt_tokens,omitempty"`
	CompletionTokens        *int             `json:"completion_tokens,omitempty"`
	ElapsedSeconds          float64          `json:"elapsed_seconds"`
	ContextTooLarge         bool             `json:"context_too_large"`
	HTTPStatus              int              `json:"http_status"`
	TransportError          string           `json:"transport_error,omitempty"`
	Score                   scoring.Score    `json:"score"`
	ResponseMetadata        map[string]any   `json:"response_metadata,omitempty"`
	ResponseAttemptMetadata []map[string]any `json:"response_attempt_metadata,omitempty"`
	Calibration             *Calibration     `json:"calibration,omitempty"`
	TestFilesCleaned        bool             `json:"test_files_cleaned,omitempty"`
}

type Runner struct {
	Config          config.Config
	Root            string
	Out             io.Writer
	seenSeeds       map[int64]bool
	seedMu          sync.Mutex
	requestMu       sync.Mutex
	nextRequestAt   time.Time
	logMu           sync.Mutex
	checkpointMu    sync.Mutex
	checkpointPath  string
	configSignature string
}

func New(cfg config.Config, root string, out io.Writer) *Runner {
	return &Runner{Config: cfg, Root: root, Out: out, seenSeeds: map[int64]bool{}, configSignature: ConfigSignature(cfg)}
}

func (runner *Runner) SetCheckpointPath(path string) {
	runner.checkpointPath = path
}

func (runner *Runner) newCheckpoint(phase string) Checkpoint {
	return Checkpoint{
		Version:         checkpointVersion,
		ConfigSignature: runner.configSignature,
		Phase:           phase,
	}
}

func (runner *Runner) saveCheckpoint(checkpoint Checkpoint) error {
	if runner.checkpointPath == "" {
		return nil
	}
	checkpoint.ConfigSignature = runner.configSignature
	return SaveCheckpoint(runner.checkpointPath, checkpoint)
}

func trialKey(tier int, label string) string {
	return fmt.Sprintf("%d|%s", tier, label)
}

func findTrial(trials []Trial, tier int, label string) (Trial, bool) {
	key := trialKey(tier, label)
	for _, trial := range trials {
		if trialKey(trial.Tier, trial.Label) == key {
			return trial, true
		}
	}
	return Trial{}, false
}

func (runner *Runner) resumeTrial(state *Checkpoint, tier int, label string) (Trial, error) {
	runner.checkpointMu.Lock()
	if trial, ok := findTrial(state.Trials, tier, label); ok {
		runner.checkpointMu.Unlock()
		fmt.Fprintf(runner.Out, "Reusing completed trial %s …\n", label)
		return trial, nil
	}
	runner.checkpointMu.Unlock()

	trial, err := runner.runTrial(tier, label)
	if err != nil {
		return Trial{}, err
	}

	runner.checkpointMu.Lock()
	defer runner.checkpointMu.Unlock()
	if existing, ok := findTrial(state.Trials, tier, label); ok {
		return existing, nil
	}
	state.Trials = append(state.Trials, trial)
	if err := runner.saveCheckpoint(*state); err != nil {
		return Trial{}, err
	}
	return trial, nil
}

func (runner *Runner) Run() ([]Trial, error) {
	var checkpoint *Checkpoint
	if runner.checkpointPath != "" {
		if _, err := os.Stat(runner.checkpointPath); err == nil {
			loaded, loadErr := LoadCheckpoint(runner.checkpointPath, runner.Config)
			if loadErr != nil {
				return nil, loadErr
			}
			checkpoint = &loaded
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if checkpoint != nil && checkpoint.Complete {
		return checkpoint.Trials, nil
	}
	if runner.Config.EnhancedAutoTest {
		return runner.runEnhanced(checkpoint)
	}
	return runner.runNormal(checkpoint)
}

func (runner *Runner) runNormal(checkpoint *Checkpoint) ([]Trial, error) {
	trials := []Trial{}
	startIndex := 0
	state := runner.newCheckpoint("configured")
	if checkpoint != nil {
		if checkpoint.Phase != "configured" {
			return nil, fmt.Errorf("cannot resume normal mode from phase %q", checkpoint.Phase)
		}
		trials = checkpoint.Trials
		startIndex = checkpoint.TierIndex
		state = *checkpoint
	}
	if checkpoint == nil {
		if err := runner.saveCheckpoint(state); err != nil {
			return trials, err
		}
	}
	for index := startIndex; index < len(runner.Config.Tiers); index++ {
		tier := runner.Config.Tiers[index]
		fmt.Fprintf(runner.Out, "Testing %s context tier …\n", comma(tier))
		primary, err := runner.resumeTrial(&state, tier, "primary")
		if err != nil {
			return trials, err
		}
		trials = state.Trials
		runner.applyCalibration(primary)

		if primary.ContextTooLarge {
			if index == 0 {
				fmt.Fprintln(runner.Out, "The first configured tier exceeds the model context window.")
				state.Trials = trials
				state.Phase = "done"
				state.Complete = true
				if err := runner.saveCheckpoint(state); err != nil {
					return trials, err
				}
				break
			}
			previous := runner.Config.Tiers[index-1]
			fmt.Fprintf(runner.Out, "Context limit reached. Confirming %s with a new question set …\n", comma(previous))
			confirmation, err := runner.resumeTrial(&state, previous, "confirmation_new_questions")
			if err != nil {
				return trials, err
			}
			trials = state.Trials
			runner.applyCalibration(confirmation)
			state.Trials = trials
			state.Phase = "done"
			state.Complete = true
			if err := runner.saveCheckpoint(state); err != nil {
				return trials, err
			}
			break
		}
		if primary.Score.State != "passed" {
			fmt.Fprintln(runner.Out, "Stopping: the model accepted this size but did not pass the benchmark.")
			state.Trials = trials
			state.Phase = "done"
			state.Complete = true
			if err := runner.saveCheckpoint(state); err != nil {
				return trials, err
			}
			break
		}
		state.TierIndex = index + 1
		state.LastPassed = tier
		state.Trials = trials
		if err := runner.saveCheckpoint(state); err != nil {
			return trials, err
		}
	}
	if !state.Complete {
		state.Trials = trials
		state.Phase = "done"
		state.Complete = true
		if err := runner.saveCheckpoint(state); err != nil {
			return trials, err
		}
	}
	return trials, nil
}

type tierOutcome struct {
	trials          []Trial
	passed          bool
	contextTooLarge bool
}

func (runner *Runner) runEnhanced(checkpoint *Checkpoint) ([]Trial, error) {
	state := runner.newCheckpoint("configured")
	if checkpoint != nil {
		switch checkpoint.Phase {
		case "configured", "probe", "binary", "done":
		default:
			return nil, fmt.Errorf("cannot resume enhanced mode from phase %q", checkpoint.Phase)
		}
		state = *checkpoint
		if state.Complete {
			return state.Trials, nil
		}
	}
	if state.Phase == "done" {
		state.Complete = true
		if err := runner.saveCheckpoint(state); err != nil {
			return state.Trials, err
		}
		return state.Trials, nil
	}
	if state.Phase == "probe" {
		return runner.runProbe(&state)
	}
	if state.Phase == "binary" {
		return runner.runBinarySearch(&state)
	}
	if checkpoint == nil {
		if err := runner.saveCheckpoint(state); err != nil {
			return state.Trials, err
		}
	}

	for index := state.TierIndex; index < len(runner.Config.Tiers); index++ {
		tier := runner.Config.Tiers[index]
		fmt.Fprintf(runner.Out, "Enhanced test: %s context tier, 3 independent question sets …\n", comma(tier))
		outcome, err := runner.runTier(tier, "primary", &state)
		if err != nil {
			return state.Trials, err
		}
		runner.applyTrialSetCalibration(tier, outcome.trials)
		if !outcome.passed {
			if state.LastPassed == 0 {
				fmt.Fprintln(runner.Out, "The first configured tier did not pass; no lower passing bound is available.")
				state.Phase = "done"
				state.Complete = true
				if err := runner.saveCheckpoint(state); err != nil {
					return state.Trials, err
				}
				return state.Trials, nil
			}
			state.Phase = "binary"
			state.BinaryLow = state.LastPassed
			state.BinaryHigh = tier
			if err := runner.saveCheckpoint(state); err != nil {
				return state.Trials, err
			}
			return runner.runBinarySearch(&state)
		}
		state.TierIndex = index + 1
		state.LastPassed = tier
		if err := runner.saveCheckpoint(state); err != nil {
			return state.Trials, err
		}
	}

	// No configured tier failed, so probe upward before binary-searching the bound.
	state.Phase = "probe"
	state.ProbeHigh = state.LastPassed * 2
	state.ProbeCount = 0
	if err := runner.saveCheckpoint(state); err != nil {
		return state.Trials, err
	}
	return runner.runProbe(&state)
}

func (runner *Runner) runProbe(state *Checkpoint) ([]Trial, error) {
	for state.ProbeCount < 4 && state.ProbeHigh > state.LastPassed {
		fmt.Fprintf(runner.Out, "Enhanced probe: %s context tier, 3 independent question sets …\n", comma(state.ProbeHigh))
		outcome, err := runner.runTier(state.ProbeHigh, fmt.Sprintf("probe_%d_%d", state.ProbeHigh, state.ProbeCount+1), state)
		if err != nil {
			return state.Trials, err
		}
		runner.applyTrialSetCalibration(state.ProbeHigh, outcome.trials)
		if !outcome.passed {
			state.Phase = "binary"
			state.BinaryLow = state.LastPassed
			state.BinaryHigh = state.ProbeHigh
			if err := runner.saveCheckpoint(*state); err != nil {
				return state.Trials, err
			}
			return runner.runBinarySearch(state)
		}
		state.LastPassed = state.ProbeHigh
		state.ProbeCount++
		state.ProbeHigh *= 2
		if err := runner.saveCheckpoint(*state); err != nil {
			return state.Trials, err
		}
	}
	fmt.Fprintln(runner.Out, "No context limit was reached within the configured probe range.")
	state.Phase = "done"
	state.Complete = true
	if err := runner.saveCheckpoint(*state); err != nil {
		return state.Trials, err
	}
	return state.Trials, nil
}

func (runner *Runner) runBinarySearch(state *Checkpoint) ([]Trial, error) {
	precision := runner.Config.BinaryPrecisionTokens
	if precision <= 0 {
		precision = 10000
	}
	fmt.Fprintf(runner.Out, "Binary search between %s and %s tokens (precision %d) …\n", comma(state.BinaryLow), comma(state.BinaryHigh), precision)
	for state.BinaryHigh-state.BinaryLow > precision {
		mid := ((state.BinaryLow + state.BinaryHigh) / 2 / precision) * precision
		if mid <= state.BinaryLow || mid >= state.BinaryHigh {
			break
		}
		fmt.Fprintf(runner.Out, "Binary candidate: %s context tier, 3 independent question sets …\n", comma(mid))
		outcome, err := runner.runTier(mid, fmt.Sprintf("binary_%d_%d", state.BinaryLow, state.BinaryHigh), state)
		if err != nil {
			return state.Trials, err
		}
		runner.applyTrialSetCalibration(mid, outcome.trials)
		if outcome.passed {
			state.BinaryLow = mid
		} else {
			state.BinaryHigh = mid
		}
		if err := runner.saveCheckpoint(*state); err != nil {
			return state.Trials, err
		}
	}
	fmt.Fprintf(runner.Out, "Estimated usable context upper bound: %s tokens (between %s and %s).\n", comma(state.BinaryLow), comma(state.BinaryLow), comma(state.BinaryHigh))
	state.Phase = "done"
	state.Complete = true
	if err := runner.saveCheckpoint(*state); err != nil {
		return state.Trials, err
	}
	return state.Trials, nil
}

func (runner *Runner) runTier(tier int, label string, state *Checkpoint) (tierOutcome, error) {
	if runner.Config.ParallelTests {
		return runner.runTierParallel(tier, label, state)
	}
	outcome := tierOutcome{passed: true}
	for repetition := 1; repetition <= 3; repetition++ {
		trialLabel := fmt.Sprintf("%s_%d", label, repetition)
		trial, err := runner.resumeTrial(state, tier, trialLabel)
		if err != nil {
			return outcome, err
		}
		outcome.trials = append(outcome.trials, trial)
		if trial.ContextTooLarge {
			outcome.contextTooLarge = true
		}
		if trial.Score.State != "passed" {
			outcome.passed = false
		}
	}
	return outcome, nil
}

func (runner *Runner) runTierParallel(tier int, label string, state *Checkpoint) (tierOutcome, error) {
	outcome := tierOutcome{passed: true}
	trials := make([]Trial, 3)
	errors := make([]error, 3)
	var wait sync.WaitGroup
	for index := 0; index < 3; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			trial, err := runner.resumeTrial(state, tier, fmt.Sprintf("%s_%d", label, index+1))
			trials[index] = trial
			errors[index] = err
		}(index)
	}
	wait.Wait()
	for _, err := range errors {
		if err != nil {
			return outcome, err
		}
	}
	for _, trial := range trials {
		outcome.trials = append(outcome.trials, trial)
		if trial.ContextTooLarge {
			outcome.contextTooLarge = true
		}
		if trial.Score.State != "passed" {
			outcome.passed = false
		}
	}
	return outcome, nil
}

func (runner *Runner) runTrial(tier int, label string) (Trial, error) {
	seed, err := runner.nextSeed()
	if err != nil {
		return Trial{}, err
	}
	charsUsed := runner.Config.CharsPerToken
	corpusPath := filepath.Join(runner.Root, fmt.Sprintf("%dk_%s", tier/1000, label))
	manifest, err := corpus.Generate(corpusPath, tier, seed, charsUsed, runner.Config.AnchorCount, runner.Config.TestModes)
	if err != nil {
		return Trial{}, err
	}
	material, err := os.ReadFile(filepath.Join(corpusPath, manifest.File))
	if err != nil {
		return Trial{}, err
	}
	questions, err := os.ReadFile(filepath.Join(corpusPath, "questions.json"))
	if err != nil {
		return Trial{}, err
	}

	var result provider.Result
	attempts := 0
	outputLimit := OutputLimitFor(runner.Config.AnchorCount, runner.Config.MaxOutputTokens)
	outputCap := outputAutoCap(runner.Config.MaxOutputTokens)
	outputRetries := 0
	responseFile := ""
	attemptMetadata := []map[string]any{}
	for attempts = 1; attempts <= runner.Config.TransportRetries+1; attempts++ {
		for {
			requestConfig := runner.Config
			requestConfig.MaxOutputTokens = outputLimit
			runner.throttle()
			started := time.Now()
			result = provider.Send(requestConfig, string(material), string(questions))
			result.Raw["_benchmark"] = manifest
			result.Raw["_request"] = map[string]string{
				"base_url":          runner.Config.BaseURL,
				"api_mode":          runner.Config.APIMode,
				"model":             runner.Config.Model,
				"reasoning_effort":  runner.Config.ReasoningEffort,
				"reasoning_summary": runner.Config.ReasoningSummary,
			}
			result.Raw["_extracted_text"] = result.Text
			result.Raw["_elapsed_seconds"] = math.Round(float64(time.Since(started).Milliseconds())/1000*1000) / 1000
			result.Raw["_attempt"] = attempts
			result.Raw["_max_output_tokens"] = outputLimit
			result.Raw["_retryable"] = result.Retryable
			result.Raw["_retry_after_ms"] = result.RetryAfter.Milliseconds()
			result.Raw["_retry_class"] = result.RetryClass
			result.Raw["_structured_output"] = result.StructuredOutput
			result.Raw["_structured_output_fallback"] = result.StructuredOutputFallback
			suffix := ""
			if outputRetries > 0 {
				suffix = fmt.Sprintf("_output_%d", outputRetries)
			}
			responseFile = filepath.Join(corpusPath, fmt.Sprintf("response_attempt_%d%s.json", attempts, suffix))
			result.Raw["_response_file"] = filepath.Base(responseFile)
			if err := writeJSON(responseFile, result.Raw); err != nil {
				return Trial{}, err
			}
			attemptMetadata = append(attemptMetadata, provider.Metadata(result.Raw))
			if result.FinishReason() == "length" && outputRetries < 1 && outputLimit < outputCap {
				nextLimit := outputLimit * 2
				if nextLimit > outputCap {
					nextLimit = outputCap
				}
				runner.logMu.Lock()
				fmt.Fprintf(runner.Out, "Output limit reached; expanding from %d to %d tokens …\n", outputLimit, nextLimit)
				runner.logMu.Unlock()
				outputLimit = nextLimit
				outputRetries++
				continue
			}
			break
		}
		if !result.Retryable || attempts >= runner.Config.TransportRetries+1 {
			break
		}
		delay := retryDelay(attempts, result)
		fmt.Fprintf(runner.Out, "Retrying %s after %s (attempt %d/%d) …\n", result.RetryClass, delay, attempts+1, runner.Config.TransportRetries+1)
		time.Sleep(delay)
	}

	prompt := result.PromptTokens()
	completion := result.CompletionTokens()
	status := result.HTTPStatus
	if status == 0 {
		status = 200
	}
	trial := Trial{
		Tier:                    tier,
		Label:                   label,
		Seed:                    seed,
		Corpus:                  corpusPath,
		ResponseFile:            responseFile,
		Attempts:                attempts,
		MaxOutputTokensUsed:     outputLimit,
		OutputRetries:           outputRetries,
		CharsPerTokenUsed:       charsUsed,
		PromptTokens:            prompt,
		CompletionTokens:        completion,
		ElapsedSeconds:          floatValue(result.Raw["_elapsed_seconds"]),
		ContextTooLarge:         result.ContextTooLarge(),
		HTTPStatus:              status,
		TransportError:          result.TransportError,
		ResponseMetadata:        provider.Metadata(result.Raw),
		ResponseAttemptMetadata: attemptMetadata,
		Score:                   scoring.Response(corpusPath, result.Raw, runner.Config.EvidenceTolerance),
	}
	trial.Calibration = Calibrate(tier, charsUsed, prompt)
	if err := writeJSON(filepath.Join(corpusPath, "trial_summary.json"), trial); err != nil {
		return Trial{}, err
	}
	return trial, nil
}

func (runner *Runner) applyCalibration(trial Trial) {
	if trial.Calibration == nil {
		return
	}
	fmt.Fprintf(runner.Out, "Actual input: %s tokens; recommended chars_per_token: %.4f\n", comma(trial.Calibration.ActualPromptTokens), trial.Calibration.RecommendedCharsPerToken)
	if runner.Config.AutoCalibrate && !trial.ContextTooLarge {
		runner.Config.CharsPerToken = trial.Calibration.RecommendedCharsPerToken
		fmt.Fprintln(runner.Out, "Using this calibrated value for the following tiers in this run.")
	}
}

func (runner *Runner) applyTrialSetCalibration(tier int, trials []Trial) {
	calibration := CalibrateFromTrials(tier, runner.Config.CharsPerToken, trials)
	if calibration == nil {
		return
	}
	contextTooLarge := false
	for _, trial := range trials {
		if trial.ContextTooLarge {
			contextTooLarge = true
			break
		}
	}
	fmt.Fprintf(runner.Out, "Median input: %s tokens; recommended chars_per_token: %.4f\n", comma(calibration.ActualPromptTokens), calibration.RecommendedCharsPerToken)
	if runner.Config.AutoCalibrate && !contextTooLarge {
		runner.Config.CharsPerToken = calibration.RecommendedCharsPerToken
		fmt.Fprintln(runner.Out, "Using the median calibration for the following tiers in this run.")
	}
}

func Calibrate(tier int, chars float64, actual *int) *Calibration {
	if actual == nil || *actual == 0 {
		return nil
	}
	target := tier - config.HeadroomTokens
	recommended := chars * float64(target) / float64(*actual)
	return &Calibration{
		TargetPromptTokens:       target,
		ActualPromptTokens:       *actual,
		CharsPerTokenUsed:        chars,
		RecommendedCharsPerToken: math.Round(recommended*10000) / 10000,
		DifferencePercent:        math.Round((float64(*actual)/float64(target)-1)*100) / 100,
	}
}

func CalibrateFromTrials(tier int, chars float64, trials []Trial) *Calibration {
	values := []int{}
	for _, trial := range trials {
		if trial.PromptTokens != nil && *trial.PromptTokens > 0 {
			values = append(values, *trial.PromptTokens)
		}
	}
	if len(values) == 0 {
		return nil
	}
	sort.Ints(values)
	median := values[len(values)/2]
	return Calibrate(tier, chars, &median)
}

func retryDelay(attempt int, result provider.Result) time.Duration {
	base := 500 * time.Millisecond
	maximum := 30 * time.Second
	switch result.RetryClass {
	case "model_cooldown":
		base = 15 * time.Second
		maximum = 5 * time.Minute
	case "rate_limit", "server_error", "timeout":
		base = time.Second
	}
	if result.RetryAfter > 0 {
		if result.RetryAfter > maximum {
			return maximum
		}
		return result.RetryAfter
	}
	delay := base
	for index := 1; index < attempt && delay < maximum; index++ {
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func OutputLimitFor(anchorCount, configured int) int {
	estimated := 512 + anchorCount*64
	if estimated < configured {
		return configured
	}
	if estimated > outputAutoCap(configured) {
		return outputAutoCap(configured)
	}
	return estimated
}

func outputAutoCap(configured int) int {
	const automaticMaximum = 16384
	if configured > automaticMaximum {
		return configured
	}
	return automaticMaximum
}

func (runner *Runner) nextSeed() (int64, error) {
	runner.seedMu.Lock()
	defer runner.seedMu.Unlock()
	if runner.seenSeeds == nil {
		runner.seenSeeds = map[int64]bool{}
	}
	for {
		seed, err := randomSeed()
		if err != nil {
			return 0, err
		}
		if !runner.seenSeeds[seed] {
			runner.seenSeeds[seed] = true
			return seed, nil
		}
	}
}

func (runner *Runner) throttle() {
	if runner.Config.RequestIntervalMS <= 0 {
		return
	}
	interval := time.Duration(runner.Config.RequestIntervalMS) * time.Millisecond
	runner.requestMu.Lock()
	now := time.Now()
	start := now
	if runner.nextRequestAt.After(now) {
		start = runner.nextRequestAt
	}
	runner.nextRequestAt = start.Add(interval)
	runner.requestMu.Unlock()
	if delay := time.Until(start); delay > 0 {
		time.Sleep(delay)
	}
}

func CleanGeneratedFiles(root string, trials []Trial) []string {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	deleted := []string{}
	for i := range trials {
		corpusAbs, err := filepath.Abs(trials[i].Corpus)
		if err != nil || filepath.Dir(corpusAbs) != rootAbs {
			continue
		}
		info, err := os.Stat(corpusAbs)
		if err != nil || !info.IsDir() {
			continue
		}
		if err := os.RemoveAll(corpusAbs); err == nil {
			trials[i].TestFilesCleaned = true
			deleted = append(deleted, filepath.Base(corpusAbs))
		}
	}
	return deleted
}

func randomSeed() (int64, error) {
	var raw [8]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(raw[:]) & math.MaxInt64), nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func comma(value int) string {
	return fmt.Sprintf("%d", value)
}

func floatValue(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case int:
		return float64(number)
	default:
		return 0
	}
}
