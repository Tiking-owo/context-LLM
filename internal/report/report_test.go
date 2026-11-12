package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"context-LLM/internal/config"
	"context-LLM/internal/runner"
	"context-LLM/internal/scoring"
)

func TestPublicConfigExcludesAPIKey(t *testing.T) {
	cfg := config.Config{APIKey: "secret", Model: "model", TestModes: []string{"needle"}}
	values := PublicConfig(cfg)
	if _, exists := values["api_key"]; exists {
		t.Fatal("public config contains api_key")
	}
	if values["model"] != "model" {
		t.Fatalf("unexpected model: %#v", values["model"])
	}
}

func TestMarkdownIncludesEnabledModes(t *testing.T) {
	cfg := config.Config{TestModes: []string{"needle", "conflict"}}
	text := Markdown(cfg, nil, "/tmp/run")
	if !strings.Contains(text, "Test modes: `needle,conflict`") {
		t.Fatalf("missing test modes from report:\n%s", text)
	}
}

func TestMarkdownIncludesModeBreakdown(t *testing.T) {
	cfg := config.Config{TestModes: []string{"needle"}}
	trial := runner.Trial{
		Tier:  200000,
		Label: "primary",
		Score: scoring.Score{
			ByMode: map[string]scoring.ModeScore{
				"needle": {AnswerHits: 1, EvidenceHits: 1, QuestionCount: 1},
			},
		},
	}
	text := Markdown(cfg, []runner.Trial{trial}, "/tmp/run")
	if !strings.Contains(text, "| 200000 | primary | needle | 1/1 | 1/1 |") {
		t.Fatalf("missing per-mode breakdown:\n%s", text)
	}
}

func TestMarkdownUsesMedianCalibration(t *testing.T) {
	cfg := config.Config{TestModes: []string{"needle"}, EnhancedAutoTest: true}
	one, two, three := 100, 110, 120
	trials := []runner.Trial{
		{Tier: 200000, Label: "primary_1", CharsPerTokenUsed: 6.25, PromptTokens: &one},
		{Tier: 200000, Label: "primary_2", CharsPerTokenUsed: 6.25, PromptTokens: &two},
		{Tier: 200000, Label: "primary_3", CharsPerTokenUsed: 6.25, PromptTokens: &three},
	}
	text := Markdown(cfg, trials, "/tmp/run")
	if !strings.Contains(text, "median of 3 trial(s)") {
		t.Fatalf("missing median calibration note:\n%s", text)
	}
}

func TestMarkdownShowsProviderErrorDetails(t *testing.T) {
	cfg := config.Config{TestModes: []string{"needle"}}
	trial := runner.Trial{
		Tier:       200000,
		Label:      "primary",
		HTTPStatus: 429,
		Attempts:   2,
		Score: scoring.Score{
			State:  "request_failed",
			Detail: `{"error":{"message":"usage limit reached","code":"usage_limit_reached"}}`,
		},
	}
	text := Markdown(cfg, []runner.Trial{trial}, "/tmp/run")
	if !strings.Contains(text, "| 200000 | primary | request_failed | 429 | 2 | usage_limit_reached | usage limit reached |") {
		t.Fatalf("missing provider error row:\n%s", text)
	}
}

func TestChineseErrorTranslationFallsBackToOriginal(t *testing.T) {
	if got := chineseErrorMessage("unknown_code", "Proprietary provider message"); !strings.Contains(got, "Proprietary provider message") {
		t.Fatalf("unknown message was not preserved: %q", got)
	}
	if got := chineseErrorMessage("model_cooldown", "usage limit has been reached"); got != "模型正在冷却，且使用额度已达到上限" {
		t.Fatalf("unexpected translated message: %q", got)
	}
}

func TestEstimateUsageUsesKUnits(t *testing.T) {
	cfg := config.Config{Tiers: []int{200000}, AnchorCount: 13, MaxOutputTokens: 2500}
	estimates := EstimateUsage(cfg)
	if len(estimates) != 1 {
		t.Fatalf("unexpected estimates: %#v", estimates)
	}
	estimate := estimates[0]
	if estimate.InputTokens != 189500 || estimate.OutputTokens != 2500 || estimate.TotalTokens != 192000 {
		t.Fatalf("unexpected usage estimate: %#v", estimate)
	}
}

func TestMarkdownNormalModeUsesIndividualCalibration(t *testing.T) {
	cfg := config.Config{TestModes: []string{"needle"}}
	individual := &runner.Calibration{RecommendedCharsPerToken: 4.25}
	trials := []runner.Trial{{Tier: 200000, Label: "primary", CharsPerTokenUsed: 6.25, Calibration: individual}}
	text := Markdown(cfg, trials, "/tmp/run")
	if !strings.Contains(text, "4.2500") {
		t.Fatalf("normal mode did not use individual calibration:\n%s", text)
	}
}

func TestAggregateSummarizesRepeatedTrials(t *testing.T) {
	promptPassed, promptLimit, promptFailed := 100, 110, 120
	completionPassed, completionLimit, completionFailed := 10, 20, 30
	trials := []runner.Trial{
		{Tier: 200000, Label: "primary_1", PromptTokens: &promptPassed, CompletionTokens: &completionPassed, ElapsedSeconds: 1, Score: scoring.Score{State: "passed"}},
		{Tier: 200000, Label: "primary_2", PromptTokens: &promptLimit, CompletionTokens: &completionLimit, ElapsedSeconds: 2, ContextTooLarge: true},
		{Tier: 200000, Label: "primary_3", PromptTokens: &promptFailed, CompletionTokens: &completionFailed, ElapsedSeconds: 3, Score: scoring.Score{State: "scored_failure"}},
	}
	stats := Aggregate(trials)
	if len(stats) != 1 {
		t.Fatalf("expected one tier summary, got %#v", stats)
	}
	stat := stats[0]
	if stat.Trials != 3 || stat.Passed != 1 || stat.ContextLimits != 1 || stat.Failed != 1 {
		t.Fatalf("unexpected counts: %#v", stat)
	}
	if stat.PassRatePercent < 33.33 || stat.PassRatePercent > 33.34 || stat.Status != "partial" {
		t.Fatalf("unexpected rate/status: %#v", stat)
	}
	if stat.PromptTokensAverage != 110 || stat.PromptTokensMedian != 110 || stat.PromptTokensMin != 100 || stat.PromptTokensMax != 120 {
		t.Fatalf("unexpected prompt statistics: %#v", stat)
	}
	if stat.CompletionTokensAverage != 20 || stat.ElapsedSecondsAverage != 2 {
		t.Fatalf("unexpected output/latency statistics: %#v", stat)
	}
	if stat.ElapsedSecondsMedian != 2 || stat.ElapsedSecondsP95 != 3 {
		t.Fatalf("unexpected latency statistics: %#v", stat)
	}
	if stat.PromptTokensTotal != 330 || stat.CompletionTokensTotal != 60 || stat.TotalTokensTotal != 390 || stat.TotalTokensAverage != 130 {
		t.Fatalf("unexpected token totals: %#v", stat)
	}
}

func TestMarkdownIncludesResponseMetadata(t *testing.T) {
	cfg := config.Config{TestModes: []string{"needle"}}
	trial := runner.Trial{
		Tier:  200000,
		Label: "primary",
		ResponseMetadata: map[string]any{
			"model":                       "actual-model",
			"_structured_output_fallback": true,
			"choices": []any{
				map[string]any{"finish_reason": "stop"},
			},
			"usage": map[string]any{
				"prompt_tokens_details":     map[string]any{"cached_tokens": 0},
				"completion_tokens_details": map[string]any{"reasoning_tokens": 123},
			},
		},
		Score: scoring.Score{State: "passed", Repaired: true},
	}
	english := Markdown(cfg, []runner.Trial{trial}, "/tmp/run")
	for _, expected := range []string{"## Response metadata", "actual-model", "stop", "| 0 | 123 | yes | yes |"} {
		if !strings.Contains(english, expected) {
			t.Fatalf("English metadata missing %q:\n%s", expected, english)
		}
	}
	cfg.ReportLanguage = "cn"
	chinese := MarkdownChinese(cfg, []runner.Trial{trial}, "/tmp/run")
	for _, expected := range []string{"## 响应元数据", "actual-model", "停止", "| 0 | 123 | 是 | 是 |"} {
		if !strings.Contains(chinese, expected) {
			t.Fatalf("Chinese metadata missing %q:\n%s", expected, chinese)
		}
	}
}

func TestWriteSummaryCreatesTierSummaryJSON(t *testing.T) {
	runRoot := t.TempDir()
	cfg := config.Config{TestModes: []string{"needle"}}
	if err := WriteSummary(runRoot, cfg, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(runRoot, "tier_summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stats []TierStats
	if err := json.Unmarshal(data, &stats); err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("unexpected stats for no trials: %#v", stats)
	}
	results, err := os.ReadFile(filepath.Join(runRoot, "RESULTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(results), "# Long-context benchmark result") {
		t.Fatalf("default report was not English:\n%s", results)
	}
}

func TestWriteSummaryCreatesChineseReport(t *testing.T) {
	runRoot := t.TempDir()
	cfg := config.Config{TestModes: []string{"needle"}, ReportLanguage: "cn"}
	trial := runner.Trial{
		Tier:       200000,
		Label:      "primary",
		HTTPStatus: 429,
		Attempts:   2,
		Score: scoring.Score{
			State:  "request_failed",
			Detail: `{"error":{"message":"usage limit reached","code":"usage_limit_reached"}}`,
		},
	}
	if err := WriteSummary(runRoot, cfg, []runner.Trial{trial}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(runRoot, "RESULTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"# 长上下文测试结果", "## 档位汇总", "## 错误", "请求失败", "使用额度已用尽", "使用额度已达到上限"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Chinese report missing %q:\n%s", expected, text)
		}
	}
}
