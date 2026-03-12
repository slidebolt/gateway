package main

import (
	"bytes"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// responseCapture wraps gin.ResponseWriter to buffer the response body so the
// logging middleware can include it in 5xx log lines.
type responseCapture struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	rc.body.Write(b)
	return rc.ResponseWriter.Write(b)
}

// requestLogger is a Gin middleware that logs every HTTP request.
//   - 2xx / 3xx  →  METHOD path → STATUS duration
//   - 4xx        →  METHOD path → STATUS duration
//   - 5xx        →  METHOD path → STATUS duration | <response body>
//
// SSE and WebSocket upgrade paths are skipped (they are long-lived streams).
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.RequestURI()

		// Skip noisy streaming endpoints.
		if strings.HasPrefix(path, "/api/topics/") {
			c.Next()
			return
		}

		start := time.Now()
		rc := &responseCapture{ResponseWriter: c.Writer}
		c.Writer = rc

		c.Next()

		status := rc.Status()
		dur := time.Since(start).Round(time.Millisecond)

		if status >= 500 {
			body := strings.TrimSpace(rc.body.String())
			if len(body) > 512 {
				body = body[:512] + "…"
			}
			log.Printf("gateway: %s %s → %d %s | %s",
				c.Request.Method, path, status, dur, body)
		} else {
			log.Printf("gateway: %s %s → %d %s",
				c.Request.Method, path, status, dur)
		}
	}
}
