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
	"slices"
	"sync"

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
	shadow  caddyhttp.MiddlewareHandler
	primary caddyhttp.MiddlewareHandler

	slogger *slog.Logger

	ComparisonConfig
	ReportingConfig

	PrimaryJSON json.RawMessage
	ShadowJSON  json.RawMessage
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
		shadowCtx, cancelShadow = context.WithCancel(shadowCtx)
		primaryCtx, cancelPrimary = context.WithCancel(primaryCtx)
		defer cancelShadow()
		defer cancelPrimary()
	}

	primaryBuf := bufferPool.Get().(*bytes.Buffer)
	shadowBuf := bufferPool.Get().(*bytes.Buffer)
	defer bufferPool.Put(primaryBuf)
	defer bufferPool.Put(shadowBuf)
	primaryBuf.Reset()
	shadowBuf.Reset()

	primaryRec := caddyhttp.NewResponseRecorder(w, primaryBuf, func(_ int, _ http.Header) bool {
		return len(h.json) > 0 || h.Body
	})
	shadowRec := caddyhttp.NewResponseRecorder(w, shadowBuf, func(_ int, _ http.Header) bool { return true })

	wg := sync.WaitGroup{}
	wg.Add(2)

	primaryR := r.Clone(primaryCtx)
	shadowR := r.Clone(shadowCtx)

	if r.Body != nil {
		// Multiplex the request body if it's present
		reqBuf := bufferPool.Get().(*bytes.Buffer)
		reqBuf.Reset()
		defer bufferPool.Put(reqBuf)

		tee := io.TeeReader(r.Body, reqBuf)
		primaryR.Body = io.NopCloser(tee)
		shadowR.Body = io.NopCloser(reqBuf)
	}

	go func() {
		err2 := h.primary.ServeHTTP(primaryRec, primaryR, next)
		if err2 != nil {
			h.slogger.Error("primaryHandlerError", slog.String("error", err.Error()))
			err = errors.Join(err, err2)
		}
		wg.Done()
	}()
	go func() {
		err2 := h.shadow.ServeHTTP(shadowRec, shadowR, next)
		if err2 != nil {
			h.slogger.Error("shadowHandlerError", slog.String("error", err.Error()))
			err = errors.Join(err, err2)
		}
		wg.Done()
	}()
	wg.Wait()

	if !primaryRec.Buffered() {
		return err
	}

	primaryBS := primaryRec.Buffer().Bytes()
	shadowBS := shadowRec.Buffer().Bytes()

	if h.Body {
		if !slices.Equal(primaryBS, shadowBS) {
			slog.Info("responseMismatch",
				slog.String("primary", string(primaryBS)),
				slog.String("shadow", string(shadowBS)),
			)
		}
	}

	primaryBuf.Reset()
	w.WriteHeader(primaryRec.Status())
	_, err = w.Write(primaryBS)
	return err
}

// Helper types for handling multiple responses
type responseWriter struct {
	status int
	body   *bytes.Buffer
	header http.Header
}

func newResponseWriter() *responseWriter {
	return &responseWriter{
		status: http.StatusOK,
		body:   &bytes.Buffer{},
		header: make(http.Header),
	}
}

func (w *responseWriter) Header() http.Header {
	return w.header
}

func (w *responseWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func (w *responseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

var (
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)
