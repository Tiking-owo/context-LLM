package config

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const HeadroomTokens = 12000
const PlaceholderAPIKey = "sk-123456789"

type Config struct {
	BaseURL               string
	Model                 string
	APIKey                string
	APIMode               string
	ReasoningEffort       string
	ReasoningSummary      string
	Tiers                 []int
	CharsPerToken         float64
	MaxOutputTokens       int
	Timeout               time.Duration
	TransportRetries      int
	AutoCalibrate         bool
	EnhancedAutoTest      bool
	BinaryPrecisionTokens int
	StructuredOutput      bool
	ParallelTests         bool
	RequestIntervalMS     int
	AnchorCount           int
	EvidenceTolerance     int
	TestModes             []string
	ReportLanguage        string
	PreviewAfterRun       bool
}

func DefaultPath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), "config.txt"), nil
}

func Load(path string, requireKey bool) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	sections := map[string]map[string]string{}
	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(stripInlineComment(scanner.Text()))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			sections[section] = map[string]string{}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			return Config{}, fmt.Errorf("invalid line %q", line)
		}
		sections[section][strings.TrimSpace(key)] = strings.TrimSpace(stripInlineComment(value))
	}
	if err := scanner.Err(); err != nil {
		return Config{}, err
	}

	provider, okProvider := sections["provider"]
	bench, okBench := sections["benchmark"]
	if !okProvider || !okBench {
		return Config{}, errors.New("both [provider] and [benchmark] sections are required")
	}
	for _, key := range []string{"base_url", "model", "api_mode"} {
		if provider[key] == "" {
			return Config{}, fmt.Errorf("missing provider setting %q", key)
		}
	}
	apiKey := provider["api_key"]
	if requireKey && apiKey == "" {
		return Config{}, errors.New("missing API key: set api_key in config.txt")
	}
	if requireKey && apiKey == PlaceholderAPIKey {
		return Config{}, errors.New("placeholder API key is not valid: replace api_key in config.txt")
	}
	if provider["api_mode"] != "chat-completions" && provider["api_mode"] != "responses" {
		return Config{}, errors.New("api_mode must be chat-completions or responses")
	}
	reasoningEffort, err := parseReasoningEffort(provider["reasoning_effort"])
	if err != nil {
		return Config{}, err
	}
	reasoningSummary, err := parseReasoningSummary(provider["reasoning_summary"])
	if err != nil {
		return Config{}, err
	}

	tiers, err := parseTiers(bench["tiers"])
	if err != nil {
		return Config{}, err
	}
	chars, err := strconv.ParseFloat(defaultString(bench["chars_per_token"], "6.25"), 64)
	if err != nil || chars <= 0 {
		return Config{}, errors.New("chars_per_token must be a positive number")
	}
	maxOutput, err := strconv.Atoi(defaultString(bench["max_output_tokens"], "2500"))
	if err != nil || maxOutput <= 0 {
		return Config{}, errors.New("max_output_tokens must be a positive integer")
	}
	timeoutSeconds, err := strconv.Atoi(defaultString(bench["timeout_seconds"], "900"))
	if err != nil || timeoutSeconds <= 0 {
		return Config{}, errors.New("timeout_seconds must be a positive integer")
	}
	retries, err := strconv.Atoi(defaultString(bench["transport_retries"], "1"))
	if err != nil || retries < 0 {
		return Config{}, errors.New("transport_retries must be zero or greater")
	}
	autoCalibrate, err := strconv.ParseBool(defaultString(bench["auto_calibrate"], "true"))
	if err != nil {
		return Config{}, errors.New("auto_calibrate must be true or false")
	}
	enhancedValue := bench["enhanced_auto_test"]
	if enhancedValue == "" {
		enhancedValue = bench["增强的自动测试"]
	}
	enhancedAutoTest, err := strconv.ParseBool(defaultString(enhancedValue, "false"))
	if err != nil {
		return Config{}, errors.New("enhanced_auto_test must be true or false")
	}
	binaryPrecisionTokens, err := strconv.Atoi(defaultString(bench["binary_precision_tokens"], "10000"))
	if err != nil || binaryPrecisionTokens <= 0 {
		return Config{}, errors.New("binary_precision_tokens must be a positive integer")
	}
	structuredOutput, err := strconv.ParseBool(defaultString(bench["structured_output"], "true"))
	if err != nil {
		return Config{}, errors.New("structured_output must be true or false")
	}
	parallelTests, err := strconv.ParseBool(defaultString(bench["parallel_tests"], "false"))
	if err != nil {
		return Config{}, errors.New("parallel_tests must be true or false")
	}
	requestIntervalMS, err := strconv.Atoi(defaultString(bench["request_interval_ms"], "0"))
	if err != nil || requestIntervalMS < 0 {
		return Config{}, errors.New("request_interval_ms must be zero or greater")
	}
	anchorCount, err := strconv.Atoi(defaultString(bench["anchor_count"], "13"))
	if err != nil || anchorCount < 13 || anchorCount > 50 {
		return Config{}, errors.New("anchor_count must be an integer from 13 to 50")
	}
	evidenceTolerance, err := strconv.Atoi(defaultString(bench["evidence_tolerance"], "0"))
	if err != nil || evidenceTolerance < 0 || evidenceTolerance > anchorCount {
		return Config{}, fmt.Errorf("evidence_tolerance must be an integer from 0 to %d", anchorCount)
	}
	testModes, err := parseTestModes(defaultString(bench["test_modes"], "needle,multi_hop,conflict,adversarial"))
	if err != nil {
		return Config{}, err
	}
	reportLanguage := strings.ToLower(strings.TrimSpace(defaultString(bench["report_language"], "en")))
	if reportLanguage != "en" && reportLanguage != "cn" {
		return Config{}, errors.New("report_language must be en or cn")
	}
	previewAfterRun, err := strconv.ParseBool(defaultString(bench["preview_after_run"], "false"))
	if err != nil {
		return Config{}, errors.New("preview_after_run must be true or false")
	}

	return Config{
		BaseURL:               strings.TrimRight(provider["base_url"], "/"),
		Model:                 provider["model"],
		APIKey:                apiKey,
		APIMode:               provider["api_mode"],
		ReasoningEffort:       reasoningEffort,
		ReasoningSummary:      reasoningSummary,
		Tiers:                 tiers,
		CharsPerToken:         chars,
		MaxOutputTokens:       maxOutput,
		Timeout:               time.Duration(timeoutSeconds) * time.Second,
		TransportRetries:      retries,
		AutoCalibrate:         autoCalibrate,
		EnhancedAutoTest:      enhancedAutoTest,
		BinaryPrecisionTokens: binaryPrecisionTokens,
		StructuredOutput:      structuredOutput,
		ParallelTests:         parallelTests,
		RequestIntervalMS:     requestIntervalMS,
		AnchorCount:           anchorCount,
		EvidenceTolerance:     evidenceTolerance,
		TestModes:             testModes,
		ReportLanguage:        reportLanguage,
		PreviewAfterRun:       previewAfterRun,
	}, nil
}

func WriteDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeText(path, `# context-LLM 配置
# 首次启动若缺少此文件，程序会在可执行文件同目录生成默认配置并退出。
# 修改后重新运行程序；# 后面的内容是注释，不参与解析。

# ============================================================
# 服务商连接
# ============================================================
[provider]
base_url           = https://api.openai.com/v1 # OpenAI 兼容接口地址
model              = GPT6.1-Astra             # 被测模型 ID
api_key            = sk-123456789             # 占位密钥，正式测试前必须替换
api_mode           = chat-completions          # 接口格式：chat-completions 或 responses

# 思考等级；default 表示不发送该参数。
# 可选：none / minimal / low / medium / high / xhigh / max
reasoning_effort   = default                   # 推理强度；default 不修改请求参数

# 仅 responses 生效；default 表示不发送该参数。
# 可选：auto / concise / detailed
reasoning_summary  = default                   # 回答摘要；default 不修改请求参数

# ============================================================
# 上下文与输出
# ============================================================
[benchmark]
# 测试档位；支持纯数字、K、M 和小数，如 1.1M
tiers              = 200K, 400K, 600K, 800K, 1M # 待测上下文档位，必须递增且不重复

chars_per_token    = 6.25   # 输入字符估算基准；运行报告会给出下次建议值
max_output_tokens  = 2500   # 输出 token 最小预算，会按锚点数和完成状态扩展
timeout_seconds    = 900    # 单次请求超时时间，单位：秒
transport_retries  = 1      # 网络、429、5xx 等可恢复错误的额外重试次数

# ============================================================
# 自动测试
# ============================================================
auto_calibrate          = true  # 按实际输入 token 校准后续档位
enhanced_auto_test      = false # true：每档三次新题、中位数校准并自动二分
binary_precision_tokens = 10000 # 增强模式二分精度，单位：token，越小越精确
structured_output       = true  # 发送 JSON 约束；供应商不支持时自动降级
parallel_tests          = false # 仅增强模式：三次测试并行；关闭则串行
request_interval_ms     = 0     # 全局请求起始间隔，单位：毫秒；0 表示不限速

# ============================================================
# 题目与评分
# ============================================================
# 总题目数，范围 13-50。锚点越多，弱模型越容易漏答或返回异常 JSON。
anchor_count         = 13      # 题目总数；弱模型建议从默认值开始

# 每种题型允许的证据错误数；建议不超过 2。
evidence_tolerance   = 0       # 宽容的证据错误数；弱模型也不建议超过 2

# 可用题型：needle / multi_hop / conflict / adversarial
test_modes           = needle,multi_hop,conflict,adversarial # 逗号分隔，至少保留一种

# RESULTS.md 输出语言：en（英文）或 cn（中文）
report_language      = en      # 报告语言：en 或 cn

# 测试完成后自动打开 RESULTS.md 预览；浏览器打开后最多运行 5 分钟
preview_after_run    = false   # true：测试结束后启动本地报告预览
`)
}

func parseTiers(raw string) ([]int, error) {
	if raw == "" {
		return nil, errors.New("tiers is required")
	}
	values := []int{}
	seen := map[int]bool{}
	last := 0
	for _, part := range strings.Split(raw, ",") {
		tier, err := parseTierToken(strings.TrimSpace(part))
		if err != nil || tier <= HeadroomTokens || tier <= last || seen[tier] {
			return nil, fmt.Errorf("tiers must use unique ascending token values greater than %d; K and M units are supported", HeadroomTokens)
		}
		values, seen[tier], last = append(values, tier), true, tier
	}
	return values, nil
}

func parseTierToken(raw string) (int, error) {
	if raw == "" {
		return 0, errors.New("empty tier")
	}
	upper := strings.ToUpper(raw)
	multiplier := 1.0
	switch {
	case strings.HasSuffix(upper, "K"):
		multiplier = 1000
		upper = strings.TrimSpace(strings.TrimSuffix(upper, "K"))
	case strings.HasSuffix(upper, "M"):
		multiplier = 1_000_000
		upper = strings.TrimSpace(strings.TrimSuffix(upper, "M"))
	}
	value, err := strconv.ParseFloat(upper, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, fmt.Errorf("invalid tier %q", raw)
	}
	return int(math.Round(value * multiplier)), nil
}

func parseTestModes(raw string) ([]string, error) {
	valid := map[string]bool{"needle": true, "multi_hop": true, "conflict": true, "adversarial": true}
	modes := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		mode := strings.TrimSpace(strings.ToLower(part))
		if mode == "" || !valid[mode] || seen[mode] {
			return nil, errors.New("test_modes must contain unique values from: needle, multi_hop, conflict, adversarial")
		}
		modes, seen[mode] = append(modes, mode), true
	}
	if len(modes) == 0 {
		return nil, errors.New("test_modes must enable at least one test mode")
	}
	return modes, nil
}

func parseReasoningEffort(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(defaultString(raw, "default")))
	switch value {
	case "default", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return value, nil
	default:
		return "", errors.New("reasoning_effort must be default, none, minimal, low, medium, high, xhigh, or max")
	}
}

func parseReasoningSummary(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(defaultString(raw, "default")))
	switch value {
	case "default", "auto", "concise", "detailed":
		return value, nil
	default:
		return "", errors.New("reasoning_summary must be default, auto, concise, or detailed")
	}
}

func stripInlineComment(value string) string {
	for _, marker := range []string{" #", " ;"} {
		if index := strings.Index(value, marker); index >= 0 {
			value = value[:index]
		}
	}
	return strings.TrimSpace(value)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeText(path, text string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(text)
	return err
}
