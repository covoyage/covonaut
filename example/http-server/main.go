package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/chatcompat"
	"github.com/covoyage/covonaut/server"
	"github.com/covoyage/covonaut/session"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	apiKey, model, token := os.Getenv("OPENAI_API_KEY"), os.Getenv("AGENT_MODEL"), os.Getenv("CHAT_API_TOKEN")
	if apiKey == "" || model == "" || token == "" {
		return errors.New("set OPENAI_API_KEY, AGENT_MODEL, and CHAT_API_TOKEN")
	}
	directory := os.Getenv("SESSION_DIR")
	if directory == "" {
		directory = "./sessions"
	}
	sessions, err := session.NewFileStore(directory)
	if err != nil {
		return err
	}
	cfg := agentcore.NewConfig(
		agentcore.WithModel(model),
		agentcore.WithProvider(chatcompat.New(chatcompat.Config{
			APIKey: apiKey, BaseURL: os.Getenv("OPENAI_BASE_URL"),
		})),
		agentcore.WithStore(session.NewAgentStore(sessions, ".")),
		agentcore.WithStreaming(true),
		agentcore.WithMaxTurns(10),
	)
	cfg.DisableSkillRegistryAPI = true
	cfg.DisableSkillReloadAPI = true
	application := server.New(cfg)
	defer application.Close()
	application.SetMaxRequestBodyBytes(1 << 20)
	handler := application.Handler()
	protected := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") ||
			subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(writer, request)
	})
	httpServer := &http.Server{
		Addr: "127.0.0.1:8080", Handler: protected,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	finished := make(chan error, 1)
	go func() { finished <- httpServer.ListenAndServe() }()
	fmt.Println("Chat API: http://127.0.0.1:8080")
	select {
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}