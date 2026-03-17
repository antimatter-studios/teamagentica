package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// AuthStatus returns the current Claude CLI authentication state.
func (h *Handler) AuthStatus(c *gin.Context) {
	if h.claudeCLI == nil {
		log.Printf("[auth] AuthStatus: claudeCLI is nil")
		c.JSON(http.StatusOK, gin.H{
			"cli_enabled":      false,
			"authenticated":    false,
			"login_in_progress": false,
		})
		return
	}
	authed := h.claudeCLI.IsAuthenticated()
	inProgress := h.claudeCLI.IsLoginInProgress()
	log.Printf("[auth] AuthStatus: authenticated=%v login_in_progress=%v", authed, inProgress)
	c.JSON(http.StatusOK, gin.H{
		"cli_enabled":       true,
		"authenticated":     authed,
		"login_in_progress": inProgress,
	})
}

// AuthDeviceCode starts the Claude CLI login flow and returns the OAuth URL.
func (h *Handler) AuthDeviceCode(c *gin.Context) {
	if h.claudeCLI == nil {
		log.Printf("[auth] AuthDeviceCode: claudeCLI is nil")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Claude CLI is not enabled"})
		return
	}
	log.Printf("[auth] AuthDeviceCode: starting login flow")
	result, err := h.claudeCLI.StartLogin()
	if err != nil {
		log.Printf("[auth] AuthDeviceCode: error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[auth] AuthDeviceCode: got URL=%s", result.URL)
	c.JSON(http.StatusOK, gin.H{"url": result.URL})
}

// AuthSubmitCode delivers the authorization code to the running login process
// and waits for authentication to complete. Returns {authenticated: bool}.
func (h *Handler) AuthSubmitCode(c *gin.Context) {
	if h.claudeCLI == nil {
		log.Printf("[auth] AuthSubmitCode: claudeCLI is nil")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Claude CLI is not enabled"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
		log.Printf("[auth] AuthSubmitCode: bad request (err=%v, code empty=%v)", err, req.Code == "")
		c.JSON(http.StatusBadRequest, gin.H{"error": "code is required"})
		return
	}
	log.Printf("[auth] AuthSubmitCode: submitting code len=%d", len(req.Code))
	authed, err := h.claudeCLI.SubmitCode(req.Code)
	if err != nil {
		log.Printf("[auth] AuthSubmitCode: error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[auth] AuthSubmitCode: authenticated=%v", authed)
	c.JSON(http.StatusOK, gin.H{"authenticated": authed})
}

// AuthLogout clears Claude CLI stored credentials.
func (h *Handler) AuthLogout(c *gin.Context) {
	if h.claudeCLI == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Claude CLI is not enabled"})
		return
	}
	if err := h.claudeCLI.Logout(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}
