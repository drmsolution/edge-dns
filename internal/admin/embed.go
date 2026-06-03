package admin

import (
	"embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed web/index.html
var dashboardFS embed.FS

func (s *AdminService) dashboardHandler(c *gin.Context) {
	data, err := dashboardFS.ReadFile("web/index.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "dashboard not found")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}
