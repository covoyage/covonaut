package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type CDPRecorder struct {
	mu          sync.Mutex
	isRecording bool
	ctx         context.Context
	cancel      context.CancelFunc
	outputPath  string
	sessionID   string
	frameCount  int
	startedAt   time.Time
	webmFile    *os.File
}

func NewCDPRecorder(outputDir string) *CDPRecorder {
	return &CDPRecorder{
		outputPath: outputDir,
	}
}

func (r *CDPRecorder) StartRecording(ctx context.Context, sessionID string, browserCtx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.isRecording {
		return fmt.Errorf("already recording")
	}

	if err := os.MkdirAll(r.outputPath, 0755); err != nil {
		return fmt.Errorf("failed to create recording dir: %w", err)
	}

	filename := fmt.Sprintf("recording_%s_%d.webm", sessionID, time.Now().Unix())
	filepath := filepath.Join(r.outputPath, filename)

	f, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create recording file: %w", err)
	}

	recCtx, recCancel := context.WithCancel(browserCtx)

	r.webmFile = f
	r.sessionID = sessionID
	r.isRecording = true
	r.frameCount = 0
	r.startedAt = time.Now()
	r.ctx = recCtx
	r.cancel = recCancel

	go r.captureFrames(recCtx)

	return nil
}

func (r *CDPRecorder) captureFrames(ctx context.Context) {
	chromedp.ListenTarget(ctx, func(ev any) {
		if !r.isRecording {
			return
		}

		switch e := ev.(type) {
		case *page.EventScreencastFrame:
			r.mu.Lock()
			if !r.isRecording {
				r.mu.Unlock()
				return
			}

			data, err := base64.StdEncoding.DecodeString(e.Data)
			if err != nil {
				r.mu.Unlock()
				return
			}

			if _, err := r.webmFile.Write(data); err != nil {
				r.mu.Unlock()
				return
			}

			r.frameCount++
			r.mu.Unlock()

			if err := page.ScreencastFrameAck(e.SessionID).Do(ctx); err != nil {
				return
			}
		}
	})

	if err := page.StartScreencast().
		WithFormat(page.ScreencastFormatJpeg).
		WithQuality(60).
		WithEveryNthFrame(1).
		Do(ctx); err != nil {
		return
	}

	<-ctx.Done()

	page.StopScreencast().Do(ctx)
}

func (r *CDPRecorder) StopRecording() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.isRecording {
		return "", fmt.Errorf("not recording")
	}

	if r.cancel != nil {
		r.cancel()
	}

	r.isRecording = false

	if r.webmFile != nil {
		r.webmFile.Close()
	}

	outputPath := r.webmFile.Name()
	return outputPath, nil
}

func (r *CDPRecorder) IsRecording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.isRecording
}

func (r *CDPRecorder) FrameCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frameCount
}

func (r *CDPRecorder) Duration() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.isRecording {
		return time.Since(r.startedAt)
	}
	return time.Since(r.startedAt)
}
