package preview

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveLatestReport(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.txt")
	reportPath := filepath.Join(root, "runs", "run_1", "RESULTS.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, []byte("# Report"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve("latest", configPath)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != reportPath {
		t.Fatalf("resolved %q, want %q", resolved, reportPath)
	}
}

func TestReportHandlerSecurityHeaders(t *testing.T) {
	handler := reportHandler([]byte("<html>ok</html>"))
	request := httptest.NewRequest(http.MethodGet, "/report", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	if !strings.Contains(recorder.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("missing CSP header")
	}
}

func TestWaitForPreviewLifecycle(t *testing.T) {
	done := make(chan struct{})
	serverError := make(chan error, 1)
	result, err := waitForPreview(done, serverError, 10*time.Millisecond)
	if err != nil || result != previewWaitTimedOut {
		t.Fatalf("unexpected timeout result: %v, %v", result, err)
	}

	close(done)
	result, err = waitForPreview(done, serverError, time.Second)
	if err != nil || result != previewWaitStopped {
		t.Fatalf("unexpected stop result: %v, %v", result, err)
	}

	serverError <- errors.New("server failed")
	result, err = waitForPreview(make(chan struct{}), serverError, time.Second)
	if err == nil || result != previewWaitServerFailed {
		t.Fatalf("unexpected server failure result: %v, %v", result, err)
	}
}
