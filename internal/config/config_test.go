package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPathIsBesideExecutable(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Dir(executable) {
		t.Fatalf("default config path %q is not beside executable %q", path, executable)
	}
}

func TestWriteAndLoadDefaultConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.txt")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://api.openai.com/v1" || cfg.Model != "GPT6.1-Astra" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.AnchorCount != 13 || cfg.EvidenceTolerance != 0 {
		t.Fatalf("unexpected scoring defaults: %#v", cfg)
	}
	if cfg.EnhancedAutoTest {
		t.Fatal("enhanced auto test should default to false")
	}
	if cfg.BinaryPrecisionTokens != 10000 {
		t.Fatalf("unexpected binary precision: %d", cfg.BinaryPrecisionTokens)
	}
	if cfg.ReasoningEffort != "default" || cfg.ReasoningSummary != "default" {
		t.Fatalf("reasoning defaults changed: %#v", cfg)
	}
	if !cfg.StructuredOutput {
		t.Fatal("structured output should default to true")
	}
	if cfg.ParallelTests || cfg.RequestIntervalMS != 0 {
		t.Fatalf("parallel/rate-limit defaults changed: %#v", cfg)
	}
	if len(cfg.TestModes) != 4 {
		t.Fatalf("unexpected default modes: %#v", cfg.TestModes)
	}
	if cfg.ReportLanguage != "en" {
		t.Fatalf("unexpected report language: %q", cfg.ReportLanguage)
	}
	if cfg.PreviewAfterRun {
		t.Fatal("preview_after_run should default to false")
	}
}

func TestPlaceholderAPIKeyIsRejectedForRealRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.txt")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, true); err == nil {
		t.Fatal("placeholder API key should be rejected when a real run requires a key")
	}
}

func TestParseTestModesRejectsUnknownMode(t *testing.T) {
	if _, err := parseTestModes("needle,unknown"); err == nil {
		t.Fatal("expected unknown mode to fail")
	}
}

func TestParseTiersSupportsKAndMUnits(t *testing.T) {
	tiers, err := parseTiers("20K, 1.1M, 2M")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{20000, 1100000, 2000000}
	if len(tiers) != len(want) {
		t.Fatalf("unexpected tiers: %#v", tiers)
	}
	for index := range want {
		if tiers[index] != want[index] {
			t.Fatalf("tier %d is %d, want %d", index, tiers[index], want[index])
		}
	}
}

func TestParseTiersRejectsInvalidUnit(t *testing.T) {
	if _, err := parseTiers("1X"); err == nil {
		t.Fatal("invalid tier unit should fail")
	}
}

func TestParseReasoningValues(t *testing.T) {
	if value, err := parseReasoningEffort("HIGH"); err != nil || value != "high" {
		t.Fatalf("unexpected reasoning effort: %q, %v", value, err)
	}
	if value, err := parseReasoningSummary("detailed"); err != nil || value != "detailed" {
		t.Fatalf("unexpected reasoning summary: %q, %v", value, err)
	}
	if _, err := parseReasoningSummary("verbose"); err == nil {
		t.Fatal("invalid reasoning summary should fail")
	}
}

func TestParseReportLanguage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.txt")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "report_language      = en", "report_language = cn", 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReportLanguage != "cn" {
		t.Fatalf("Chinese report language was not loaded: %q", cfg.ReportLanguage)
	}
}

func TestBinaryPrecisionIsConfigurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.txt")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "binary_precision_tokens = 10000", "binary_precision_tokens = 2500", 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BinaryPrecisionTokens != 2500 {
		t.Fatalf("custom binary precision was not loaded: %d", cfg.BinaryPrecisionTokens)
	}
}

func TestChineseEnhancedAutoTestAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.txt")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "enhanced_auto_test      = false", "增强的自动测试 = true", 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EnhancedAutoTest {
		t.Fatal("Chinese enhanced-auto-test key was not recognized")
	}
}
