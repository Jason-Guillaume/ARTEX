package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type ProjectPreferences struct {
	DefaultLLMProfileID   *int64  `json:"default_llm_profile_id"`
	DefaultAgentKey       string  `json:"default_agent_key"`
	DefaultTaskTemplateID *int64  `json:"default_task_template_id"`
	DefaultCompanyIDs     []int64 `json:"default_company_ids"`
}

func (d *DB) GetProjectPreferences() (*ProjectPreferences, error) {
	p := &ProjectPreferences{DefaultCompanyIDs: []int64{}}
	var agent sql.NullString
	var companies []byte
	var profileID, templateID sql.NullInt64
	err := d.QueryRow(`SELECT default_llm_profile_id, default_agent_key, default_task_template_id, default_company_ids
		FROM project_preferences WHERE id=1`).Scan(&profileID, &agent, &templateID, &companies)
	if err != nil {
		return nil, err
	}
	p.DefaultAgentKey = agent.String
	if profileID.Valid {
		p.DefaultLLMProfileID = &profileID.Int64
	}
	if templateID.Valid {
		p.DefaultTaskTemplateID = &templateID.Int64
	}
	if len(companies) > 0 {
		if err := json.Unmarshal(companies, &p.DefaultCompanyIDs); err != nil {
			return nil, err
		}
	}
	if p.DefaultCompanyIDs == nil {
		p.DefaultCompanyIDs = []int64{}
	}
	return p, nil
}

func (d *DB) SetProjectPreferences(p ProjectPreferences) error {
	companyIDs, err := NormalizeTaskCompanyIDs(p.DefaultCompanyIDs)
	if err != nil {
		return err
	}
	for _, id := range companyIDs {
		var exists bool
		if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM companies WHERE id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("默认企业 #%d 不存在", id)
		}
	}
	p.DefaultAgentKey = strings.TrimSpace(p.DefaultAgentKey)
	companies, err := json.Marshal(companyIDs)
	if err != nil {
		return err
	}
	_, err = d.Exec(`INSERT INTO project_preferences(id, default_llm_profile_id, default_agent_key, default_task_template_id, default_company_ids)
		VALUES (1,$1,NULLIF($2,''),$3,$4)
		ON CONFLICT (id) DO UPDATE SET default_llm_profile_id=EXCLUDED.default_llm_profile_id,
		default_agent_key=EXCLUDED.default_agent_key, default_task_template_id=EXCLUDED.default_task_template_id,
		default_company_ids=EXCLUDED.default_company_ids, updated_at=now()`,
		p.DefaultLLMProfileID, p.DefaultAgentKey, p.DefaultTaskTemplateID, string(companies))
	return err
}
