package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

const RequestIDKey = "request_id"

const CorrelationIDHeader = "X-Correlation-ID"

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,63}$`)

// RequestID reads X-Request-ID from client or generates a random one,
// stores it in context, and echoes it back in the response header.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader(CorrelationIDHeader))
		if id == "" {
			id = strings.TrimSpace(c.GetHeader("X-Request-ID"))
		}
		if !safeRequestID.MatchString(id) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		c.Set(RequestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Header(CorrelationIDHeader, id)
		c.Next()
	}
}
