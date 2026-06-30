package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type ChromeInfo struct {
	Path        string
	Version     string
	IsChromium  bool
	IsHeadless  bool
}

func FindChrome() (*ChromeInfo, error) {
	if path := os.Getenv("CHROME_PATH"); path != "" {
		if info, err := examineChrome(path); err == nil {
			return info, nil
		}
	}
	if path := os.Getenv("BROWSER_PATH"); path != "" {
		if info, err := examineChrome(path); err == nil {
			return info, nil
		}
	}

	candidates := chromeCandidates()
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				path = resolved
			}
			if info, err := examineChrome(path); err == nil {
				return info, nil
			}
		}
	}

	wellKnown := wellKnownPaths()
	for _, p := range wellKnown {
		if _, err := os.Stat(p); err == nil {
			if info, err := examineChrome(p); err == nil {
				return info, nil
			}
		}
	}

	return nil, fmt.Errorf("chrome/chromium not found - set CHROME_PATH env var or install Chromium")
}

func chromeCandidates() []string {
	names := []string{
		"google-chrome",
		"google-chrome-stable",
		"google-chrome-beta",
		"google-chrome-unstable",
		"chromium",
		"chromium-browser",
		"chrome",
		"chrome-browser",
		"brave",
		"brave-browser",
		"edge",
		"microsoft-edge",
		"microsoft-edge-stable",
		"opera",
		"vivaldi",
		"yandex-browser",
	}
	return names
}

func wellKnownPaths() []string {
	home, _ := os.UserHomeDir()
	paths := []string{}

	switch runtime.GOOS {
	case "darwin":
		paths = append(paths,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome Beta.app/Contents/MacOS/Google Chrome Beta",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Opera.app/Contents/MacOS/Opera",
			"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
		)
		if home != "" {
			paths = append(paths,
				filepath.Join(home, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
				filepath.Join(home, "Applications", "Chromium.app", "Contents", "MacOS", "Chromium"),
				filepath.Join(home, ".cache", "ms-playwright"),
				filepath.Join(home, "Library", "Application Support", "Google", "Chrome"),
			)
		}
	case "linux":
		paths = append(paths,
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium-browser-stable",
			"/snap/bin/chromium",
			"/snap/bin/google-chrome",
			"/opt/google/chrome/chrome",
			"/opt/chromium.org/chromium/chromium",
		)
		if home != "" {
			paths = append(paths,
				filepath.Join(home, ".cache", "ms-playwright"),
				filepath.Join(home, "snap", "chromium", "current", ".local", "share", "app", "chromium", "chrome"),
			)
		}
	case "windows":
		paths = append(paths,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Chromium\Application\chrome.exe`,
			`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		)
		if home != "" {
			paths = append(paths,
				filepath.Join(home, "AppData", "Local", "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(home, "AppData", "Local", "Chromium", "Application", "chrome.exe"),
				filepath.Join(home, "AppData", "Local", "Microsoft", "Edge", "Application", "msedge.exe"),
			)
		}
	}

	if home != "" {
		playwrightDir := filepath.Join(home, ".cache", "ms-playwright")
		if entries, err := os.ReadDir(playwrightDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), "chromium-") {
					chromeBin := filepath.Join(playwrightDir, e.Name(), "chrome-linux", "chrome")
					if runtime.GOOS == "darwin" {
						chromeBin = filepath.Join(playwrightDir, e.Name(), "chrome-mac", "Chromium.app", "Contents", "MacOS", "Chromium")
					} else if runtime.GOOS == "windows" {
						chromeBin = filepath.Join(playwrightDir, e.Name(), "chrome-win", "chrome.exe")
					}
					if _, err := os.Stat(chromeBin); err == nil {
						paths = append([]string{chromeBin}, paths...)
					}
				}
			}
		}
	}

	return paths
}

func examineChrome(path string) (*ChromeInfo, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}

	info := &ChromeInfo{
		Path: path,
	}

	base := strings.ToLower(filepath.Base(path))
	info.IsChromium = strings.Contains(base, "chromium")
	info.IsHeadless = strings.Contains(base, "headless")

	version, _ := chromeVersion(path)
	info.Version = version

	return info, nil
}

func chromeVersion(path string) (string, error) {
	cmd := exec.Command(path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
