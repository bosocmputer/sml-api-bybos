package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Auth validates the request using either:
//  1. X-Api-Key header  (new apps / curl)
//  2. api_key query param  (convenience)
//  3. guid header  (BillFlow / SML legacy clients — same key set)
//
// All valid keys are stored in a single set so one key works for all methods.
func Auth(validKeys []string) gin.HandlerFunc {
	keySet := make(map[string]struct{}, len(validKeys))
	for _, k := range validKeys {
		if k != "" {
			keySet[k] = struct{}{}
		}
	}
	return func(c *gin.Context) {
		key := c.GetHeader("X-Api-Key")
		if key == "" {
			key = c.GetHeader("guid")
		}
		if key == "" {
			key = c.Query("api_key")
		}
		if _, ok := keySet[key]; !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized — provide X-Api-Key header, guid header, or api_key query param",
			})
			return
		}
		c.Next()
	}
}
