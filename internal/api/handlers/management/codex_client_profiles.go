package management

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func (h *Handler) GetCodexClientProfiles(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"profiles": h.cfg.CodexClientProfiles})
}

func (h *Handler) PutCodexClientProfiles(c *gin.Context) {
	var profiles []config.CodexClientProfile
	if err := c.ShouldBindJSON(&profiles); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	for i := range profiles {
		profiles[i].ID = strings.TrimSpace(profiles[i].ID)
		profiles[i].Name = strings.TrimSpace(profiles[i].Name)
		if profiles[i].ID == "" {
			profiles[i].ID = uuid.NewString()
		}
		if profiles[i].Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "profile name is required"})
			return
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg.CodexClientProfiles = profiles
	if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"profiles": profiles})
}
