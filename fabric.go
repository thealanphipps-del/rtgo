package rtgo

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

type RelationshipType string

const (
	RelEvolution  RelationshipType = "EVOLUTION"
	RelConsequence RelationshipType = "CONSEQUENCE"
	RelContext     RelationshipType = "CONTEXT"
	RelGenesis     RelationshipType = "GENESIS"
)

type FabricTicket struct {
	ID             uuid.UUID
	LayerID        int
	CreatorAgentID string
	Status         string
	CreatedAt      time.Time
}

type FabricContent struct {
	TicketID       uuid.UUID
	IntentBlob     map[string]interface{}
	StateVector    []float64
	ConsensusScore float64
	RawContent     []byte
	SummaryHash    string
	PayloadHash    string
}

type Manager struct {
	db *sql.DB
}

func NewManager(connStr string) (*Manager, error) {
	if connStr == "" {
		connStr = "postgresql://root@localhost:26257/antigravity?sslmode=disable"
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cockroachdb: %v", err)
	}
	return &Manager{db: db}, nil
}

func (m *Manager) CreateFabricTicketV71(ctx context.Context, layer int, agentID string, content FabricContent) (uuid.UUID, error) {
	summaryHash := sha256.Sum256([]byte(fmt.Sprintf("%v", content.IntentBlob)))
	content.SummaryHash = hex.EncodeToString(summaryHash[:])

	payloadHash := sha256.Sum256(content.RawContent)
	content.PayloadHash = hex.EncodeToString(payloadHash[:])

	ticketID := uuid.New()
	
	if len(content.StateVector) == 0 {
		content.StateVector = m.GenerateStateVector(content.RawContent)
	}

	_, err := m.db.ExecContext(ctx, `
		INSERT INTO tickets (ticket_id, layer_id, creator_agent_id, status)
		VALUES ($1, $2, $3, $4)
	`, ticketID, layer, agentID, "PENDING")
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to create ticket: %v", err)
	}

	intentJSON, _ := json.Marshal(content.IntentBlob)
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO ticket_content (ticket_id, intent_blob, consensus_score, raw_content, summary_hash, payload_hash, state_vector)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, ticketID, intentJSON, content.ConsensusScore, content.RawContent, content.SummaryHash, content.PayloadHash, content.StateVector)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to store ticket content: %v", err)
	}

	return ticketID, nil
}

func (m *Manager) LinkTicketsV71(ctx context.Context, parentID, childID uuid.UUID, relType RelationshipType) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO ticket_relationships (parent_id, child_id, relationship_type)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, parentID, childID, string(relType))
	return err
}

func (m *Manager) UpdateTicket(ctx context.Context, idStr string, title string, status string) error {
	if status != "" {
		_, err := m.db.ExecContext(ctx, `UPDATE tickets SET status = $1 WHERE ticket_id = $2`, status, idStr)
		if err != nil {
			return err
		}
	}

	if title != "" {
		_, err := m.db.ExecContext(ctx, `
			UPDATE ticket_content 
			SET intent_blob = intent_blob || jsonb_build_object('title', $1::STRING)
			WHERE ticket_id = $2
		`, title, idStr)
		if err != nil {
			return err
		}
	}
	
	return nil
}

func (m *Manager) GenerateStateVector(content []byte) []float64 {
	h := sha256.Sum256(content)
	vec := make([]float64, 8)
	for i := 0; i < 8; i++ {
		vec[i] = float64(h[i]) / 255.0
	}
	return vec
}
