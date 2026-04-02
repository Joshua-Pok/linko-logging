package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"boot.dev/linko/internal/store"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type server struct {
	httpServer *http.Server
	store      store.Store
	cancel     context.CancelFunc
	logger     *slog.Logger
}

type spyReadCloser struct {
	io.ReadCloser
	bytesRead int
}

type spyResponseWriter struct {
	http.ResponseWriter
	bytesWritten int
	statusCode   int
}

const logContextKey contextKey = "log_context"

type logContext struct {
	Username string
	Error    error
}

func httpError(ctx context.Context, w http.ResponseWriter, status int, err error) {
	if logCtx, ok := ctx.Value(logContextKey).(*logContext); ok {
		logCtx.Error = err
	}
	http.Error(w, err.Error(), status) //http request logs for error responses include full error details
}

func (w *spyResponseWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}

	n, err := w.ResponseWriter.Write(p)
	w.bytesWritten += n
	return n, err
}

func (w *spyResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (r *spyReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.bytesRead += n //accumulate size of all calls
	return n, err
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID") //retrieve id from the request
		if id == "" {
			id = rand.Text()
		}
		w.Header().Set("X-Request-ID", id) //set id in the response header
		next.ServeHTTP(w, r)
	})
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler { //takes logger, returns middleware
	return func(next http.Handler) http.Handler { //returns middleware: takes handler, wraps it
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			lc := &logContext{}

			ctx := context.WithValue(r.Context(), logContextKey, lc)
			spyReader := &spyReadCloser{ReadCloser: r.Body} //create a new spyreader that wraps r,body decorator pattern
			r.Body = spyReader                              //swap r.body back to spyreader
			spyWriter := &spyResponseWriter{ResponseWriter: w}
			next.ServeHTTP(spyWriter, r.WithContext(ctx)) // let handler do its thing

			attrs := []slog.Attr{
				slog.String("request_id", w.Header().Get("X-Request-ID")),
				slog.String("method", r.Method),
				slog.String("Path", r.URL.Path),
				slog.String("Client_IP", r.RemoteAddr),
				slog.Duration("duration", time.Since(start)),
				slog.Int("request_body_bytes", spyReader.bytesRead),
				slog.Int("response_status", spyWriter.statusCode),
				slog.Int("response_body_bytes", spyWriter.bytesWritten),
			}

			if lc.Username != "" {
				attrs = append(attrs, slog.String("user", lc.Username))
			}

			logger.LogAttrs(r.Context(), slog.LevelInfo, "served request", attrs...)
		})
	}
}

func newServer(store store.Store, port int, cancel context.CancelFunc, logger *slog.Logger) *server {
	mux := http.NewServeMux()

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: requestLogger(logger)(mux),
	}

	s := &server{
		httpServer: srv,
		store:      store,
		cancel:     cancel,
		logger:     logger,
	}

	mux.HandleFunc("GET /", s.handlerIndex)
	mux.Handle("POST /api/login", s.authMiddleware(http.HandlerFunc(s.handlerLogin)))
	mux.Handle("POST /api/shorten", s.authMiddleware(http.HandlerFunc(s.handlerShortenLink)))
	mux.Handle("GET /api/stats", s.authMiddleware(http.HandlerFunc(s.handlerStats)))
	mux.Handle("GET /api/urls", s.authMiddleware(http.HandlerFunc(s.handlerListURLs)))
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /{shortCode}", s.handlerRedirect)
	mux.HandleFunc("POST /admin/shutdown", s.handlerShutdown)

	return s
}

func (s *server) start() error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	addr := ln.Addr().(*net.TCPAddr)
	port := addr.Port
	s.logger.Info("Linko is running on ", port)
	if err := s.httpServer.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func (s *server) shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *server) handlerShutdown(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("ENV") == "production" {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	s.logger.Info("Server is shutting down...")
	go s.cancel()
}
