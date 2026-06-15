package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AgisoDeliveryHandler struct {
	service *service.AgisoDeliveryService
}

func NewAgisoDeliveryHandler(svc *service.AgisoDeliveryService) *AgisoDeliveryHandler {
	return &AgisoDeliveryHandler{service: svc}
}

func (h *AgisoDeliveryHandler) Delivery(c *gin.Context) {
	event, err := service.ParseAgisoDeliveryEvent(c.Query("fromPlatform"), c.Query("aopic"), c.PostForm("json"))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if h.service == nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if err := h.service.HandleEvent(c.Request.Context(), event); err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}
