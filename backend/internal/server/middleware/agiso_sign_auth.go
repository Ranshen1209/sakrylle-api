package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func AgisoSignAuth(appSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.TrimSpace(appSecret) == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		timestamp := c.Query("timestamp")
		sign := c.Query("sign")
		jsonValue := c.PostForm("json")
		if timestamp == "" || sign == "" || jsonValue == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if !service.AgisoPushSignValid(appSecret, jsonValue, timestamp, sign) {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}
