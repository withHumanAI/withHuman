package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/toolcatalog"
)

// agentInstanceSelf answers with the identity the instance credential
// resolved to. The CLI reads it to learn whether a saved credential still
// authenticates; the document carries nothing beyond what the credential
// itself names.
func (a *API) agentInstanceSelf(c *gin.Context) {
	principal := currentPrincipal(c)
	c.JSON(http.StatusOK, gin.H{"agent_slug": principal.AgentSlug, "agent_instance_id": principal.AgentInstanceID})
}

func (a *API) reportToolCatalog(c *gin.Context) {
	if a.toolCatalog == nil {
		a.writeError(c, domain.ErrNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	var report domain.ToolCatalogReport
	if !bindStrict(c, &report) {
		return
	}
	if err := a.toolCatalog.Report(c.Request.Context(), currentPrincipal(c), report); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func (a *API) listToolCatalog(c *gin.Context) {
	if a.toolCatalog == nil {
		if c.Param("id") != "" {
			a.writeError(c, domain.ErrNotFound)
			return
		}
		c.JSON(http.StatusOK, domain.ToolCatalogPage{Tools: []domain.ToolCatalogEntry{}, Sources: []domain.ToolCatalogSource{}})
		return
	}
	result, err := a.toolCatalog.List(c.Request.Context(), currentPrincipal(c), toolcatalog.Query{AgentSlug: domain.AgentSlug(c.Query("agent_slug")), Search: c.Query("q"), Source: c.Query("source"), Status: c.Query("status"), Limit: queryInt(c, "limit", 50), Offset: queryInt(c, "offset", 0), Refresh: c.Query("refresh") == "true", ID: domain.ID(c.Param("id"))})
	if err != nil {
		a.writeError(c, err)
		return
	}
	if c.Param("id") != "" {
		if len(result.Tools) == 0 {
			a.writeError(c, domain.ErrNotFound)
			return
		}
		c.JSON(http.StatusOK, result.Tools[0])
		return
	}
	c.JSON(http.StatusOK, result)
}
