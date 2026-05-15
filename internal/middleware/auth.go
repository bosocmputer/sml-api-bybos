package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func Auth(validKeys []string) gin.HandlerFunc {
	keySet := make(map[string]struct{}, len(validKeys))
	for _, k := range validKeys {
		keySet[k] = struct{}{}
	}
	return func(c *gin.Context) {
		key := c.GetHeader("X-Api-Key")
		if key == "" {
			key = c.Query("api_key")
		}
		if _, ok := keySet[key]; !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing api key"})
			return
		}
		c.Next()
	}
}
