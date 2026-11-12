package preview

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"context-LLM/internal/mdrender"
)

const previewLifetime = 5 * time.Minute

func Resolve(target, configPath string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", errors.New("preview target is required")
	}
	if target != "latest" {
		path, err := filepath.Abs(target)
		if err != nil {
			return "", err
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			path = filepath.Join(path, "RESULTS.md")
		}
		return validateReport(path)
	}

	runsDir := filepath.Join(filepath.Dir(configPath), "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("read runs directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		left, leftErr := entries[i].Info()
		right, rightErr := entries[j].Info()
		if leftErr != nil || rightErr != nil {
			return entries[i].Name() > entries[j].Name()
		}
		return left.ModTime().After(right.ModTime())
	})
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path, err := validateReport(filepath.Join(runsDir, entry.Name(), "RESULTS.md"))
		if err == nil {
			return path, nil
		}
	}
	return "", errors.New("no RESULTS.md found under runs")
}

func Run(path string) error {
	path, err := validateReport(path)
	if err != nil {
		return err
	}
	markdown, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	rendered := mdrender.Render(markdown)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start local report server: %w", err)
	}
	server := &http.Server{
		Handler:           reportHandler(rendered),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverError := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverError <- err
		}
	}()

	address := listener.Addr().String()
	reportURL := "http://" + address + "/report"
	absolutePath, _ := filepath.Abs(path)
	fmt.Printf("Markdown file: %s\n", absolutePath)

	if err := openBrowser(reportURL); err != nil {
		shutdown(server)
		fmt.Printf("Browser unavailable: %v\n", err)
		fmt.Printf("View the Markdown file directly: %s\n", absolutePath)
		return nil
	}

	fmt.Printf("Report opened in your browser: %s\n", reportURL)
	fmt.Printf("Press Ctrl+C to stop, or the server will close automatically after %s.\n", previewLifetime)

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result, err := waitForPreview(signalContext.Done(), serverError, previewLifetime)
	if err != nil {
		return err
	}
	switch result {
	case previewWaitTimedOut:
		if err := shutdown(server); err != nil {
			return err
		}
		fmt.Printf("Preview stopped after %s: %s\n", previewLifetime, absolutePath)
		return nil
	case previewWaitServerFailed:
		return errors.New("local preview server failed")
	default:
		return shutdown(server)
	}
}

type previewWaitResult int

const (
	previewWaitStopped previewWaitResult = iota
	previewWaitTimedOut
	previewWaitServerFailed
)

func waitForPreview(done <-chan struct{}, serverError <-chan error, timeout time.Duration) (previewWaitResult, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return previewWaitStopped, nil
	case <-timer.C:
		return previewWaitTimedOut, nil
	case err := <-serverError:
		return previewWaitServerFailed, err
	}
}

func reportHandler(content []byte) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" && request.URL.Path != "/report" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = writer.Write(content)
	})
}

func validateReport(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read report: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("report path is a directory: %s", path)
	}
	return path, nil
}

func shutdown(server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func openBrowser(target string) error {
	var name string
	var arguments []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
		arguments = []string{target}
	case "windows":
		name = "cmd"
		arguments = []string{"/c", "start", "", target}
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no graphical display detected")
		}
		name = "xdg-open"
		arguments = []string{target}
	default:
		return fmt.Errorf("automatic browser opening is unsupported on %s", runtime.GOOS)
	}
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is not available", name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	if err := command.Run(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}
