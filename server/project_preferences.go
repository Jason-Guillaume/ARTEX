package server

import (
	"net/http"
	"strings"

	"github.com/Autumn-27/artex/db"
)

func (s *Server) projectPreferencesAccess(w http.ResponseWriter, r *http.Request, write bool) (*db.DB, bool) {
	if s.m == nil {
		writeErr(w, http.StatusServiceUnavailable, errDataSourceUnavailable)
		return nil, false
	}
	pg := s.m.PG()
	if pg == nil {
		writeErr(w, http.StatusServiceUnavailable, errDataSourceUnavailable)
		return nil, false
	}
	id, ok := pathInt(r, "id")
	current := db.ProjectFromContext(r.Context())
	if !ok || current <= 0 || id != current {
		writeErr(w, http.StatusNotFound, "项目不存在或不是当前项目")
		return nil, false
	}
	project, err := pg.Project(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "项目不存在")
		return nil, false
	}
	if write && project.Status != "active" {
		writeErr(w, http.StatusConflict, "已归档项目不能修改默认配置")
		return nil, false
	}
	return pg, true
}

func (s *Server) getProjectPreferences(w http.ResponseWriter, r *http.Request) {
	pg, ok := s.projectPreferencesAccess(w, r, false)
	if !ok {
		return
	}
	prefs, err := pg.GetProjectPreferences()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (s *Server) updateProjectPreferences(w http.ResponseWriter, r *http.Request) {
	pg, ok := s.projectPreferencesAccess(w, r, true)
	if !ok {
		return
	}
	var prefs db.ProjectPreferences
	if err := decode(r, &prefs); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	prefs.DefaultAgentKey = strings.TrimSpace(prefs.DefaultAgentKey)
	if prefs.DefaultAgentKey != "" {
		agent, err := pg.GetAgentByKey(prefs.DefaultAgentKey)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if agent == nil || !agent.Enabled {
			writeErr(w, http.StatusBadRequest, "默认 Agent 不存在或未启用")
			return
		}
	}
	if err := pg.SetProjectPreferences(prefs); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := pg.GetProjectPreferences()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) applyProjectTaskDefaults(req *createTaskReq) error {
	prefs, err := s.m.pg.GetProjectPreferences()
	if err != nil {
		return err
	}
	if len(req.LLMProfileIDs) == 0 && prefs.DefaultLLMProfileID != nil {
		req.LLMProfileIDs = []int64{*prefs.DefaultLLMProfileID}
	}
	if len(req.CompanyIDs) == 0 {
		req.CompanyIDs = append([]int64(nil), prefs.DefaultCompanyIDs...)
	}
	if prefs.DefaultTaskTemplateID == nil {
		return nil
	}
	template, err := s.m.pg.GetTaskTemplate(*prefs.DefaultTaskTemplateID)
	if err != nil {
		return err
	}
	if template == nil {
		return nil
	}
	if strings.TrimSpace(req.Description) == "" {
		req.Description = template.Description
	}
	if strings.TrimSpace(req.Goal) == "" {
		req.Goal = template.Goal
	}
	if req.CategoryID == nil {
		req.CategoryID = template.CategoryID
	}
	if len(req.InterceptRules) == 0 {
		for _, rule := range template.InterceptRules {
			req.InterceptRules = append(req.InterceptRules, taskInterceptRuleReq{
				Enabled: rule.Enabled, Action: rule.Action, Kind: rule.Kind, Pattern: rule.Pattern, Note: rule.Note,
			})
		}
	}
	return nil
}
