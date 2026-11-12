package corpus

import (
	"strings"
	"testing"
)

func TestTestModesGenerateExpectedCases(t *testing.T) {
	cases := benchmarkCases(2026110806, 13, []string{"needle", "multi_hop", "conflict", "adversarial"})
	if len(cases) != 13 || cases[12].Mode != "abstention" {
		t.Fatalf("unexpected case count or abstention case: %#v", cases)
	}
	counts := map[string]int{}
	for _, testCase := range cases {
		counts[testCase.Mode]++
	}
	for _, mode := range []string{"needle", "multi_hop", "conflict", "adversarial"} {
		if counts[mode] != 3 {
			t.Fatalf("mode %s received %d cases, want 3", mode, counts[mode])
		}
	}
}

func TestNeedleOnlyConfiguration(t *testing.T) {
	cases := benchmarkCases(2026110807, 13, []string{"needle"})
	for _, testCase := range cases[:12] {
		if testCase.Mode != "needle" {
			t.Fatalf("needle-only configuration generated %s", testCase.Mode)
		}
	}
}

func TestAdversarialUsesAuthorityRulesWithoutExplicitSpoilers(t *testing.T) {
	cases := benchmarkCases(2026110808, 13, []string{"adversarial"})
	for _, testCase := range cases {
		if testCase.Mode != "adversarial" {
			continue
		}
		joined := strings.Join(testCase.Records, "")
		if !strings.Contains(joined, "AUTHORITATIVE") || !strings.Contains(joined, "ADVISORY") || !strings.Contains(joined, "EARLIER REVISION") {
			t.Fatalf("adversarial records do not contain all authority levels: %q", joined)
		}
		if strings.Contains(strings.ToLower(joined), "unverified") ||
			strings.Contains(strings.ToLower(joined), "not authoritative") ||
			strings.Contains(strings.ToLower(joined), "invalid") {
			t.Fatalf("adversarial records contain an explicit spoiler: %q", joined)
		}
	}
}

func TestConflictEvidenceRequiresBothSides(t *testing.T) {
	cases := benchmarkCases(2026110809, 13, []string{"conflict"})
	found := false
	for _, testCase := range cases {
		if testCase.Mode != "conflict" {
			continue
		}
		found = true
		if !strings.Contains(testCase.Answer.Evidence, "CF-OLD-") || !strings.Contains(testCase.Answer.Evidence, "CF-NEW-") {
			t.Fatalf("conflict evidence does not require both records: %#v", testCase.Answer)
		}
	}
	if !found {
		t.Fatal("no conflict case generated")
	}
}

func TestModeAssignmentIsRandomized(t *testing.T) {
	first := benchmarkCases(2026110810, 21, []string{"needle", "multi_hop", "conflict", "adversarial"})
	second := benchmarkCases(2026110811, 21, []string{"needle", "multi_hop", "conflict", "adversarial"})
	same := true
	for index := range first {
		if first[index].Mode != second[index].Mode {
			same = false
			break
		}
	}
	if same {
		t.Fatal("mode assignment did not vary with the random seed")
	}
}
