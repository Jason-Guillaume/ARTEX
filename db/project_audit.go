package db

import "encoding/json"

type ProjectAuditEntry struct {
	ID          int64           `json:"id"`
	ProjectID   int64           `json:"project_id"`
	ProjectName string          `json:"project_name"`
	Action      string          `json:"action"`
	Actor       string          `json:"actor"`
	Details     json.RawMessage `json:"details"`
	CreatedAt   string          `json:"created_at"`
}

func (d *DB) AddProjectAudit(projectID int64, projectName, action string, details any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = d.Exec(`INSERT INTO public.project_audit(project_id,project_name,action,details) VALUES ($1,$2,$3,$4)`, projectID, projectName, action, string(raw))
	return err
}

func (d *DB) ListProjectAudit(projectID int64, limit int) ([]ProjectAuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.Query(`SELECT id,project_id,project_name,action,actor,details,created_at::text
		FROM public.project_audit WHERE ($1=0 OR project_id=$1) ORDER BY id DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ProjectAuditEntry, 0)
	for rows.Next() {
		var item ProjectAuditEntry
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.ProjectName, &item.Action, &item.Actor, &item.Details, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
