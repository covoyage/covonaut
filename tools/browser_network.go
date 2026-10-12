package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type BrowserNetworkConfig struct {
	AllowURL   func(string) bool
	OnExchange func(BrowserNetworkExchange)
	OnError    func(error)
}

type BrowserNetworkExchange struct {
	SessionID          string
	RequestID          string
	Method             string
	URL                string
	StatusCode         int
	Protocol           string
	RequestHeaders     map[string]string
	Request            string
	Response           string
	HeadersIncomplete  bool
	RequestIncomplete  bool
	ResponseIncomplete bool
	Error              string
}

type browserNetworkItem struct {
	exchange     BrowserNetworkExchange
	requestID    network.RequestID
	readResponse bool
}

type browserNetworkCapture struct {
	mu         sync.Mutex
	config     BrowserNetworkConfig
	sessionID  string
	pending    map[network.RequestID]BrowserNetworkExchange
	redirected map[network.RequestID]bool
	queue      chan browserNetworkItem
	dropped    atomic.Uint64
}

func (session *BrowserSession) startNetworkCapture(config *BrowserNetworkConfig) error {
	if config == nil || config.OnExchange == nil {
		return nil
	}
	if session.ctx == nil {
		return fmt.Errorf("network observation is unsupported by backend %s", session.backendType)
	}
	capture := newBrowserNetworkCapture(session.sessionID, *config)
	chromedp.ListenTarget(session.ctx, capture.handle)
	ctx, cancel := context.WithTimeout(session.ctx, 15*time.Second)
	err := chromedp.Run(ctx, network.Enable().WithMaxTotalBufferSize(1<<20).WithMaxResourceBufferSize(64<<10).WithMaxPostDataSize(4000))
	cancel()
	if err != nil {
		return fmt.Errorf("enable browser network observation: %w", err)
	}
	go capture.run(session.ctx)
	return nil
}

func newBrowserNetworkCapture(sessionID string, config BrowserNetworkConfig) *browserNetworkCapture {
	return &browserNetworkCapture{config: config, sessionID: sessionID, pending: make(map[network.RequestID]BrowserNetworkExchange), redirected: make(map[network.RequestID]bool), queue: make(chan browserNetworkItem, 64)}
}

func browserNetworkHeaders(headers network.Headers) (map[string]string, bool) {
	result := make(map[string]string)
	incomplete, total := false, 0
	for name, value := range headers {
		text, valid := value.(string)
		if !valid || total+len(name)+len(text)+4 > 2000 {
			incomplete = true
			continue
		}
		total += len(name) + len(text) + 4
		result[name] = text
	}
	return result, incomplete
}

func (capture *browserNetworkCapture) emit(item browserNetworkItem) {
	select {
	case capture.queue <- item:
	default:
		capture.dropped.Add(1)
	}
}

func (capture *browserNetworkCapture) handle(event any) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	switch message := event.(type) {
	case *network.EventRequestWillBeSent:
		if prior, found := capture.pending[message.RequestID]; found {
			delete(capture.pending, message.RequestID)
			if message.RedirectResponse != nil {
				prior.StatusCode = int(message.RedirectResponse.Status)
				prior.Protocol = message.RedirectResponse.Protocol
				prior.HeadersIncomplete = true
			}
			prior.ResponseIncomplete = true
			capture.emit(browserNetworkItem{exchange: prior})
		}
		request := message.Request
		if request == nil || (capture.config.AllowURL != nil && !capture.config.AllowURL(request.URL)) {
			return
		}
		if len(capture.pending) >= 128 {
			capture.dropped.Add(1)
			return
		}
		if message.RedirectResponse != nil {
			capture.redirected[message.RequestID] = true
		}
		exchange := BrowserNetworkExchange{SessionID: capture.sessionID, RequestID: string(message.RequestID), Method: request.Method, URL: request.URL, HeadersIncomplete: true, RequestHeaders: make(map[string]string)}
		exchange.RequestHeaders, _ = browserNetworkHeaders(request.Headers)
		if request.HasPostData {
			exchange.RequestIncomplete = len(request.PostDataEntries) == 0
			for _, entry := range request.PostDataEntries {
				if entry == nil || len(entry.Bytes) > 6000 {
					exchange.RequestIncomplete = true
					break
				}
				data, err := base64.StdEncoding.DecodeString(entry.Bytes)
				if err != nil || len(exchange.Request)+len(data) > 4000 {
					exchange.RequestIncomplete = true
					break
				}
				exchange.Request += string(data)
			}
		}
		capture.pending[message.RequestID] = exchange
	case *network.EventRequestWillBeSentExtraInfo:
		if capture.redirected[message.RequestID] {
			return
		}
		if exchange, found := capture.pending[message.RequestID]; found {
			exchange.RequestHeaders, exchange.HeadersIncomplete = browserNetworkHeaders(message.Headers)
			capture.pending[message.RequestID] = exchange
		}
	case *network.EventResponseReceived:
		if exchange, found := capture.pending[message.RequestID]; found && message.Response != nil {
			exchange.StatusCode = int(message.Response.Status)
			exchange.Protocol = message.Response.Protocol
			capture.pending[message.RequestID] = exchange
		}
	case *network.EventLoadingFinished:
		delete(capture.redirected, message.RequestID)
		if exchange, found := capture.pending[message.RequestID]; found {
			delete(capture.pending, message.RequestID)
			read := message.EncodedDataLength <= 8000 && exchange.StatusCode != 0
			exchange.ResponseIncomplete = !read
			capture.emit(browserNetworkItem{exchange: exchange, requestID: message.RequestID, readResponse: read})
		}
	case *network.EventLoadingFailed:
		delete(capture.redirected, message.RequestID)
		if exchange, found := capture.pending[message.RequestID]; found {
			delete(capture.pending, message.RequestID)
			exchange.ResponseIncomplete, exchange.Error = true, message.ErrorText
			capture.emit(browserNetworkItem{exchange: exchange})
		}
	}
}

func (capture *browserNetworkCapture) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	reportDrops := func() {
		if count := capture.dropped.Swap(0); count > 0 && capture.config.OnError != nil {
			capture.config.OnError(fmt.Errorf("browser network observation lost %d records due to capacity", count))
		}
	}
	defer reportDrops()
	defer func() {
		capture.mu.Lock()
		pending := len(capture.pending) + len(capture.queue)
		capture.mu.Unlock()
		if pending > 0 && capture.config.OnError != nil {
			capture.config.OnError(fmt.Errorf("browser network observation closed with %d unfinished records", pending))
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reportDrops()
		case item := <-capture.queue:
			if item.readResponse {
				bodyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				var response []byte
				err := chromedp.Run(bodyCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
					var err error
					response, err = network.GetResponseBody(item.requestID).Do(actionCtx)
					return err
				}))
				cancel()
				if err != nil {
					item.exchange.ResponseIncomplete = true
				} else {
					if len(response) > 8000 {
						response = response[:8000]
						item.exchange.ResponseIncomplete = true
					}
					item.exchange.Response = strings.ToValidUTF8(string(response), "")
					item.exchange.ResponseIncomplete = item.exchange.ResponseIncomplete || item.exchange.Response != string(response)
				}
			}
			if ctx.Err() != nil {
				return
			}
			capture.config.OnExchange(item.exchange)
		}
	}
}
