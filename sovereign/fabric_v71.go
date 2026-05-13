package tickets

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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

func (m *Manager) CreateFabricTicketV71(ctx context.Context, layer int, agentID string, content FabricContent) (uuid.UUID, error) {
	// Generate hashes for deduplication/anchoring
	summaryHash := sha256.Sum256([]byte(fmt.Sprintf("%v", content.IntentBlob)))
	content.SummaryHash = hex.EncodeToString(summaryHash[:])

	payloadHash := sha256.Sum256(content.RawContent)
	content.PayloadHash = hex.EncodeToString(payloadHash[:])

	ticketID := uuid.New()
	
	// Generate State Vector if not provided
	if len(content.StateVector) == 0 {
		content.StateVector = m.GenerateStateVector(content.RawContent)
	}

	// 1. Insert into tickets
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO tickets (ticket_id, layer_id, creator_agent_id, status)
		VALUES ($1, $2, $3, $4)
	`, ticketID, layer, agentID, "PENDING")
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to create ticket: %v", err)
	}

	// 2. Insert into ticket_content
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
	`, parentID, childID, string(relType))
	if err != nil {
		return fmt.Errorf("failed to link tickets: %v", err)
	}
	return nil
}

type ContextSnapshotV71 struct {
	ActiveTicket FabricTicket
	Content      FabricContent
	Ancestors    []FabricTicket
	Children     []FabricTicket
}

func (m *Manager) GetContextV71(ctx context.Context, agentID string) (*ContextSnapshotV71, error) {
	// 1. Target Identification
	var activeTicketID uuid.UUID
	var depth int
	err := m.db.QueryRowContext(ctx, `
		SELECT ticket_id, context_depth FROM agent_memory_index WHERE agent_id = $1
	`, agentID).Scan(&activeTicketID, &depth)
	if err != nil {
		return nil, fmt.Errorf("failed to identify active ticket for agent %s: %v", agentID, err)
	}

	snapshot := &ContextSnapshotV71{}

	// Helper to fetch ticket and content
	fetchFullTicket := func(id uuid.UUID) (FabricTicket, FabricContent, error) {
		var t FabricTicket
		var c FabricContent
		var intentJSON []byte
		var score sql.NullFloat64
		var summaryHash, payloadHash sql.NullString
		
		err := m.db.QueryRowContext(ctx, `
			SELECT t.ticket_id, t.layer_id, t.creator_agent_id, t.status, t.created_at,
			       c.intent_blob, c.consensus_score, c.raw_content, c.summary_hash, c.payload_hash
			FROM tickets t
			LEFT JOIN ticket_content c ON t.ticket_id = c.ticket_id
			WHERE t.ticket_id = $1
		`, id).Scan(&t.ID, &t.LayerID, &t.CreatorAgentID, &t.Status, &t.CreatedAt,
			&intentJSON, &score, &c.RawContent, &summaryHash, &payloadHash)
		
		if err == nil {
			if intentJSON != nil {
				json.Unmarshal(intentJSON, &c.IntentBlob)
			}
			if score.Valid {
				c.ConsensusScore = score.Float64
			}
			if summaryHash.Valid {
				c.SummaryHash = summaryHash.String
			}
			if payloadHash.Valid {
				c.PayloadHash = payloadHash.String
			}
		}
		return t, c, err
	}

	// Fetch active ticket
	snapshot.ActiveTicket, snapshot.Content, err = fetchFullTicket(activeTicketID)
	if err != nil {
		return nil, err
	}

	// 2. Causal Depth (Ancestors)
	currID := activeTicketID
	for i := 0; i < depth; i++ {
		var parentID uuid.UUID
		err = m.db.QueryRowContext(ctx, `
			SELECT parent_id FROM ticket_relationships WHERE child_id = $1 AND relationship_type = 'EVOLUTION'
		`, currID).Scan(&parentID)
		if err != nil {
			break
		}
		t, _, err := fetchFullTicket(parentID)
		if err == nil {
			snapshot.Ancestors = append(snapshot.Ancestors, t)
			currID = parentID
		}
	}

	// 3. Impact Analysis (Children)
	rows, _ := m.db.QueryContext(ctx, `
		SELECT child_id FROM ticket_relationships WHERE parent_id = $1
	`, activeTicketID)
	defer rows.Close()
	for rows.Next() {
		var childID uuid.UUID
		rows.Scan(&childID)
		t, _, err := fetchFullTicket(childID)
		if err == nil {
			snapshot.Children = append(snapshot.Children, t)
		}
	}

	return snapshot, nil
}

func (m *Manager) UpdateAgentMemory(ctx context.Context, agentID string, activeTicketID uuid.UUID) error {
	_, err := m.db.ExecContext(ctx, `
		UPSERT INTO agent_memory_index (agent_id, ticket_id, last_accessed)
		VALUES ($1, $2, now())
	`, agentID, activeTicketID)
	if err != nil {
		return fmt.Errorf("failed to update agent memory: %v", err)
	}
	return nil
}

// Semantic Layer: State Vector Generation
func (m *Manager) GenerateStateVector(content []byte) []float64 {
	// Placeholder: In a production mesh, this would call an LLM embedding model.
	// Here we generate a deterministic 8-dimensional vector based on the content hash.
	h := sha256.Sum256(content)
	vec := make([]float64, 8)
	for i := 0; i < 8; i++ {
		vec[i] = float64(h[i]) / 255.0
	}
	return vec
}

func (m *Manager) FindSimilarTickets(ctx context.Context, vector []float64, limit int) ([]uuid.UUID, error) {
	// Vector similarity search using CockroachDB array comparison
	// (Note: This is a simplified L2 distance approximation for Go logic)
	rows, err := m.db.QueryContext(ctx, `
		SELECT ticket_id FROM ticket_content
		ORDER BY state_vector <-> $1::FLOAT8[]
		LIMIT $2
	`, vector, limit)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %v", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids, nil
}
