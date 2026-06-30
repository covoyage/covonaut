package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type LightpandaManager struct {
	mu          sync.Mutex
	binaryPath  string
	args        []string
	processes   map[string]*LightpandaProcess
	cdpBasePort int
	nextPort    int
}

type LightpandaProcess struct {
	TaskID    string
	CDPURL    string
	Cmd       *exec.Cmd
	Port      int
	StartedAt time.Time
}

func NewLightpandaManager() *LightpandaManager {
	return &LightpandaManager{
		binaryPath:  findLightpandaBinary(),
		processes:   make(map[string]*LightpandaProcess),
		cdpBasePort: 9222,
		nextPort:    9222,
	}
}

func findLightpandaBinary() string {
	paths := []string{
		"lightpanda",
		"/usr/local/bin/lightpanda",
		"/usr/bin/lightpanda",
	}
	for _, p := range paths {
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
	}
	return ""
}

func (lm *LightpandaManager) StartProcess(ctx context.Context, taskID string, headless bool) (*LightpandaProcess, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if existing, ok := lm.processes[taskID]; ok {
		return existing, nil
	}

	if lm.binaryPath == "" {
		return nil, fmt.Errorf("lightpanda binary not found. Install from https://github.com/lightpanda-io/lightpanda")
	}

	port := lm.nextPort
	lm.nextPort++

	args := []string{
		"--cdp-port", fmt.Sprintf("%d", port),
	}

	if headless {
		args = append(args, "--headless")
	}

	cmd := exec.CommandContext(ctx, lm.binaryPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start lightpanda: %w", err)
	}

	cdpURL := fmt.Sprintf("ws://127.0.0.1:%d", port)

	process := &LightpandaProcess{
		TaskID:    taskID,
		CDPURL:    cdpURL,
		Cmd:       cmd,
		Port:      port,
		StartedAt: time.Now(),
	}

	lm.processes[taskID] = process

	if err := lm.waitForCDP(cdpURL, 10*time.Second); err != nil {
		cmd.Process.Kill()
		delete(lm.processes, taskID)
		return nil, fmt.Errorf("lightpanda CDP not ready: %w", err)
	}

	return process, nil
}

func (lm *LightpandaManager) waitForCDP(cdpURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isCDPReady(cdpURL) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for CDP at %s", cdpURL)
}

func isCDPReady(cdpURL string) bool {
	port := extractPortFromCDPURL(cdpURL)
	if port == 0 {
		return false
	}

	conn, err := exec.Command("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", fmt.Sprintf("http://127.0.0.1:%d/json/version", port)).Output()
	if err != nil {
		return false
	}

	return strings.Contains(string(conn), "200")
}

func extractPortFromCDPURL(url string) int {
	re := regexp.MustCompile(`:(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) < 2 {
		return 0
	}
	var port int
	fmt.Sscanf(matches[1], "%d", &port)
	return port
}

func (lm *LightpandaManager) StopProcess(taskID string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if proc, ok := lm.processes[taskID]; ok {
		if proc.Cmd != nil && proc.Cmd.Process != nil {
			proc.Cmd.Process.Kill()
		}
		delete(lm.processes, taskID)
	}
}

func (lm *LightpandaManager) StopAll() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for id, proc := range lm.processes {
		if proc.Cmd != nil && proc.Cmd.Process != nil {
			proc.Cmd.Process.Kill()
		}
		delete(lm.processes, id)
	}
}

type SessionRecorder struct {
	mu          sync.Mutex
	isRecording bool
	frames      []Frame
	outputPath  string
	frameChan   chan Frame
	doneChan    chan struct{}
}

type Frame struct {
	Data      []byte
	Timestamp time.Time
	Format    string
}

func NewSessionRecorder(outputDir string) *SessionRecorder {
	return &SessionRecorder{
		outputPath: outputDir,
		frameChan:  make(chan Frame, 100),
		doneChan:   make(chan struct{}),
	}
}

func (sr *SessionRecorder) StartRecording(ctx context.Context, sessionID string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.isRecording {
		return fmt.Errorf("already recording")
	}

	sr.isRecording = true
	sr.frames = nil

	go sr.processFrames(ctx, sessionID)

	return nil
}

func (sr *SessionRecorder) processFrames(ctx context.Context, sessionID string) {
	defer func() {
		sr.mu.Lock()
		sr.isRecording = false
		sr.mu.Unlock()
		close(sr.doneChan)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-sr.frameChan:
			if !ok {
				return
			}
			sr.frames = append(sr.frames, frame)
		}
	}
}

func (sr *SessionRecorder) AddFrame(data []byte, format string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if !sr.isRecording {
		return
	}

	select {
	case sr.frameChan <- Frame{
		Data:      data,
		Timestamp: time.Now(),
		Format:    format,
	}:
	default:
	}
}

func (sr *SessionRecorder) StopRecording() (string, error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if !sr.isRecording {
		return "", fmt.Errorf("not recording")
	}

	close(sr.frameChan)
	<-sr.doneChan

	if len(sr.frames) == 0 {
		return "", fmt.Errorf("no frames captured")
	}

	outputFile := filepath.Join(sr.outputPath, fmt.Sprintf("recording_%d.webm", time.Now().Unix()))

	if err := sr.saveAsWebM(outputFile); err != nil {
		return "", fmt.Errorf("failed to save recording: %w", err)
	}

	return outputFile, nil
}

func (sr *SessionRecorder) saveAsWebM(outputPath string) error {
	if len(sr.frames) == 0 {
		return fmt.Errorf("no frames to save")
	}

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found, required for recording: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "browser-recording")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	for i, frame := range sr.frames {
		framePath := filepath.Join(tmpDir, fmt.Sprintf("frame_%06d.png", i))
		if err := os.WriteFile(framePath, frame.Data, 0644); err != nil {
			return err
		}
	}

	cmd := exec.Command(ffmpegPath,
		"-framerate", "10",
		"-pattern_type", "glob",
		"-i", filepath.Join(tmpDir, "frame_*.png"),
		"-c:v", "libvpx-vp9",
		"-pix_fmt", "yuv420p",
		"-y",
		outputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %v\n%s", err, string(output))
	}

	return nil
}

func (sr *SessionRecorder) IsRecording() bool {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return sr.isRecording
}

func (sr *SessionRecorder) FrameCount() int {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return len(sr.frames)
}
