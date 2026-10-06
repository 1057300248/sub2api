package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// Parent admin authentication, observer authorization and audit middleware apply.
func registerClineAccountRoutes(accounts *gin.RouterGroup, h *handler.Handlers) {
	accounts.GET("/:id/cline/state", h.Admin.Account.GetClineMetadata)
	accounts.POST("/:id/cline/refresh", h.Admin.Account.RefreshClineMetadata)
}
