package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

const projectMigrationLedgerDDL = `CREATE TABLE IF NOT EXISTS project_schema_migrations (
	version TEXT PRIMARY KEY,
	checksum TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

type SchemaMigration struct {
	Scope      string    `json:"scope"`
	ProjectID  int64     `json:"project_id,omitempty"`
	Project    string    `json:"project,omitempty"`
	Version    string    `json:"version"`
	Checksum   string    `json:"checksum"`
	AppliedAt  time.Time `json:"applied_at"`
}

func schemaFingerprint(script string) string {
	sum := sha256.Sum256([]byte(script))
	return hex.EncodeToString(sum[:])
}

func recordSystemSchemaMigration(d *DB) error {
	checksum := schemaFingerprint(scopedSchema("system"))
	_, err := d.Exec(`INSERT INTO public.schema_migrations(version,checksum) VALUES ($1,$1) ON CONFLICT (version) DO NOTHING`, checksum)
	return err
}

func recordProjectSchemaMigration(q interface{ Exec(string, ...any) (sql.Result, error) }) error {
	if _, err := q.Exec(projectMigrationLedgerDDL); err != nil {
		return err
	}
	checksum := schemaFingerprint(scopedSchema("project"))
	_, err := q.Exec(`INSERT INTO project_schema_migrations(version,checksum) VALUES ($1,$1) ON CONFLICT (version) DO NOTHING`, checksum)
	return err
}

func (d *DB) SchemaMigrationHistory() ([]SchemaMigration, error) {
	items := make([]SchemaMigration, 0)
	rows, err := d.Query(`SELECT version,checksum,applied_at FROM public.schema_migrations ORDER BY applied_at DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item SchemaMigration
		item.Scope = "system"
		if err := rows.Scan(&item.Version, &item.Checksum, &item.AppliedAt); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	projects, err := d.ListProjects()
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		if err := WithProject(project.ID, func() error {
			rows, err := d.Query(`SELECT version,checksum,applied_at FROM project_schema_migrations ORDER BY applied_at DESC`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				item := SchemaMigration{Scope: "project", ProjectID: project.ID, Project: project.Name}
				if err := rows.Scan(&item.Version, &item.Checksum, &item.AppliedAt); err != nil {
					return err
				}
				items = append(items, item)
			}
			return rows.Err()
		}); err != nil {
			return nil, fmt.Errorf("项目 %s 迁移版本: %w", project.Name, err)
		}
	}
	return items, nil
}
