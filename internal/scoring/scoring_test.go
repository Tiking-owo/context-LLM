package scoring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"context-LLM/internal/corpus"
)

func TestGeneratedCorpusScoresPerfectResponse(t *testing.T) {
	root := t.TempDir()
	manifest, err := corpus.Generate(root, 13000, 2026110803, 1, 13, []string{"needle", "multi_hop", "conflict", "adversarial"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.File != "context_13k.txt" {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	answers := readAnswers(t, root)
	if len(answers) != 13 {
		t.Fatalf("got %d answers, want 13", len(answers))
	}
	response, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	score := Response(root, map[string]any{"_extracted_text": string(response)}, 0)
	if score.State != "passed" || score.AnswerHits != 13 || score.EvidenceHits != 13 {
		t.Fatalf("unexpected score: %#v", score)
	}
}

func TestFiftyAnchorsGenerateAndScore(t *testing.T) {
	root := t.TempDir()
	manifest, err := corpus.Generate(root, 13000, 2026110804, 1, 50, []string{"needle", "multi_hop", "conflict", "adversarial"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.AnchorCount != 50 {
		t.Fatalf("unexpected anchor count in manifest: %#v", manifest)
	}
	answers := readAnswers(t, root)
	if len(answers) != 50 {
		t.Fatalf("got %d answers, want 50", len(answers))
	}
	response, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	score := Response(root, map[string]any{"_extracted_text": string(response)}, 0)
	if score.State != "passed" || score.QuestionCount != 50 {
		t.Fatalf("unexpected 50-anchor score: %#v", score)
	}
}

func TestEvidenceToleranceRequiresCorrectAnswers(t *testing.T) {
	root := t.TempDir()
	if _, err := corpus.Generate(root, 13000, 2026110805, 1, 13, []string{"needle", "multi_hop", "conflict", "adversarial"}); err != nil {
		t.Fatal(err)
	}
	answers := readAnswers(t, root)
	badEvidence := answers["Q01"]
	badEvidence.Evidence = "wrong-record"
	answers["Q01"] = badEvidence
	response, _ := json.Marshal(answers)
	if score := Response(root, map[string]any{"_extracted_text": string(response)}, 0); score.State != "scored_failure" {
		t.Fatalf("zero tolerance unexpectedly passed: %#v", score)
	}
	if score := Response(root, map[string]any{"_extracted_text": string(response)}, 1); score.State != "passed" {
		t.Fatalf("one evidence miss should pass at tolerance 1: %#v", score)
	}
	badAnswer := answers["Q02"]
	badAnswer.Answer = "wrong-answer"
	answers["Q02"] = badAnswer
	response, _ = json.Marshal(answers)
	if score := Response(root, map[string]any{"_extracted_text": string(response)}, 50); score.State != "scored_failure" {
		t.Fatalf("incorrect answer unexpectedly passed: %#v", score)
	}
}

func TestScoreBreaksDownByModeAndToleranceIsPerMode(t *testing.T) {
	root := t.TempDir()
	if _, err := corpus.Generate(root, 13000, 2026110812, 1, 13, []string{"needle", "multi_hop", "conflict", "adversarial"}); err != nil {
		t.Fatal(err)
	}
	answers := readAnswers(t, root)
	mutatedModes := map[string]bool{}
	for qid, answer := range answers {
		if !mutatedModes[answer.Mode] {
			answer.Evidence = "wrong-record"
			answers[qid] = answer
			mutatedModes[answer.Mode] = true
		}
	}
	response, _ := json.Marshal(answers)
	score := Response(root, map[string]any{"_extracted_text": string(response)}, 1)
	if score.State != "passed" {
		t.Fatalf("one evidence miss per mode should pass at per-mode tolerance 1: %#v", score)
	}
	if len(score.ByMode) != 5 {
		t.Fatalf("expected five mode buckets, got %#v", score.ByMode)
	}
	for mode, modeScore := range score.ByMode {
		if modeScore.EvidenceHits != modeScore.QuestionCount-1 {
			t.Fatalf("mode %s did not receive one tolerated evidence miss: %#v", mode, modeScore)
		}
	}

	for qid, answer := range answers {
		answer.Evidence = "still-wrong"
		answers[qid] = answer
	}
	response, _ = json.Marshal(answers)
	score = Response(root, map[string]any{"_extracted_text": string(response)}, 1)
	if score.State != "scored_failure" {
		t.Fatalf("two evidence misses per mode should fail at tolerance 1: %#v", score)
	}
}

func TestModeSpecificAnswerAndEvidenceFailures(t *testing.T) {
	root := t.TempDir()
	if _, err := corpus.Generate(root, 13000, 2026110813, 1, 13, []string{"needle", "multi_hop", "conflict", "adversarial"}); err != nil {
		t.Fatal(err)
	}
	answers := readAnswers(t, root)
	for _, mode := range []string{"needle", "multi_hop", "conflict", "adversarial", "abstention"} {
		qid := firstQuestionForMode(t, answers, mode)

		answerFailure := cloneAnswers(answers)
		badAnswer := answerFailure[qid]
		badAnswer.Answer = "WRONG-ANSWER"
		answerFailure[qid] = badAnswer
		response, _ := json.Marshal(answerFailure)
		score := Response(root, map[string]any{"_extracted_text": string(response)}, 0)
		if score.State != "scored_failure" || score.ByMode[mode].AnswerHits != score.ByMode[mode].QuestionCount-1 {
			t.Fatalf("%s answer failure not isolated: %#v", mode, score)
		}

		evidenceFailure := cloneAnswers(answers)
		badEvidence := evidenceFailure[qid]
		badEvidence.Evidence = "WRONG-EVIDENCE"
		evidenceFailure[qid] = badEvidence
		response, _ = json.Marshal(evidenceFailure)
		score = Response(root, map[string]any{"_extracted_text": string(response)}, 0)
		if score.State != "scored_failure" || score.ByMode[mode].EvidenceHits != score.ByMode[mode].QuestionCount-1 {
			t.Fatalf("%s evidence failure not isolated: %#v", mode, score)
		}
	}
}

func TestDecodeAnswersRepairsCommonJSONDamage(t *testing.T) {
	direct, repaired, err := decodeAnswers(`{"Q01":{"answer":"value","evidence":"AR-01"}}`)
	if err != nil || repaired || direct["Q01"].Answer != "value" {
		t.Fatalf("direct JSON should parse without repair: %#v, %v, %v", direct, repaired, err)
	}

	trailing, repaired, err := decodeAnswers(`{"Q01":{"answer":"value","evidence":"AR-01"},}`)
	if err != nil || !repaired || trailing["Q01"].Answer != "value" {
		t.Fatalf("trailing comma was not repaired: %#v, %v, %v", trailing, repaired, err)
	}

	missing, repaired, err := decodeAnswers("Result:\n```json\n{\"Q01\":{\"answer\":\"value\",\"evidence\":\"AR-01\"}")
	if err != nil || !repaired || missing["Q01"].Evidence != "AR-01" {
		t.Fatalf("missing delimiter was not repaired: %#v, %v, %v", missing, repaired, err)
	}

	if _, _, err := decodeAnswers("not json"); err == nil {
		t.Fatal("invalid output should remain invalid")
	}
}

func TestRequestFailurePreservesProviderError(t *testing.T) {
	root := t.TempDir()
	if _, err := corpus.Generate(root, 13000, 2026110814, 1, 13, []string{"needle", "multi_hop", "conflict", "adversarial"}); err != nil {
		t.Fatal(err)
	}
	score := Response(root, map[string]any{
		"http_status": 429,
		"error":       `{"error":{"message":"usage limit reached","code":"usage_limit_reached"}}`,
	}, 0)
	if score.State != "request_failed" || !strings.Contains(score.Detail, "usage limit reached") {
		t.Fatalf("provider error detail missing: %#v", score)
	}
}

func readAnswers(t *testing.T, root string) map[string]corpus.Answer {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "answers.private.json"))
	if err != nil {
		t.Fatal(err)
	}
	answers := map[string]corpus.Answer{}
	if err := json.Unmarshal(data, &answers); err != nil {
		t.Fatal(err)
	}
	return answers
}

func cloneAnswers(answers map[string]corpus.Answer) map[string]corpus.Answer {
	cloned := make(map[string]corpus.Answer, len(answers))
	for qid, answer := range answers {
		cloned[qid] = answer
	}
	return cloned
}

func firstQuestionForMode(t *testing.T, answers map[string]corpus.Answer, mode string) string {
	t.Helper()
	for qid, answer := range answers {
		if answer.Mode == mode {
			return qid
		}
	}
	t.Fatalf("no question found for mode %q", mode)
	return ""
}
