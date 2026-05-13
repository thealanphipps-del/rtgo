package rtgo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Server struct {
	Manager *Manager
	Router  *gin.Engine
}

func NewServer(mgr *Manager) *Server {
	r := gin.Default()
	s := &Server{
		Manager: mgr,
		Router:  r,
	}

	api := r.Group("/REST/2.0")
	{
		api.POST("/ticket", s.handleCreateTicket)
		api.GET("/ticket/:id", s.handleGetTicket)
		api.PUT("/ticket/:id", s.handleUpdateTicket)
		api.GET("/tickets", s.handleSearchTickets)
	}

	return s
}

func (s *Server) handleCreateTicket(c *gin.Context) {
	var req struct {
		Subject string                 `json:"Subject"`
		Queue   string                 `json:"Queue"`
		Content string                 `json:"Text"`
		AgentID string                 `json:"AgentID"`
		Layer   int                    `json:"Layer"`
		Intent  map[string]interface{} `json:"Intent"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.AgentID == "" {
		req.AgentID = "REST-API-USER"
	}
	if req.Layer == 0 {
		req.Layer = 2 // Default to Layer 2 (child of Genesis)
	}

	ctx := c.Request.Context()
	fabricContent := FabricContent{
		IntentBlob: req.Intent,
		RawContent: []byte(req.Content),
	}
	if fabricContent.IntentBlob == nil {
		fabricContent.IntentBlob = map[string]interface{}{"subject": req.Subject, "queue": req.Queue}
	}

	ticketID, err := s.Manager.CreateFabricTicketV71(ctx, req.Layer, req.AgentID, fabricContent)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Link to Genesis if it's a new chain
	genesisID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	s.Manager.LinkTicketsV71(ctx, genesisID, ticketID, RelEvolution)

	c.JSON(http.StatusCreated, gin.H{
		"id":      ticketID.String(),
		"message": fmt.Sprintf("Ticket %s created", ticketID),
	})
}

func (s *Server) handleGetTicket(c *gin.Context) {
	idStr := c.Param("id")
	ticketID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid uuid"})
		return
	}

	ctx := c.Request.Context()
	// Using the fabric logic to get context
	// We need to know which agent is requesting to get their "active ticket" context, 
	// but here we'll just fetch the specific ticket.
	
	var t FabricTicket
	var content FabricContent
	var intentJSON []byte
	
	err = s.Manager.db.QueryRowContext(ctx, `
		SELECT t.ticket_id, t.layer_id, t.creator_agent_id, t.status, t.created_at,
		       c.intent_blob, c.raw_content
		FROM tickets t
		LEFT JOIN ticket_content c ON t.ticket_id = c.ticket_id
		WHERE t.ticket_id = $1
	`, ticketID).Scan(&t.ID, &t.LayerID, &t.CreatorAgentID, &t.Status, &t.CreatedAt, &intentJSON, &content.RawContent)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ticket not found"})
		return
	}

	if intentJSON != nil {
		json.Unmarshal(intentJSON, &content.IntentBlob)
	}

	c.JSON(http.StatusOK, gin.H{
		"id":         t.ID.String(),
		"layer":      t.LayerID,
		"creator":    t.CreatorAgentID,
		"status":     t.Status,
		"created_at": t.CreatedAt,
		"intent":     content.IntentBlob,
		"content":    string(content.RawContent),
	})
}

func (s *Server) handleUpdateTicket(c *gin.Context) {
	idStr := c.Param("id")
	var req struct {
		Status string `json:"Status"`
		Title  string `json:"Title"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := s.Manager.UpdateTicket(c.Request.Context(), idStr, req.Title, req.Status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func (s *Server) handleSearchTickets(c *gin.Context) {
	// Simple search implementation
	rows, err := s.Manager.db.QueryContext(c.Request.Context(), `
		SELECT ticket_id, status, created_at FROM tickets ORDER BY created_at DESC LIMIT 50
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var results []gin.H
	for rows.Next() {
		var id, status string
		var created time.Time
		rows.Scan(&id, &status, &created)
		results = append(results, gin.H{"id": id, "status": status, "created_at": created})
	}

	c.JSON(http.StatusOK, results)
}

func (s *Server) Run(addr string) error {
	return s.Router.Run(addr)
}
