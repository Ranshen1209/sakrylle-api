package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgisoSignAuth_AcceptsValidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jsonValue := `{"biz_order_id":1001,"order_status":2}`
	timestamp := "1710000000"
	sign := service.AgisoPushSign("secret", jsonValue, timestamp)
	r := gin.New()
	r.POST("/hook", AgisoSignAuth("secret"), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	form := url.Values{"json": {jsonValue}}
	req := httptest.NewRequest(http.MethodPost, "/hook?timestamp="+timestamp+"&aopic=1&fromPlatform=AldsIdle&sign="+strings.ToUpper(sign), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestAgisoSignAuth_RejectsInvalidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/hook", AgisoSignAuth("secret"), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	form := url.Values{"json": {`{"biz_order_id":1001}`}}
	req := httptest.NewRequest(http.MethodPost, "/hook?timestamp=1710000000&aopic=1&sign=bad", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}
