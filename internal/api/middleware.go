package api

import (
	"high-performance-kv-store/internal/metrics"
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriterWrapper) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func LoggingAndMetricsMiddleware(mc *metrics.MetricsCollector, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapper := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapper, r)

		duration := time.Since(start)

		// Record hand-rolled metrics
		if mc != nil {
			mc.RecordRequest(r.Method, r.URL.Path, http.StatusText(wrapper.statusCode))
			mc.RecordLatency(r.Method, r.URL.Path, duration)
		}

		// Structured server logs
		log.Printf("[HTTP] %s %s from %s - Status: %d - Duration: %v",
			r.Method, r.URL.Path, r.RemoteAddr, wrapper.statusCode, duration)
	})
}

func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[PANIC] recovered from panic: %v\nStack trace:\n%s", err, debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
