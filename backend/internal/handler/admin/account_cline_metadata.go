package admin

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) clineMetadataAccount(c *gin.Context) (*service.Account, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return nil, false
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, false
	}
	if !account.IsCline() {
		response.BadRequest(c, "This operation requires a Cline account")
		return nil, false
	}
	c.Header("Cache-Control", "no-store")
	return account, true
}

// GetClineMetadata is local-only: opening a panel does not contact the provider.
func (h *AccountHandler) GetClineMetadata(c *gin.Context) {
	account, ok := h.clineMetadataAccount(c)
	if !ok {
		return
	}
	response.Success(c, service.ClineMetadataForAccount(account, time.Now().UTC()))
}

func (h *AccountHandler) RefreshClineMetadata(c *gin.Context) {
	account, ok := h.clineMetadataAccount(c)
	if !ok {
		return
	}
	if h.openAIGatewayService == nil {
		response.ErrorFrom(c, service.ErrClineMetadataUnavailable)
		return
	}
	view, err := h.openAIGatewayService.RefreshClineMetadata(c.Request.Context(), account)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}
