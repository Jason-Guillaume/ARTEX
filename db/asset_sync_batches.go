package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type AssetSyncBatch struct {
	ID            int64           `json:"id"`
	ParentBatchID *int64          `json:"parent_batch_id,omitempty"`
	Source        string          `json:"source"`
	Request       json.RawMessage `json:"request,omitempty"`
	State         string          `json:"state"`
	Attempt       int             `json:"attempt"`
	Synced        map[string]int  `json:"synced"`
	Companies     []string        `json:"companies"`
	Warnings      []string        `json:"warnings"`
	Errors        []string        `json:"errors"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
}

// CreateAssetSyncBatch 创建同步批次，并按父批次递增重试序号。
func (d *DB) CreateAssetSyncBatch(request any, parentID int64) (int64, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	var parent any
	if parentID > 0 {
		parent = parentID
	}
	var id int64
	err = d.QueryRow(`INSERT INTO asset_sync_batches(parent_batch_id,request,attempt)
		VALUES ($1,$2,COALESCE((SELECT attempt+1 FROM asset_sync_batches WHERE id=$1),1)) RETURNING id`, parent, raw).Scan(&id)
	return id, err
}

func (d *DB) FinishAssetSyncBatch(id int64, state string, synced map[string]int, companies, warnings, errs []string) error {
	if state != "succeeded" && state != "partial" && state != "failed" {
		return fmt.Errorf("同步批次状态无效")
	}
	if synced == nil {
		synced = map[string]int{}
	}
	if companies == nil {
		companies = []string{}
	}
	if warnings == nil {
		warnings = []string{}
	}
	if errs == nil {
		errs = []string{}
	}
	syncedJSON, err := json.Marshal(synced)
	if err != nil {
		return err
	}
	companiesJSON, err := json.Marshal(companies)
	if err != nil {
		return err
	}
	warningsJSON, err := json.Marshal(warnings)
	if err != nil {
		return err
	}
	errorsJSON, err := json.Marshal(errs)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE asset_sync_batches SET state=$2,synced=$3,companies=$4,warnings=$5,errors=$6,finished_at=now()
		WHERE id=$1 AND state='running'`, id, state, string(syncedJSON), string(companiesJSON), string(warningsJSON), string(errorsJSON))
	return err
}

const assetSyncBatchCols = `id,parent_batch_id,source,request,state,attempt,synced,companies,warnings,errors,started_at,finished_at`

func scanAssetSyncBatch(row interface{ Scan(...any) error }) (*AssetSyncBatch, error) {
	b := &AssetSyncBatch{}
	var parent sql.NullInt64
	var finished sql.NullTime
	var request, synced, companies, warnings, errs []byte
	err := row.Scan(&b.ID, &parent, &b.Source, &request, &b.State, &b.Attempt, &synced, &companies,
		&warnings, &errs, &b.StartedAt, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if parent.Valid {
		b.ParentBatchID = &parent.Int64
	}
	if finished.Valid {
		b.FinishedAt = &finished.Time
	}
	b.Request = append(json.RawMessage(nil), request...)
	if err := json.Unmarshal(synced, &b.Synced); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(companies, &b.Companies); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(warnings, &b.Warnings); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(errs, &b.Errors); err != nil {
		return nil, err
	}
	if b.Synced == nil {
		b.Synced = map[string]int{}
	}
	if b.Companies == nil {
		b.Companies = []string{}
	}
	if b.Warnings == nil {
		b.Warnings = []string{}
	}
	if b.Errors == nil {
		b.Errors = []string{}
	}
	return b, nil
}

func (d *DB) AssetSyncBatch(id int64) (*AssetSyncBatch, error) {
	return scanAssetSyncBatch(d.QueryRow(`SELECT `+assetSyncBatchCols+` FROM asset_sync_batches WHERE id=$1`, id))
}

func (d *DB) ListAssetSyncBatches(limit int) ([]*AssetSyncBatch, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := d.Query(`SELECT `+assetSyncBatchCols+` FROM asset_sync_batches ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*AssetSyncBatch, 0, limit)
	for rows.Next() {
		item, err := scanAssetSyncBatch(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (d *DB) RecoverAssetSyncBatches() error {
	_, err := d.Exec(`UPDATE asset_sync_batches SET state='failed',errors='["服务重启时任务中断，可重试"]'::jsonb,finished_at=now() WHERE state='running'`)
	return err
}
