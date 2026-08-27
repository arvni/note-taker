package audit

import (
	"context"
	"encoding/json"

	"github.com/arvinizadi/fathom/internal/db"
)

// PostgresSink writes audit entries to the audit_log table. Metadata arrives
// already redacted by Logger (spec §35).
type PostgresSink struct {
	pool *db.Pool
}

func NewPostgresSink(pool *db.Pool) *PostgresSink { return &PostgresSink{pool: pool} }

// Write persists one entry. organization_id is always recorded for tenant
// attribution (spec §48).
func (s *PostgresSink) Write(ctx context.Context, e Entry) error {
	var md []byte
	if e.Metadata != nil {
		b, err := json.Marshal(e.Metadata)
		if err != nil {
			return err
		}
		md = b
	}
	var orgID, empID any
	if e.OrganizationID != 0 {
		orgID = e.OrganizationID
	}
	if e.EmployeeID != 0 {
		empID = e.EmployeeID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_log (organization_id, employee_id, actor, action, metadata)
		VALUES ($1, $2, $3, $4, $5)`,
		orgID, empID, e.Actor, string(e.Action), md)
	return err
}
