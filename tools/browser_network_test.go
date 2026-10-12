package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/covoyage/covonaut/agentcore"
)

func TestBrowserNetworkCaptureCorrelation(t *testing.T) {
	capture := newBrowserNetworkCapture("session-one", BrowserNetworkConfig{AllowURL: func(raw string) bool { return raw == "https://example.test/a" }})
	capture.handle(&network.EventRequestWillBeSent{RequestID: "ignored", Request: &network.Request{URL: "https://outside.test/"}})
	capture.handle(&network.EventRequestWillBeSent{RequestID: "one", Request: &network.Request{URL: "https://example.test/a", Method: "POST", HasPostData: true, PostDataEntries: []*network.PostDataEntry{{Bytes: base64.StdEncoding.EncodeToString([]byte(`{"id":1}`))}}, Headers: network.Headers{"Content-Type": "application/json"}}})
	capture.handle(&network.EventResponseReceived{RequestID: "one", Response: &network.Response{Status: 200, Protocol: "h2"}})
	capture.handle(&network.EventLoadingFinished{RequestID: "one", EncodedDataLength: 9000})
	item := <-capture.queue
	if item.exchange.SessionID != "session-one" || item.exchange.StatusCode != 200 || item.exchange.Request != `{"id":1}` || item.exchange.RequestIncomplete || !item.exchange.HeadersIncomplete || !item.exchange.ResponseIncomplete || len(capture.pending) != 0 {
		t.Fatalf("incorrect evidence: %+v", item)
	}
}

func TestBrowserNetworkCaptureRedirectAndBounds(t *testing.T) {
	capture := newBrowserNetworkCapture("session", BrowserNetworkConfig{})
	capture.handle(&network.EventRequestWillBeSent{RequestID: "one", Request: &network.Request{URL: "https://example.test/a", Method: "GET"}})
	capture.handle(&network.EventRequestWillBeSent{RequestID: "one", RedirectResponse: &network.Response{Status: 302}, Request: &network.Request{URL: "https://example.test/b", Method: "POST", HasPostData: true}})
	prior := <-capture.queue
	if prior.exchange.URL != "https://example.test/a" || prior.exchange.StatusCode != 302 || !prior.exchange.ResponseIncomplete {
		t.Fatalf("redirect overwritten: %+v", prior)
	}
	capture.handle(&network.EventLoadingFailed{RequestID: "one", ErrorText: "failed"})
	failed := <-capture.queue
	if !failed.exchange.RequestIncomplete || failed.exchange.Error != "failed" {
		t.Fatal("missing data reported complete", failed)
	}
	for index := 0; index < 100; index++ {
		capture.emit(browserNetworkItem{})
	}
	if len(capture.queue) != 64 || capture.dropped.Load() != 36 {
		t.Fatal("queue not bounded")
	}
}

func TestBrowserNetworkExtraHeadersAreConservative(t *testing.T) {
	for _, early := range []bool{false, true} {
		capture := newBrowserNetworkCapture("session", BrowserNetworkConfig{})
		extra := &network.EventRequestWillBeSentExtraInfo{RequestID: "one", Headers: network.Headers{"Cookie": "session=test"}}
		if early {
			capture.handle(extra)
		}
		capture.handle(&network.EventRequestWillBeSent{RequestID: "one", Request: &network.Request{URL: "https://example.test/a", Method: "GET"}})
		if !early {
			capture.handle(extra)
		}
		if capture.pending["one"].HeadersIncomplete != early {
			t.Fatal("unmatched header event reported complete")
		}
		extra.Headers["large"] = strings.Repeat("x", 2100)
		capture.handle(extra)
		if !capture.pending["one"].HeadersIncomplete {
			t.Fatal("truncated headers reported complete")
		}
	}
}

func TestBrowserNetworkRealSession(t *testing.T) {
	t.Setenv("AGENT_BROWSER_ENABLED", "false")
	t.Setenv("AGENT_BROWSER_PATH", "")
	if _, err := FindChrome(); err != nil {
		t.Skip("Chrome unavailable: ", err)
	}
	exchanges := make(chan BrowserNetworkExchange, 32)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/page":
			writer.Header().Set("Content-Type", "text/html")
			writer.Write([]byte(`<html><head><link rel="icon" href="data:,"></head><body>test<script>fetch('/data',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id:17})});fetch('/redirect');</script></body></html>`))
		case "/redirect":
			writer.Header().Set("Location", "/final")
			writer.WriteHeader(http.StatusFound)
		default:
			writer.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()
	first, firstManager := newBrowserTool(&BrowserToolConfig{Headless: true, AllowPrivate: true, Engine: "chrome", NetworkObservation: &BrowserNetworkConfig{
		AllowURL:   func(raw string) bool { return strings.HasPrefix(raw, server.URL+"/") },
		OnExchange: func(exchange BrowserNetworkExchange) { exchanges <- exchange },
	}})
	t.Cleanup(firstManager.CloseAll)
	args, _ := json.Marshal(map[string]string{"action": "navigate", "url": server.URL + "/page"})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := first.Func(ctx, args); err != nil {
		t.Fatal(err)
	}
	seen := map[string]BrowserNetworkExchange{}
	for len(seen) < 4 {
		select {
		case exchange := <-exchanges:
			seen[strings.TrimPrefix(exchange.URL, server.URL)] = exchange
		case <-ctx.Done():
			t.Fatalf("missing captured requests: %+v", seen)
		}
	}
	if seen["/redirect"].StatusCode != 302 || seen["/final"].StatusCode != 200 || seen["/data"].Method != "POST" || seen["/data"].Request != `{"id":17}` || seen["/data"].Response != `{"ok":true}` {
		t.Fatalf("incorrect actual network evidence: %+v", seen)
	}
	if requests.Load() != 4 {
		t.Fatalf("observation added target requests: %d", requests.Load())
	}
	second, secondManager := newBrowserTool(&BrowserToolConfig{Headless: true, AllowPrivate: true, Engine: "chrome"})
	t.Cleanup(secondManager.CloseAll)
	args, _ = json.Marshal(map[string]string{"action": "navigate", "url": server.URL + "/second"})
	if _, err := second.Func(ctx, args); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]string{"action": "evaluate", "expression": "location.pathname"})
	result, err := first.Func(ctx, args)
	encoded, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), "/page") || strings.Contains(string(encoded), "/second") {
		t.Fatalf("tool instances shared a session: %s err=%v", encoded, err)
	}
	select {
	case exchange := <-exchanges:
		t.Fatalf("other tool traffic reached observer: %+v", exchange)
	default:
	}
}

func TestBrowserNetworkShutdownReportsLoss(t *testing.T) {
	reported := make(chan error, 2)
	finished := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	capture := newBrowserNetworkCapture("session", BrowserNetworkConfig{OnError: func(err error) { reported <- err }})
	capture.dropped.Store(3)
	capture.pending["one"] = BrowserNetworkExchange{}
	cancel()
	go func() {
		capture.run(ctx)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("network worker did not stop")
	}
	if len(reported) != 2 {
		t.Fatal("dropped or unfinished records were not reported")
	}
}

func TestBrowserExtensionDisposeClosesOwnedSessions(t *testing.T) {
	ext := NewExtension(ExtensionConfig{Browser: &BrowserToolConfig{Headless: true}})
	core := agentcore.New(agentcore.Config{})
	defer core.Close()
	if err := ext.Init(context.Background(), core); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ext.browserManager.sessions["test"] = &BrowserSession{ctx: ctx, cancel: cancel}
	if err := ext.Dispose(); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || len(ext.browserManager.sessions) != 0 {
		t.Fatal("extension did not close its own sessions")
	}
}
