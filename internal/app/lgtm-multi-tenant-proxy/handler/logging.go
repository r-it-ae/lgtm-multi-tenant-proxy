package handler

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"lgtm-multi-tenant-proxy/pkg/httputil"
)

const maxBodyCapture = 512

// We need to wrap the response writer to be able to log the status code https://gist.github.com/Boerworz/b683e46ae0761056a636
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
}

func NewLoggingResponseWriter(w http.ResponseWriter) *loggingResponseWriter {
	// WriteHeader(int) is not called if our response implicitly returns 200 OK, so
	// we default to that status code.
	return &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	if lrw.statusCode >= 400 && lrw.body.Len() < maxBodyCapture {
		remaining := maxBodyCapture - lrw.body.Len()
		if len(b) < remaining {
			remaining = len(b)
		}
		lrw.body.Write(b[:remaining])
	}
	return lrw.ResponseWriter.Write(b)
}

// Logger can be used as a middleware chain to log every request before proxying the request
func Logger(handler http.HandlerFunc, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lrw := NewLoggingResponseWriter(w)
		defer func(begin time.Time) {
			username, _, _ := r.BasicAuth()
			fields := []zap.Field{
				zap.String("method", r.Method),
				zap.String("url", r.URL.String()),
				zap.String("proto", r.Proto),
				zap.Int("status", lrw.statusCode),
				zap.String("ip", httputil.RealClientIP(r)),
				zap.String("user_agent", r.UserAgent()),
				zap.Duration("took", time.Since(begin)),
			}
			if username != "" {
				fields = append(fields, zap.String("username", username))
			}
			if lrw.statusCode >= 400 && lrw.body.Len() > 0 {
				fields = append(fields, zap.String("upstream_error", strings.TrimSpace(lrw.body.String())))
			}

			switch {
			case lrw.statusCode >= 500:
				logger.Error("Server error", fields...)
			case lrw.statusCode >= 400:
				logger.Warn("Client error", fields...)
			case lrw.statusCode >= 300:
				logger.Info("Redirection", fields...)
			default:
				logger.Debug("Success", fields...)
			}
		}(time.Now())
		handler(lrw, r)
	}
}
