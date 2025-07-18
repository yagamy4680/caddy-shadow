package shadow

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

var (
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)

var (
	bufferPool = sync.Pool{
		New: func() any {
			return new(bytes.Buffer)
		},
	}
)

// Handler runs multiple handlers and aggregates their results
type Handler struct {
	ComparisonConfig
	ReportingConfig

	MetricsName string `json:"metrics_name"`
	metrics     metrics

	ShadowRaw       json.RawMessage `json:"shadow"`
	PrimaryRaw      json.RawMessage `json:"primary"`
	shadow, primary caddyhttp.MiddlewareHandler

	Timeout string `json:"timeout,omitempty"`
	timeout time.Duration

	slogger *slog.Logger
	now     func() time.Time
}

func (h Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.shadow",
		New: func() caddy.Module { return new(Handler) },
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) (err error) {
	primaryCtx := r.Context()

	// The vars map isn't concurrency safe, so we'll clone it for the shadowed request
	shadowCtx := context.WithValue(
		primaryCtx,
		caddyhttp.VarsCtxKey,
		maps.Clone(primaryCtx.Value(caddyhttp.VarsCtxKey).(map[string]any)),
	)

	var primaryBuf, shadowBuf *bytes.Buffer
	if h.shouldCompare() { // Only prepare buffers if we anticipate needing them for response comparison
		primaryBuf = bufferPool.Get().(*bytes.Buffer)
		shadowBuf = bufferPool.Get().(*bytes.Buffer)
		defer bufferPool.Put(primaryBuf)
		defer bufferPool.Put(shadowBuf)
		primaryBuf.Reset()
		shadowBuf.Reset()
	}

	pRecorder := caddyhttp.NewResponseRecorder(w, primaryBuf, h.shouldBuffer)
	sRecorder := caddyhttp.NewResponseRecorder(&NopResponseWriter{}, shadowBuf, h.shouldBuffer)

	// Clone the request to help ensure that concurrent upstream handlers don't step on each other
	pr := r.Clone(primaryCtx)
	sr := r.Clone(shadowCtx)

	if r.Body != nil { // Body is strictly read-once, can't be cloned. So we multiplex it to shadow with io.TeeReader
		reqBuf := bufferPool.Get().(*bytes.Buffer)
		reqBuf.Reset()
		defer bufferPool.Put(reqBuf)

		tee := io.TeeReader(r.Body, reqBuf)
		pr.Body = io.NopCloser(tee)
		sr.Body = io.NopCloser(reqBuf)
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() { // Handle only the shadowed request asynchronously
		defer wg.Done()
		sErr := h.requestProcessor("shadow", h.shadow)(sRecorder, sr, next)
		if sErr != nil { // TODO: Make sure that this error is handled as idiomatically and safely as possible
			h.slogger.Error("shadow_handler_error", slog.String("error", sErr.Error()))
		}
	}()

	err = h.requestProcessor("primary", h.primary)(pRecorder, pr, next)
	if err != nil {
		return err
	}

	var pBytes []byte
	if pRecorder.Buffered() {
		// We don't want the shadowed request to block sending any response downstream. So here we send the primary response
		// without waiting for the shadowed request.
		pBytes = pRecorder.Buffer().Bytes()
		w.WriteHeader(pRecorder.Status())

		// I think the best thing to do is sit on this error until we've finished handling the shadowed request.
		// A failure to write downstream (client disconnect, etc) doesn't reflect on the shadowed handler and shouldn't be
		// allowed to impact handling the shadowed request, getting metrics, doing comparisons, etc.
		_, err = w.Write(pBytes)
	}

	if h.shouldCompare() {
		// If we're doing comparison, let's do it async so we can avoid blocking. This way downstream handlers and
		// clients are able to know we're done with our ResponseWriter here.
		go func() {
			wg.Wait()
			var sBytes []byte
			if sRecorder.Buffered() {
				sBytes = sRecorder.Buffer().Bytes()
			}
			h.compareBody(pBytes, sBytes)
			h.compareHeaders(pRecorder.Header(), sRecorder.Header())
			h.compareStatus(pRecorder.Status(), sRecorder.Status())
		}()
	}

	return err
}

func (h *Handler) requestProcessor(name string, inner caddyhttp.MiddlewareHandler) func(wr http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	return func(wr http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
		// Even though there may be a timeout provided by another handler, we really want to make sure we keep our
		// goroutines tidy. We're enforcing a timeout on all request processing as mitigation for the possibility of
		// goroutine leaks and connection leaks.
		ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
		defer cancel()
		r = r.WithContext(ctx)
		startedAt := h.now()
		if h.MetricsName != "" {
			if r.Body != nil {
				// Since the primary and shadow request bodies are sent through a tee, it's unfair to compareBody response
				// timing using the original startedAt value. The shadow request body can never be fully transmitted
				// before the primary, introducing unintended skew to the metrics.
				//
				// This FinishReadCloser lets us decouple the timing of primary and shadow handlers by re-setting
				// the startedAt value at the time the request body is fully transmitted.
				r.Body = NewFinishReadCloser(r.Body, func() {
					startedAt = h.now()
				})
			}

			// TimedWriter lets us capture the time when we first start receiving a response body, and the time when we
			// first receive a response status, allowing us to track time to first byte.
			wr = NewTimedWriter(wr, func() {
				h.metrics.ttfb[name].Observe(time.Since(startedAt).Seconds())
			})
		}
		err := inner.ServeHTTP(wr, r, next)
		if h.MetricsName != "" {
			h.metrics.totalTime[name].Observe(time.Since(startedAt).Seconds())
		}
		if err != nil {
			h.slogger.Error(name+"_handler_error", slog.String("error", err.Error()))
		}
		return err
	}
}
