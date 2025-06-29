package shadow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	shadow, primary caddyhttp.MiddlewareHandler

	Timeout string `json:"timeout"`
	timeout time.Duration

	PrimaryJSON json.RawMessage `json:"primary_json"`
	ShadowJSON  json.RawMessage `json:"shadow_json"`

	MetricsName string `json:"metrics_name"`

	slogger *slog.Logger

	now func() time.Time

	metrics metrics
}

// CaddyModule returns the Caddy module information.
func (h Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.shadow",
		New: func() caddy.Module { return new(Handler) },
	}
}

// ServeHTTP implements caddyhttp.MiddlewareHandler
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) (err error) {
	// Strip the incoming request context of any pre-existing canceler
	// to avoid one handler canceling the context before the other is finished.
	primaryCtx := context.WithoutCancel(r.Context())

	// Clone the underlying caddy vars for the shadowed request context
	// because the underlying map is not concurrency safe
	vars := maps.Clone(primaryCtx.Value(caddyhttp.VarsCtxKey).(map[string]any))
	shadowCtx := context.WithValue(primaryCtx, caddyhttp.VarsCtxKey, vars)

	{ // Make sure that both request contexts get canceled when we're done with them
		var cancelShadow, cancelPrimary context.CancelFunc
		shadowCtx, cancelShadow = context.WithTimeout(shadowCtx, h.timeout)
		primaryCtx, cancelPrimary = context.WithTimeout(primaryCtx, h.timeout)
		defer cancelShadow()
		defer cancelPrimary()
	}

	// Prepare buffers in case we need them later for performing comparisons on response bodies
	primaryBuf := bufferPool.Get().(*bytes.Buffer)
	shadowBuf := bufferPool.Get().(*bytes.Buffer)
	defer bufferPool.Put(primaryBuf)
	defer bufferPool.Put(shadowBuf)
	primaryBuf.Reset()
	shadowBuf.Reset()

	primaryRec := caddyhttp.NewResponseRecorder(w, primaryBuf, h.shouldBuffer)
	shadowRec := caddyhttp.NewResponseRecorder(&NopResponseWriter{}, shadowBuf, h.shouldBuffer)

	// Clone the request to help ensure that concurrent upstream handlers don't step on each other
	primaryR := r.Clone(primaryCtx)
	shadowR := r.Clone(shadowCtx)

	if r.Body != nil {
		// Multiplex the request body across cloned requests if it's present, since r.Clone can only do a
		// shallow clone of the body
		reqBuf := bufferPool.Get().(*bytes.Buffer)
		reqBuf.Reset()
		defer bufferPool.Put(reqBuf)

		tee := io.TeeReader(r.Body, reqBuf)
		primaryR.Body = io.NopCloser(tee)
		shadowR.Body = io.NopCloser(reqBuf)
	}

	wg := sync.WaitGroup{}
	wg.Add(2)

	// Handle the two requests concurrently
	go func() {
		err2 := h.requestProcessor("primary", h.primary)(primaryRec, primaryR, next)
		if err2 != nil {
			err = errors.Join(err, err2)
		}
		wg.Done()
	}()
	go func() {
		err2 := h.requestProcessor("shadow", h.shadow)(shadowRec, shadowR, next)
		if err2 != nil {
			err = errors.Join(err, err2)
		}
		wg.Done()
	}()

	wg.Wait()

	// If we didn't buffer, nothing left to do. Everything after here is for body comparison.
	if !primaryRec.Buffered() {
		return err
	}

	primaryBS := primaryRec.Buffer().Bytes()
	if h.CompareBody || h.CompareJQ != nil {
		h.compare(primaryBS, shadowRec.Buffer().Bytes())
	}

	w.WriteHeader(primaryRec.Status())
	_, err = w.Write(primaryBS)
	return err
}

func (h *Handler) requestProcessor(name string, inner caddyhttp.MiddlewareHandler) func(wr http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	return func(wr http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
		startedAt := h.now()
		if h.MetricsName != "" {
			if r.Body != nil {
				// Since the primary and shadow request bodies are sent through a tee, it's unfair to compare response
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

func (h *Handler) shouldBuffer(status int, _ http.Header) bool {
	return status >= 200 && status < 300 && (len(h.compareJQ) > 0 || h.CompareBody)
}

var (
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)
