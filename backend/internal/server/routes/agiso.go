package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterAgisoRoutes(r *gin.Engine, h *handler.Handlers, cfg *config.Config) {
	if h == nil || h.AgisoDelivery == nil || cfg == nil {
		return
	}
	r.POST(
		"/integrations/agiso/delivery",
		middleware.RequestBodyLimit(cfg.Server.MaxRequestBodySize),
		middleware.AgisoSignAuth(cfg.Agiso.AppSecret),
		h.AgisoDelivery.Delivery,
	)
}
