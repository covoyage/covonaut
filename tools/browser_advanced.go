package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
)

var globalBrowserManagers sync.Map

func RegisterBrowserManager(id string, mgr *BrowserManager) {
	globalBrowserManagers.Store(id, mgr)
}

func UnregisterBrowserManager(id string) {
	globalBrowserManagers.Delete(id)
}

func EmergencyCleanupAll() {
	globalBrowserManagers.Range(func(key, value any) bool {
		if mgr, ok := value.(*BrowserManager); ok {
			mgr.CloseAll()
		}
		return true
	})
	GetSupervisorRegistry().StopAll()
}

func SetupSignalHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		EmergencyCleanupAll()
		os.Exit(0)
	}()
}

type OrphanReaper struct {
	mu          sync.Mutex
	socketDir   string
	reapedCount int
}

func NewOrphanReaper() *OrphanReaper {
	socketDir := os.TempDir()
	return &OrphanReaper{
		socketDir: socketDir,
	}
}

func (r *OrphanReaper) ReapOrphans() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entries, err := os.ReadDir(r.socketDir)
	if err != nil {
		return 0, err
	}

	reaped := 0
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "agent-browser-") {
			continue
		}

		dirPath := filepath.Join(r.socketDir, entry.Name())
		ownerPIDPath := filepath.Join(dirPath, "owner_pid")

		pidData, err := os.ReadFile(ownerPIDPath)
		if err != nil {
			continue
		}

		var ownerPID int
		fmt.Sscanf(strings.TrimSpace(string(pidData)), "%d", &ownerPID)

		if ownerPID > 0 && isProcessAlive(ownerPID) {
			continue
		}

		daemonPIDPath := filepath.Join(dirPath, "daemon_pid")
		if daemonPIDData, err := os.ReadFile(daemonPIDPath); err == nil {
			var daemonPID int
			fmt.Sscanf(strings.TrimSpace(string(daemonPIDData)), "%d", &daemonPID)
			if daemonPID > 0 {
				killProcess(daemonPID)
			}
		}

		os.RemoveAll(dirPath)
		reaped++
	}

	r.reapedCount += reaped
	return reaped, nil
}

func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

func killProcess(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	proc.Signal(syscall.SIGTERM)

	time.Sleep(2 * time.Second)

	if isProcessAlive(pid) {
		proc.Signal(syscall.SIGKILL)
	}
}

func NeedsLightpandaFallback(engine string, snapshot string, screenshotSize int, commandError error) bool {
	if engine != "lightpanda" && engine != "auto" {
		return false
	}

	if commandError != nil {
		return true
	}

	if len(snapshot) < 20 && strings.TrimSpace(snapshot) == "" {
		return true
	}

	if screenshotSize > 0 && screenshotSize < 20480 {
		return true
	}

	return false
}

func AnnotateLightpandaFallback(result map[string]any) {
	result["browser_engine_fallback"] = "chrome"
	result["fallback_warning"] = "Lightpanda returned empty/broken result, automatically retried with Chrome"
}

func RunChromeFallbackCommand(ctx context.Context, command string, args map[string]any, timeout time.Duration) (map[string]any, error) {
	chrome, err := FindChrome()
	if err != nil {
		return nil, fmt.Errorf("chrome not found for fallback: %w", err)
	}
	execPath := chrome.Path

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(execPath),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Headless,
		chromedp.WindowSize(1280, 800),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-sync", true),
	)

	if ua := os.Getenv("BROWSER_USER_AGENT"); ua != "" {
		opts = append(opts, chromedp.Flag("user-agent", ua))
	}
	if proxyURL := os.Getenv("BROWSER_PROXY_URL"); proxyURL != "" {
		opts = append(opts, chromedp.Flag("proxy-server", proxyURL))
		opts = append(opts, chromedp.Flag("proxy-bypass-list", "localhost;127.0.0.1;[::1]"))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	timeoutCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	var result map[string]any

	switch command {
	case "navigate":
		url, _ := args["url"].(string)
		var title string
		if err := chromedp.Run(timeoutCtx,
			chromedp.Navigate(url),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.Title(&title),
		); err != nil {
			return nil, err
		}

		snapshot, err := generateSnapshot(timeoutCtx, false, nil)
		result = map[string]any{
			"url":      url,
			"title":    title,
			"snapshot": snapshot,
		}
		if err != nil {
			result["snapshot_error"] = err.Error()
		}

	case "screenshot":
		var buf []byte
		if err := chromedp.Run(timeoutCtx, chromedp.CaptureScreenshot(&buf)); err != nil {
			return nil, err
		}
		result = map[string]any{
			"screenshot": base64.StdEncoding.EncodeToString(buf),
			"size_bytes": len(buf),
		}

	default:
		return nil, fmt.Errorf("unsupported fallback command: %s", command)
	}

	return result, nil
}

func findChromeExecutable() (string, error) {
	paths := []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/usr/bin/google-chrome",
		"/usr/bin/chromium-browser",
	}

	for _, p := range paths {
		if path, err := exec.LookPath(p); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("chrome/chromium not found in PATH")
}

func ValidatePostRedirectURL(finalURL string, originalURL string, allowPrivate bool, autoLocal bool) error {
	if finalURL == "" {
		return nil
	}

	if isMetadataEndpoint(getHostname(finalURL)) {
		return fmt.Errorf("navigation redirected to cloud metadata endpoint (%s), blocked for security", finalURL)
	}

	if !allowPrivate && !autoLocal && IsPrivateURL(finalURL) {
		return fmt.Errorf("navigation redirected to private/internal address (%s), blocked for security", finalURL)
	}

	return nil
}

func getHostname(rawURL string) string {
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		if u, err := regexp.Compile(`https?://([^/]+)`); err == nil {
			if matches := u.FindStringSubmatch(rawURL); len(matches) > 1 {
				return matches[1]
			}
		}
	}
	return rawURL
}
