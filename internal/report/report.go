package report

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"context-LLM/internal/config"
	"context-LLM/internal/runner"
)

type TierStats struct {
	Tier                    int     `json:"tier"`
	Status                  string  `json:"status"`
	Trials                  int     `json:"trials"`
	Passed                  int     `json:"passed"`
	PassRatePercent         float64 `json:"pass_rate_percent"`
	ContextLimits           int     `json:"context_limits"`
	Failed                  int     `json:"failed"`
	PromptTokensSamples     int     `json:"prompt_tokens_samples"`
	PromptTokensAverage     float64 `json:"prompt_tokens_average,omitempty"`
	PromptTokensMedian      int     `json:"prompt_tokens_median,omitempty"`
	PromptTokensMin         int     `json:"prompt_tokens_min,omitempty"`
	PromptTokensMax         int     `json:"prompt_tokens_max,omitempty"`
	CompletionTokensSamples int     `json:"completion_tokens_samples"`
	CompletionTokensAverage float64 `json:"completion_tokens_average,omitempty"`
	PromptTokensTotal       int     `json:"prompt_tokens_total,omitempty"`
	CompletionTokensTotal   int     `json:"completion_tokens_total,omitempty"`
	TotalTokensSamples      int     `json:"total_tokens_samples,omitempty"`
	TotalTokensTotal        int     `json:"total_tokens_total,omitempty"`
	TotalTokensAverage      float64 `json:"total_tokens_average,omitempty"`
	ElapsedSecondsAverage   float64 `json:"elapsed_seconds_average,omitempty"`
	ElapsedSecondsMedian    float64 `json:"elapsed_seconds_median,omitempty"`
	ElapsedSecondsP95       float64 `json:"elapsed_seconds_p95,omitempty"`
}

type UsageEstimate struct {
	Tier         int `json:"tier"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type responseMetadataSummary struct {
	Model              string
	FinishReason       string
	CachedTokens       *int
	ReasoningTokens    *int
	StructuredFallback *bool
	JSONRepaired       bool
}

func summarizeResponseMetadata(trial runner.Trial) responseMetadataSummary {
	metadata := trial.ResponseMetadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	return responseMetadataSummary{
		Model:              stringValueFromAny(metadata["model"]),
		FinishReason:       responseFinishReason(metadata),
		CachedTokens:       responseTokenCount(metadata, "cached_tokens", "prompt_tokens_details", "input_tokens_details"),
		ReasoningTokens:    responseTokenCount(metadata, "reasoning_tokens", "completion_tokens_details", "output_tokens_details"),
		StructuredFallback: responseBool(metadata["_structured_output_fallback"]),
		JSONRepaired:       trial.Score.Repaired,
	}
}

func responseFinishReason(metadata map[string]any) string {
	if choices, ok := metadata["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if reason, ok := choice["finish_reason"].(string); ok && reason != "" {
				return reason
			}
		}
	}
	if status, ok := metadata["status"].(string); ok && status != "" {
		if status == "incomplete" {
			if details, ok := metadata["incomplete_details"].(map[string]any); ok {
				if reason, ok := details["reason"].(string); ok && reason != "" {
					return status + ":" + reason
				}
			}
		}
		return status
	}
	if output, ok := metadata["output"].([]any); ok && len(output) > 0 {
		if item, ok := output[0].(map[string]any); ok {
			if status, ok := item["status"].(string); ok && status != "" {
				return status
			}
		}
	}
	return ""
}

func responseTokenCount(metadata map[string]any, key string, detailKeys ...string) *int {
	usage, ok := metadata["usage"].(map[string]any)
	if !ok {
		return nil
	}
	for _, detailKey := range detailKeys {
		if details, ok := usage[detailKey].(map[string]any); ok {
			if raw, exists := details[key]; exists {
				result := intValueFromAny(raw)
				return &result
			}
		}
	}
	return nil
}

func responseBool(value any) *bool {
	if result, ok := value.(bool); ok {
		return &result
	}
	return nil
}

func intValueFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	default:
		return 0
	}
}

func boolText(value bool, trueText, falseText string) string {
	if value {
		return trueText
	}
	return falseText
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func Markdown(cfg config.Config, trials []runner.Trial, root string) string {
	lines := []string{
		"# Long-context benchmark result",
		"",
		"- Provider: `" + cfg.BaseURL + "`",
		"- Model: `" + cfg.Model + "`",
		"- Run folder: `" + filepath.Base(root) + "`",
		"- Test modes: `" + strings.Join(cfg.TestModes, ",") + "`",
		"- Evidence tolerance: `" + strconv.Itoa(cfg.EvidenceTolerance) + "` (all answers must still be correct).",
		"- Response metadata: preserved in `summary.json` and the checkpoint; message content is excluded.",
		"- API key is intentionally excluded from all saved files.",
	}
	for i := len(trials) - 1; i >= 0; i-- {
		if calibration := calibrationForTrial(cfg, trials, trials[i]); calibration != nil {
			lines = append(lines, fmt.Sprintf("- Recommended `chars_per_token` for the next run: `%.4f` (median of %d trial(s)).", calibration.RecommendedCharsPerToken, len(trialsForTier(trials, trials[i].Tier))))
			break
		}
	}
	stats := Aggregate(trials)
	lines = append(lines, "", "## Tier summary", "", "| Tier | Status | Trials | Passed | Pass rate | Context limits | Failed | Input avg | Input median | Input range | Input total | Output avg | Output total | Total avg | Latency avg | Latency median | P95 |", "|---:|---|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|---:|---:|")
	for _, stat := range stats {
		inputAverage, inputMedian, inputRange := "—", "—", "—"
		if stat.PromptTokensSamples > 0 {
			inputAverage = formatK(int(stat.PromptTokensAverage))
			inputMedian = formatK(stat.PromptTokensMedian)
			inputRange = fmt.Sprintf("%s–%s", formatK(stat.PromptTokensMin), formatK(stat.PromptTokensMax))
		}
		outputAverage := "—"
		inputTotal, outputTotal, totalAverage := "—", "—", "—"
		if stat.CompletionTokensSamples > 0 {
			outputAverage = formatK(int(stat.CompletionTokensAverage))
		}
		if stat.PromptTokensTotal > 0 {
			inputTotal = formatK(stat.PromptTokensTotal)
		}
		if stat.CompletionTokensTotal > 0 {
			outputTotal = formatK(stat.CompletionTokensTotal)
		}
		if stat.TotalTokensSamples > 0 {
			totalAverage = formatK(int(stat.TotalTokensAverage))
		}
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %d | %d | %.1f%% | %d | %d | %s | %s | %s | %s | %s | %s | %s | %.2fs | %.2fs | %.2fs |",
			comma(stat.Tier),
			stat.Status,
			stat.Trials,
			stat.Passed,
			stat.PassRatePercent,
			stat.ContextLimits,
			stat.Failed,
			inputAverage,
			inputMedian,
			inputRange,
			outputAverage,
			inputTotal,
			outputTotal,
			totalAverage,
			stat.ElapsedSecondsAverage,
			stat.ElapsedSecondsMedian,
			stat.ElapsedSecondsP95,
		))
	}
	lines = append(lines, "", "## Response metadata", "", "| Tier | Trial | Model | Finish reason | Cached tokens | Reasoning tokens | Structured fallback | JSON repaired |", "|---:|---|---|---|---:|---:|---|---|")
	if len(trials) == 0 {
		lines = append(lines, "| — | — | — | — | — | — | — | — |")
	}
	for _, trial := range trials {
		summary := summarizeResponseMetadata(trial)
		cached, reasoning, fallback, repaired := "—", "—", "—", "no"
		if summary.CachedTokens != nil {
			cached = comma(*summary.CachedTokens)
		}
		if summary.ReasoningTokens != nil {
			reasoning = comma(*summary.ReasoningTokens)
		}
		if summary.StructuredFallback != nil {
			fallback = boolText(*summary.StructuredFallback, "yes", "no")
		}
		if summary.JSONRepaired {
			repaired = "yes"
		}
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %s | %s | %s | %s | %s | %s |",
			comma(trial.Tier),
			trial.Label,
			sanitizeCell(orDash(summary.Model)),
			sanitizeCell(orDash(summary.FinishReason)),
			cached,
			reasoning,
			fallback,
			repaired,
		))
	}
	lines = append(lines, "", "## Errors", "", "| Tier | Trial | Kind | HTTP | Attempts | Code | Message |", "|---:|---|---|---:|---:|---|---|")
	errorRows := 0
	for _, trial := range trials {
		if trial.Score.State == "passed" && !trial.ContextTooLarge {
			continue
		}
		detail := errorDetail(trial)
		errorRows++
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %s | %s | %d | %s | %s |",
			comma(trial.Tier),
			trial.Label,
			errorKind(trial),
			errorHTTPStatus(trial),
			trial.Attempts,
			sanitizeCell(detail.Code),
			sanitizeCell(detail.Message),
		))
	}
	if errorRows == 0 {
		lines = append(lines, "| — | — | none | — | 0 | — | No failures detected. |")
	}
	lines = append(lines, "", "| Configured tier | Trial | CPT used | Actual input | Actual output | Actual total | Recommended CPT | Output limit | Result | Score |", "|---:|---|---:|---:|---:|---:|---:|---:|---|---|")
	for _, trial := range trials {
		actual, recommended := "—", "—"
		actualOutput, actualTotal := "—", "—"
		if trial.PromptTokens != nil {
			actual = formatK(*trial.PromptTokens)
		}
		if trial.CompletionTokens != nil {
			actualOutput = formatK(*trial.CompletionTokens)
		}
		if trial.PromptTokens != nil && trial.CompletionTokens != nil {
			actualTotal = formatK(*trial.PromptTokens + *trial.CompletionTokens)
		}
		if calibration := calibrationForTrial(cfg, trials, trial); calibration != nil {
			recommended = fmt.Sprintf("%.4f", calibration.RecommendedCharsPerToken)
		}
		outputLimit := "—"
		if trial.MaxOutputTokensUsed > 0 {
			outputLimit = comma(trial.MaxOutputTokensUsed)
		}
		result := trial.Score.State
		if trial.ContextTooLarge {
			result = "context limit"
		}
		score := fmt.Sprintf("%d/%d answers; %d/%d evidence", trial.Score.AnswerHits, trial.Score.QuestionCount, trial.Score.EvidenceHits, trial.Score.QuestionCount)
		if trial.Score.Repaired {
			score += "; repaired JSON"
		}
		lines = append(lines, fmt.Sprintf("| %s | %s | %.4f | %s | %s | %s | %s | %s | %s | %s |", comma(trial.Tier), trial.Label, trial.CharsPerTokenUsed, actual, actualOutput, actualTotal, recommended, outputLimit, result, score))
	}
	lines = append(lines, "", "## Mode breakdown", "", "| Tier | Trial | Mode | Answers | Evidence | Missed |", "|---:|---|---|---:|---:|---|")
	for _, trial := range trials {
		if len(trial.Score.ByMode) == 0 {
			continue
		}
		modes := make([]string, 0, len(trial.Score.ByMode))
		for mode := range trial.Score.ByMode {
			modes = append(modes, mode)
		}
		sort.Strings(modes)
		for _, mode := range modes {
			modeScore := trial.Score.ByMode[mode]
			lines = append(lines, fmt.Sprintf(
				"| %s | %s | %s | %d/%d | %d/%d | %s |",
				comma(trial.Tier),
				trial.Label,
				mode,
				modeScore.AnswerHits,
				modeScore.QuestionCount,
				modeScore.EvidenceHits,
				modeScore.QuestionCount,
				strings.Join(modeScore.Missed, ", "),
			))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func MarkdownChinese(cfg config.Config, trials []runner.Trial, root string) string {
	lines := []string{
		"# 长上下文测试结果",
		"",
		"- 服务商：`" + cfg.BaseURL + "`",
		"- 模型：`" + cfg.Model + "`",
		"- 运行目录：`" + filepath.Base(root) + "`",
		"- 测试题型：`" + strings.Join(cfg.TestModes, ",") + "`",
		"- 证据宽容度：`" + strconv.Itoa(cfg.EvidenceTolerance) + "`（所有答案仍必须正确）。",
		"- 响应元数据已保存到 `summary.json` 和 checkpoint，不包含消息正文。",
		"- 错误代码和常见消息已翻译；未识别的供应商消息保留英文原文。",
		"- 公开配置和报告不包含 API 密钥。",
	}
	for i := len(trials) - 1; i >= 0; i-- {
		if calibration := calibrationForTrial(cfg, trials, trials[i]); calibration != nil {
			lines = append(lines, fmt.Sprintf("- 下次运行建议 `chars_per_token`：`%.4f`（基于 %d 次测试的中位数）。", calibration.RecommendedCharsPerToken, len(trialsForTier(trials, trials[i].Tier))))
			break
		}
	}

	stats := Aggregate(trials)
	lines = append(lines, "", "## 档位汇总", "", "| 档位 | 状态 | 测试数 | 通过 | 通过率 | 上下限 | 失败 | 输入均值 | 输入中位数 | 输入范围 | 输入总量 | 输出均值 | 输出总量 | 总量均值 | 平均耗时 | 耗时中位数 | P95 |", "|---:|---|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|---:|---:|")
	for _, stat := range stats {
		inputAverage, inputMedian, inputRange := "—", "—", "—"
		if stat.PromptTokensSamples > 0 {
			inputAverage = formatK(int(stat.PromptTokensAverage))
			inputMedian = formatK(stat.PromptTokensMedian)
			inputRange = fmt.Sprintf("%s–%s", formatK(stat.PromptTokensMin), formatK(stat.PromptTokensMax))
		}
		outputAverage, inputTotal, outputTotal, totalAverage := "—", "—", "—", "—"
		if stat.CompletionTokensSamples > 0 {
			outputAverage = formatK(int(stat.CompletionTokensAverage))
		}
		if stat.PromptTokensTotal > 0 {
			inputTotal = formatK(stat.PromptTokensTotal)
		}
		if stat.CompletionTokensTotal > 0 {
			outputTotal = formatK(stat.CompletionTokensTotal)
		}
		if stat.TotalTokensSamples > 0 {
			totalAverage = formatK(int(stat.TotalTokensAverage))
		}
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %d | %d | %.1f%% | %d | %d | %s | %s | %s | %s | %s | %s | %s | %.2fs | %.2fs | %.2fs |",
			comma(stat.Tier),
			chineseTierStatus(stat.Status),
			stat.Trials,
			stat.Passed,
			stat.PassRatePercent,
			stat.ContextLimits,
			stat.Failed,
			inputAverage,
			inputMedian,
			inputRange,
			inputTotal,
			outputAverage,
			outputTotal,
			totalAverage,
			stat.ElapsedSecondsAverage,
			stat.ElapsedSecondsMedian,
			stat.ElapsedSecondsP95,
		))
	}

	lines = append(lines, "", "## 响应元数据", "", "| 档位 | 测试 | 实际模型 | 结束原因 | 缓存 Token | 思考 Token | 结构化降级 | JSON 修复 |", "|---:|---|---|---|---:|---:|---|---|")
	if len(trials) == 0 {
		lines = append(lines, "| — | — | — | — | — | — | — | — |")
	}
	for _, trial := range trials {
		summary := summarizeResponseMetadata(trial)
		cached, reasoning, fallback, repaired := "—", "—", "—", "否"
		if summary.CachedTokens != nil {
			cached = comma(*summary.CachedTokens)
		}
		if summary.ReasoningTokens != nil {
			reasoning = comma(*summary.ReasoningTokens)
		}
		if summary.StructuredFallback != nil {
			fallback = boolText(*summary.StructuredFallback, "是", "否")
		}
		if summary.JSONRepaired {
			repaired = "是"
		}
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %s | %s | %s | %s | %s | %s |",
			comma(trial.Tier),
			trial.Label,
			sanitizeCell(orDash(summary.Model)),
			sanitizeCell(orDash(chineseFinishReason(summary.FinishReason))),
			cached,
			reasoning,
			fallback,
			repaired,
		))
	}

	lines = append(lines, "", "## 错误", "", "| 档位 | 测试 | 类型 | HTTP | 尝试次数 | 错误代码 | 错误消息 |", "|---:|---|---|---:|---:|---|---|")
	errorRows := 0
	for _, trial := range trials {
		if trial.Score.State == "passed" && !trial.ContextTooLarge {
			continue
		}
		detail := errorDetail(trial)
		errorRows++
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %s | %s | %d | %s | %s |",
			comma(trial.Tier),
			trial.Label,
			chineseErrorKind(errorKind(trial)),
			errorHTTPStatus(trial),
			trial.Attempts,
			sanitizeCell(chineseErrorCode(detail.Code)),
			sanitizeCell(chineseErrorMessage(detail.Code, detail.Message)),
		))
	}
	if errorRows == 0 {
		lines = append(lines, "| — | — | 无 | — | 0 | — | 未检测到失败。 |")
	}

	lines = append(lines, "", "## 单次测试结果", "", "| 档位 | 测试 | CPT | 实际输入 | 实际输出 | 实际总量 | 建议 CPT | 输出上限 | 结果 | 得分 |", "|---:|---|---:|---:|---:|---:|---:|---:|---|---|")
	for _, trial := range trials {
		actual, recommended := "—", "—"
		actualOutput, actualTotal := "—", "—"
		if trial.PromptTokens != nil {
			actual = formatK(*trial.PromptTokens)
		}
		if trial.CompletionTokens != nil {
			actualOutput = formatK(*trial.CompletionTokens)
		}
		if trial.PromptTokens != nil && trial.CompletionTokens != nil {
			actualTotal = formatK(*trial.PromptTokens + *trial.CompletionTokens)
		}
		if calibration := calibrationForTrial(cfg, trials, trial); calibration != nil {
			recommended = fmt.Sprintf("%.4f", calibration.RecommendedCharsPerToken)
		}
		outputLimit := "—"
		if trial.MaxOutputTokensUsed > 0 {
			outputLimit = comma(trial.MaxOutputTokensUsed)
		}
		score := fmt.Sprintf("%d/%d 答案；%d/%d 证据", trial.Score.AnswerHits, trial.Score.QuestionCount, trial.Score.EvidenceHits, trial.Score.QuestionCount)
		if trial.Score.Repaired {
			score += "；已修复 JSON"
		}
		lines = append(lines, fmt.Sprintf("| %s | %s | %.4f | %s | %s | %s | %s | %s | %s | %s |", comma(trial.Tier), trial.Label, trial.CharsPerTokenUsed, actual, actualOutput, actualTotal, recommended, outputLimit, chineseTrialResult(trial), score))
	}

	lines = append(lines, "", "## 分题型成绩", "", "| 档位 | 测试 | 题型 | 答案 | 证据 | 漏答 |", "|---:|---|---|---:|---:|---|")
	for _, trial := range trials {
		if len(trial.Score.ByMode) == 0 {
			continue
		}
		modes := make([]string, 0, len(trial.Score.ByMode))
		for mode := range trial.Score.ByMode {
			modes = append(modes, mode)
		}
		sort.Strings(modes)
		for _, mode := range modes {
			modeScore := trial.Score.ByMode[mode]
			lines = append(lines, fmt.Sprintf(
				"| %s | %s | %s | %d/%d | %d/%d | %s |",
				comma(trial.Tier),
				trial.Label,
				mode,
				modeScore.AnswerHits,
				modeScore.QuestionCount,
				modeScore.EvidenceHits,
				modeScore.QuestionCount,
				strings.Join(modeScore.Missed, ", "),
			))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func chineseTierStatus(status string) string {
	switch status {
	case "passed":
		return "通过"
	case "context_limit":
		return "上下文超限"
	case "partial":
		return "部分完成"
	default:
		return status
	}
}

func chineseErrorKind(kind string) string {
	switch kind {
	case "context_limit":
		return "上下文超限"
	case "request_failed":
		return "请求失败"
	case "invalid_answer_json":
		return "JSON 无效"
	case "scored_failure":
		return "评分失败"
	default:
		return kind
	}
}

func chineseFinishReason(reason string) string {
	switch strings.ToLower(reason) {
	case "stop":
		return "停止"
	case "length":
		return "长度截断"
	case "content_filter":
		return "内容过滤"
	case "completed":
		return "完成"
	case "incomplete":
		return "未完成"
	case "failed":
		return "失败"
	default:
		if strings.HasPrefix(strings.ToLower(reason), "incomplete:") {
			return "未完成：" + strings.TrimPrefix(reason, "incomplete:")
		}
		return reason
	}
}

func chineseErrorCode(code string) string {
	switch strings.ToLower(code) {
	case "request_failed":
		return "请求失败"
	case "invalid_answer_json":
		return "JSON 无效"
	case "scored_failure":
		return "评分失败"
	case "context_too_large", "context_length_exceeded":
		return "上下文超长"
	case "transport_error":
		return "网络传输错误"
	case "model_cooldown":
		return "模型冷却中"
	case "usage_limit_reached":
		return "使用额度已用尽"
	case "rate_limit_exceeded", "too_many_requests":
		return "请求频率或额度受限"
	case "authentication_error", "invalid_api_key":
		return "API 密钥或鉴权错误"
	case "permission_denied":
		return "权限不足"
	case "invalid_request_error":
		return "请求参数错误"
	case "model_not_found":
		return "模型不存在"
	case "content_policy_violation":
		return "内容策略拦截"
	case "server_error", "internal_server_error":
		return "服务器内部错误"
	case "upstream_error":
		return "上游服务错误"
	case "overloaded":
		return "服务过载"
	default:
		return code
	}
}

func chineseErrorMessage(code, message string) string {
	if strings.TrimSpace(message) == "" {
		return "未提供具体错误消息"
	}
	if containsChinese(message) {
		return message
	}
	lower := strings.ToLower(message)
	switch strings.ToLower(code) {
	case "model_cooldown":
		if strings.Contains(lower, "usage limit") || strings.Contains(lower, "cooling down") {
			return "模型正在冷却，且使用额度已达到上限"
		}
		return "模型正在冷却"
	case "usage_limit_reached":
		return "使用额度已达到上限"
	case "context_too_large", "context_length_exceeded":
		return "输入超过模型上下文窗口"
	}
	phrases := []struct {
		needle      string
		translation string
	}{
		{"usage limit has been reached", "使用额度已达到上限"},
		{"usage limit reached", "使用额度已达到上限"},
		{"cooling down", "模型正在冷却"},
		{"exceeds the context window", "输入超过模型上下文窗口"},
		{"rate limit", "请求频率或额度受限"},
		{"too many requests", "请求过于频繁"},
		{"invalid api key", "API 密钥无效"},
		{"incorrect api key", "API 密钥无效"},
		{"permission denied", "权限不足"},
		{"model not found", "模型不存在"},
		{"content policy", "内容策略拦截"},
		{"server error", "服务器内部错误"},
		{"overloaded", "服务过载"},
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase.needle) {
			return phrase.translation
		}
	}
	return "未识别的英文消息：" + message
}

func containsChinese(value string) bool {
	for _, character := range value {
		if character >= 0x4e00 && character <= 0x9fff {
			return true
		}
	}
	return false
}

func chineseTrialResult(trial runner.Trial) string {
	if trial.ContextTooLarge {
		return "上下文超限"
	}
	switch trial.Score.State {
	case "passed":
		return "通过"
	case "request_failed":
		return "请求失败"
	case "invalid_answer_json":
		return "JSON 无效"
	case "scored_failure":
		return "评分失败"
	default:
		return trial.Score.State
	}
}

type reportErrorDetail struct {
	Code    string
	Message string
}

func errorKind(trial runner.Trial) string {
	if trial.ContextTooLarge {
		return "context_limit"
	}
	return trial.Score.State
}

func errorHTTPStatus(trial runner.Trial) string {
	if trial.HTTPStatus > 0 {
		return strconv.Itoa(trial.HTTPStatus)
	}
	return "—"
}

func errorDetail(trial runner.Trial) reportErrorDetail {
	if trial.ContextTooLarge {
		message := trial.Score.Detail
		if message == "" {
			message = "input exceeded the model context window"
		}
		return reportErrorDetail{Code: "context_too_large", Message: message}
	}
	if trial.Score.State == "scored_failure" {
		message := trial.Score.Detail
		if message == "" && len(trial.Score.Missed) > 0 {
			message = "missed: " + strings.Join(trial.Score.Missed, ", ")
		}
		return reportErrorDetail{Code: "scored_failure", Message: message}
	}
	if trial.Score.State == "invalid_answer_json" {
		return reportErrorDetail{Code: "invalid_answer_json", Message: trial.Score.Detail}
	}
	detail := trial.Score.Detail
	if detail == "" {
		detail = trial.TransportError
	}
	parsed := parseProviderError(detail)
	if parsed.Code != "" || parsed.Message != "" {
		return parsed
	}
	return reportErrorDetail{Code: trial.Score.State, Message: detail}
}

func parseProviderError(detail string) reportErrorDetail {
	if detail == "" {
		return reportErrorDetail{}
	}
	var payload struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal([]byte(detail), &payload); err != nil {
		return reportErrorDetail{Message: detail}
	}
	switch typed := payload.Error.(type) {
	case string:
		return reportErrorDetail{Message: typed}
	case map[string]any:
		result := reportErrorDetail{Message: stringValueFromAny(typed["message"])}
		result.Code = stringValueFromAny(typed["code"])
		if result.Code == "" {
			result.Code = stringValueFromAny(typed["type"])
		}
		return result
	default:
		return reportErrorDetail{Message: detail}
	}
}

func stringValueFromAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func sanitizeCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func trialsForTier(trials []runner.Trial, tier int) []runner.Trial {
	group := []runner.Trial{}
	for _, trial := range trials {
		if trial.Tier == tier && trial.PromptTokens != nil {
			group = append(group, trial)
		}
	}
	return group
}

func calibrationForTrial(cfg config.Config, trials []runner.Trial, target runner.Trial) *runner.Calibration {
	if !cfg.EnhancedAutoTest {
		return target.Calibration
	}
	group := trialsForTier(trials, target.Tier)
	if len(group) == 0 {
		return nil
	}
	return runner.CalibrateFromTrials(target.Tier, target.CharsPerTokenUsed, group)
}

func PublicConfig(cfg config.Config) map[string]any {
	return map[string]any{
		"base_url":                cfg.BaseURL,
		"model":                   cfg.Model,
		"api_mode":                cfg.APIMode,
		"reasoning_effort":        cfg.ReasoningEffort,
		"reasoning_summary":       cfg.ReasoningSummary,
		"tiers":                   cfg.Tiers,
		"chars_per_token":         cfg.CharsPerToken,
		"max_output_tokens":       cfg.MaxOutputTokens,
		"timeout_seconds":         int(cfg.Timeout.Seconds()),
		"transport_retries":       cfg.TransportRetries,
		"auto_calibrate":          cfg.AutoCalibrate,
		"enhanced_auto_test":      cfg.EnhancedAutoTest,
		"binary_precision_tokens": cfg.BinaryPrecisionTokens,
		"structured_output":       cfg.StructuredOutput,
		"parallel_tests":          cfg.ParallelTests,
		"request_interval_ms":     cfg.RequestIntervalMS,
		"anchor_count":            cfg.AnchorCount,
		"evidence_tolerance":      cfg.EvidenceTolerance,
		"test_modes":              cfg.TestModes,
		"report_language":         cfg.ReportLanguage,
		"preview_after_run":       cfg.PreviewAfterRun,
	}
}

func Aggregate(trials []runner.Trial) []TierStats {
	if len(trials) == 0 {
		return nil
	}
	grouped := map[int][]runner.Trial{}
	order := []int{}
	for _, trial := range trials {
		if _, exists := grouped[trial.Tier]; !exists {
			order = append(order, trial.Tier)
		}
		grouped[trial.Tier] = append(grouped[trial.Tier], trial)
	}

	stats := make([]TierStats, 0, len(order))
	for _, tier := range order {
		group := grouped[tier]
		stat := TierStats{Tier: tier, Trials: len(group)}
		promptValues := []int{}
		completionValues := []int{}
		totalValues := []int{}
		elapsedValues := []float64{}
		for _, trial := range group {
			if !trial.ContextTooLarge && trial.Score.State == "passed" {
				stat.Passed++
			} else if trial.ContextTooLarge {
				stat.ContextLimits++
			} else {
				stat.Failed++
			}
			if trial.PromptTokens != nil && *trial.PromptTokens > 0 {
				promptValues = append(promptValues, *trial.PromptTokens)
			}
			if trial.CompletionTokens != nil && *trial.CompletionTokens > 0 {
				completionValues = append(completionValues, *trial.CompletionTokens)
			}
			if trial.PromptTokens != nil && trial.CompletionTokens != nil && *trial.PromptTokens > 0 && *trial.CompletionTokens > 0 {
				totalValues = append(totalValues, *trial.PromptTokens+*trial.CompletionTokens)
			}
			elapsedValues = append(elapsedValues, trial.ElapsedSeconds)
		}
		stat.PassRatePercent = float64(stat.Passed) / float64(stat.Trials) * 100
		stat.Status = tierStatus(stat)
		stat.PromptTokensSamples = len(promptValues)
		if len(promptValues) > 0 {
			sort.Ints(promptValues)
			stat.PromptTokensAverage = averageInts(promptValues)
			stat.PromptTokensMedian = promptValues[len(promptValues)/2]
			stat.PromptTokensMin = promptValues[0]
			stat.PromptTokensMax = promptValues[len(promptValues)-1]
			stat.PromptTokensTotal = sumInts(promptValues)
		}
		stat.CompletionTokensSamples = len(completionValues)
		if len(completionValues) > 0 {
			stat.CompletionTokensAverage = averageInts(completionValues)
			stat.CompletionTokensTotal = sumInts(completionValues)
		}
		stat.TotalTokensSamples = len(totalValues)
		if len(totalValues) > 0 {
			stat.TotalTokensTotal = sumInts(totalValues)
			stat.TotalTokensAverage = averageInts(totalValues)
		}
		if len(elapsedValues) > 0 {
			sort.Float64s(elapsedValues)
			stat.ElapsedSecondsAverage = averageFloats(elapsedValues)
			stat.ElapsedSecondsMedian = medianFloats(elapsedValues)
			stat.ElapsedSecondsP95 = percentileFloats(elapsedValues, 0.95)
		}
		stats = append(stats, stat)
	}
	return stats
}

func tierStatus(stat TierStats) string {
	switch {
	case stat.Passed == stat.Trials:
		return "passed"
	case stat.ContextLimits > 0 && stat.Passed == 0:
		return "context_limit"
	default:
		return "partial"
	}
}

func averageInts(values []int) float64 {
	total := 0
	for _, value := range values {
		total += value
	}
	return float64(total) / float64(len(values))
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func averageFloats(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func medianFloats(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}

func percentileFloats(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(percentile*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func WriteSummary(runRoot string, cfg config.Config, trials []runner.Trial) error {
	if err := WriteJSON(filepath.Join(runRoot, "summary.json"), trials); err != nil {
		return err
	}
	if err := WriteJSON(filepath.Join(runRoot, "tier_summary.json"), Aggregate(trials)); err != nil {
		return err
	}
	content := Markdown(cfg, trials, runRoot)
	if cfg.ReportLanguage == "cn" {
		content = MarkdownChinese(cfg, trials, runRoot)
	}
	return WriteText(filepath.Join(runRoot, "RESULTS.md"), content)
}

func WriteText(path, text string) error {
	return os.WriteFile(path, []byte(text), 0o600)
}

func comma(value int) string {
	return strconv.FormatInt(int64(value), 10)
}

func EstimateUsage(cfg config.Config) []UsageEstimate {
	const estimatedInputOverheadTokens = 1500
	outputTokens := runner.OutputLimitFor(cfg.AnchorCount, cfg.MaxOutputTokens)
	estimates := make([]UsageEstimate, 0, len(cfg.Tiers))
	for _, tier := range cfg.Tiers {
		inputTokens := tier - config.HeadroomTokens + estimatedInputOverheadTokens
		if inputTokens < 0 {
			inputTokens = 0
		}
		estimates = append(estimates, UsageEstimate{
			Tier:         tier,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  inputTokens + outputTokens,
		})
	}
	return estimates
}

func formatK(tokens int) string {
	return fmt.Sprintf("%.1fK", float64(tokens)/1000)
}
