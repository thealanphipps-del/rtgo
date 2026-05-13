package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sovereign-node-go/pkg/github"
	"time"

	_ "github.com/lib/pq"
)

type Ticket struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Labels    []string  `json:"labels"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url"`
}

type Manager struct {
	client *github.Client
	owner  string
	repo   string
	db     *sql.DB
}

func NewManager(client *github.Client, owner, repo string) (*Manager, error) {
	connStr := "postgresql://root@localhost:26257/antigravity?sslmode=disable"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cockroachdb: %v", err)
	}

	return &Manager{
		client: client,
		owner:  owner,
		repo:   repo,
		db:     db,
	}, nil
}

func (m *Manager) FetchTickets(ctx context.Context) ([]Ticket, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT t.ticket_id, t.status, t.created_at, c.intent_blob
		FROM tickets t
		LEFT JOIN ticket_content c ON t.ticket_id = c.ticket_id
		ORDER BY t.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query tickets: %v", err)
	}
	defer rows.Close()

	var tickets []Ticket
	for rows.Next() {
		var id string
		var status string
		var createdAt time.Time
		var intentJSON []byte
		
		if err := rows.Scan(&id, &status, &createdAt, &intentJSON); err != nil {
			continue
		}

		title := "Untitled Ticket"
		if intentJSON != nil {
			var intent map[string]interface{}
			json.Unmarshal(intentJSON, &intent)
			if t, ok := intent["action"].(string); ok {
				title = t
			} else if t, ok := intent["title"].(string); ok {
				title = t
			}
		}

		tickets = append(tickets, Ticket{
			ID:        id,
			Title:     title,
			Status:    status,
			CreatedAt: createdAt,
			URL:       "", // URL not needed for local tickets
		})
	}

	return tickets, nil
}

func (m *Manager) UpdateTicket(ctx context.Context, idStr string, title string, status string) error {
	// Update status in tickets table
	_, err := m.db.ExecContext(ctx, `
		UPDATE tickets SET status = $1 WHERE ticket_id = $2
	`, status, idStr)
	if err != nil {
		return fmt.Errorf("failed to update ticket status: %v", err)
	}

	// Update title in intent_blob if necessary
	// This is more complex since intent_blob is JSONB
	_, err = m.db.ExecContext(ctx, `
		UPDATE ticket_content 
		SET intent_blob = intent_blob || jsonb_build_object('title', $1::STRING)
		WHERE ticket_id = $2
	`, title, idStr)
	
	return err
}
