package db

import (
	"database/sql"
	"fmt"
	"time"
)

// ProjectUsageEntry is one metered LLM call for the project usage drill-down.
type ProjectUsageEntry struct {
	ID            int64     `json:"id"`
	At            time.Time `json:"at"`
	TaskID        string    `json:"task_id,omitempty"`
	TaskName      string    `json:"task_name,omitempty"`
	Worker        string    `json:"worker,omitempty"`
	Model         string    `json:"model,omitempty"`
	ProfileName   string    `json:"profile_name,omitempty"`
	LatencyMs     int       `json:"latency_ms"`
	InputTokens   int       `json:"input_tokens"`
	OutputTokens  int       `json:"output_tokens"`
	CacheRead     int       `json:"cache_read_tokens"`
	CacheWrite    int       `json:"cache_write_tokens"`
	Status        string    `json:"status,omitempty"`
}

// ProjectUsageDetails returns newest ledger entries, with an optional exclusive id cursor.
func (d *DB) ProjectUsageDetails(projectID, beforeID int64, limit int) ([]ProjectUsageEntry, error) {
	p, err := d.Project(projectID)
	if err != nil {
		return nil, err
	}
	if !identRe.MatchString(p.Schema) {
		return nil, fmt.Errorf("项目 schema 无效")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query := fmt.Sprintf(`SELECT u.id, u.ts, COALESCE(u.task_id,''), COALESCE(t.description,''),
		COALESCE(u.worker,''), COALESCE(u.model,''), COALESCE(u.profile_name,''),
		COALESCE(u.latency_ms,0), u.input_tokens, u.output_tokens, u.cache_read, u.cache_write, COALESCE(u.status,'')
		FROM public.llm_usage u
		LEFT JOIN %s.tasks t ON t.id::text=u.task_id
		WHERE u.project_id=$1 AND ($2=0 OR u.id<$2)
		ORDER BY u.id DESC LIMIT $3`, p.Schema)
	rows, err := d.Query(query, projectID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ProjectUsageEntry, 0, limit)
	for rows.Next() {
		var item ProjectUsageEntry
		var taskID, taskName, worker, model, profile, status sql.NullString
		if err := rows.Scan(&item.ID, &item.At, &taskID, &taskName, &worker, &model, &profile,
			&item.LatencyMs, &item.InputTokens, &item.OutputTokens, &item.CacheRead, &item.CacheWrite, &status); err != nil {
			return nil, err
		}
		item.TaskID, item.TaskName = taskID.String, taskName.String
		item.Worker, item.Model, item.ProfileName, item.Status = worker.String, model.String, profile.String, status.String
		items = append(items, item)
	}
	return items, rows.Err()
}
