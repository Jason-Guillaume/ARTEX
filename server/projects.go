package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Autumn-27/artex/db"
)

// listProjects 返回项目统计、用量和持久化删除状态，并与生命周期变更互斥。
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	s.projectMu.RLock()
	defer s.projectMu.RUnlock()
	if s.m == nil || s.m.pg == nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	projects, err := s.m.pg.ListProjects()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	usageByProject, err := s.m.pg.UsageByProject()
	if err != nil {
		writeErr(w, 503, "无法读取项目用量统计")
		return
	}
	if projects == nil {
		projects = []db.Project{}
	}
	items := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		var tasks, assets, findings int64
		query := fmt.Sprintf(`SELECT (SELECT count(*) FROM %s.tasks WHERE deleted_at IS NULL), (SELECT count(*) FROM %s.assets), (SELECT count(*) FROM %s.findings)`, p.Schema, p.Schema, p.Schema)
		if err := s.m.pg.QueryRow(query).Scan(&tasks, &assets, &findings); err != nil {
			writeErr(w, 503, "无法读取项目统计")
			return
		}
		usage := usageByProject[p.ID]
		deletion, err := s.m.pg.ProjectDeletionJob(p.ID)
		if err != nil {
			writeErr(w, 503, "无法读取项目删除状态")
			return
		}
		items = append(items, map[string]any{
			"id": p.ID, "name": p.Name, "description": p.Description,
			"status": p.Status, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
			"schema_name": p.Schema, "is_default": p.ID == db.DefaultProjectID(),
			"task_count": tasks, "asset_count": assets, "finding_count": findings,
			"llm_calls": usage.Calls, "llm_input_tokens": usage.InputTokens,
			"llm_output_tokens": usage.OutputTokens,
			"deletion_job": deletion,
		})
	}
	writeJSON(w, 200, map[string]any{
		"projects": items,
		"current":  db.CurrentProject(),
	})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "请求体不是合法 JSON")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := s.m.pg.CreateProject(ctx, req.Name, req.Description)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.auditProject(p.ID, p.Name, "created", map[string]any{"status": p.Status})
	writeJSON(w, 201, p)
}

// updateProject 仅允许修改正常项目的名称和说明，并记录审计事件。
func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	s.projectMu.Lock()
	defer s.projectMu.Unlock()
	if s.m == nil || s.m.pg == nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "项目 id 无效")
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "请求体不是合法 JSON")
		return
	}
	project, err := s.m.pg.Project(id)
	if err != nil {
		writeErr(w, 404, "项目不存在")
		return
	}
	if project.Status != "active" {
		writeErr(w, 409, "只有正常项目可以修改")
		return
	}
	if err := s.m.pg.UpdateProject(id, req.Name, req.Description); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, 404, "项目不存在")
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	s.auditProject(id, req.Name, "updated", map[string]any{"description": req.Description})
	p, err := s.m.pg.Project(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

// deleteProject 校验归档和名称确认后，将清理提交到可恢复删除队列。
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	s.projectMu.Lock()
	defer s.projectMu.Unlock()
	if s.m == nil || s.m.pg == nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "项目 id 无效")
		return
	}
	p, err := s.m.pg.Project(id)
	if err != nil {
		writeErr(w, 404, "项目不存在")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil || req.Name != p.Name {
		writeErr(w, 400, "请输入完整项目名称确认删除")
		return
	}
	if p.Status != "archived" && p.Status != "deletion_failed" {
		writeErr(w, 409, "请先归档项目后再永久删除")
		return
	}
	if err := s.projectIdle(id); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	job, err := s.m.pg.QueueProjectDeletion(id, p.Name)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	s.auditProject(id, p.Name, "delete_started", map[string]any{"status": p.Status, "job_id": job.ID})
	w.Header().Set("Location", fmt.Sprintf("/api/projects/%d", id))
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) auditProject(id int64, name, action string, details any) {
	if s.m == nil || s.m.pg == nil {
		return
	}
	if err := s.m.pg.AddProjectAudit(id, name, action, details); err != nil {
		log.Printf("[project-audit] %s project #%d: %v", action, id, err)
	}
}

func (s *Server) projectAudit(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, errDataSourceUnavailable)
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || db.ProjectFromContext(r.Context()) != id {
		writeErr(w, http.StatusNotFound, "项目不存在或不是当前项目")
		return
	}
	items, err := s.m.pg.ListProjectAudit(id, queryInt(r, "limit", 100))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// systemProjectAudit 返回最近的跨项目审计记录，供管理员追查已删除项目。
func (s *Server) systemProjectAudit(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, errDataSourceUnavailable)
		return
	}
	items, err := s.m.pg.ListProjectAudit(0, queryInt(r, "limit", 100))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (m *Manager) dropProjectTasks(projectID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.activeByProject, projectID)
	for id, t := range m.tasks {
		if t != nil && t.ProjectID == projectID {
			delete(m.tasks, id)
			if m.active == id {
				m.active = ""
			}
		}
	}
}

// projectIdle checks both live handles and persisted work before archiving.
func (s *Server) projectIdle(id int64) error {
	for _, task := range s.m.List() {
		if task.ProjectID != id {
			continue
		}
		state := task.lifecycleSnapshot()
		if !isTerminalStatus(state.Status) || state.Paused || state.Queued || s.engine.inflightCount(task.ID) > 0 || s.engine.ActiveLLMCalls(task.ID) > 0 {
			return fmt.Errorf("项目还有未结束的任务，请先结束任务")
		}
		s.chatMu.Lock()
		busy := s.chatBusy[task.ID]
		s.chatMu.Unlock()
		if busy {
			return fmt.Errorf("项目还有运行中的任务对话")
		}
	}
	return db.WithProject(id, func() error {
		var pending int
		err := s.m.pg.QueryRow(`SELECT
		 (SELECT count(*) FROM tasks WHERE deleted_at IS NULL AND status NOT IN ('done','failed','timeout')) +
		 (SELECT count(*) FROM task_archives WHERE state IN ('archive_queued','archiving','restore_queued','restoring')) +
		 (SELECT count(*) FROM side_question_requests WHERE status='running') +
		 (SELECT count(*) FROM finding_retests WHERE status IN ('pending','running'))`).Scan(&pending)
		if err != nil {
			return fmt.Errorf("无法检查后台任务: %w", err)
		}
		if pending > 0 {
			return fmt.Errorf("项目还有后台任务，请等待结束后重试")
		}
		var pendingNotifications int
		if err := s.m.pg.QueryRow(`SELECT count(*) FROM public.notification_deliveries d
JOIN public.notification_events e ON e.id=d.event_id
WHERE e.project_id=$1 AND d.state IN ($2,$3)`, id, db.NotifyStatePending, db.NotifyStateSending).Scan(&pendingNotifications); err != nil {
			return fmt.Errorf("无法检查通知投递: %w", err)
		}
		if pendingNotifications > 0 {
			return fmt.Errorf("项目还有待投递通知，请等待队列处理完")
		}
		rows, err := s.m.pg.Query(`SELECT id FROM conversations`)
		if err != nil {
			return err
		}
		defer rows.Close()
		s.chatMu.Lock()
		defer s.chatMu.Unlock()
		for rows.Next() {
			var cid int64
			if err := rows.Scan(&cid); err != nil {
				return err
			}
			if s.chatBusy[s.convBusyKey(cid)] {
				return fmt.Errorf("项目还有运行中的对话")
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		s.queueMu.Lock()
		defer s.queueMu.Unlock()
		if s.triggerProjectActive[id] > 0 {
			return fmt.Errorf("项目还有运行中的触发任务")
		}
		for _, queue := range s.triggerQ {
			for _, run := range queue {
				if run.projectID == id {
					return fmt.Errorf("项目还有待执行的触发任务")
				}
			}
		}
		return nil
	})
}

func (s *Server) archiveProject(w http.ResponseWriter, r *http.Request) {
	s.setProjectStatus(w, r, "archived")
}
func (s *Server) restoreProject(w http.ResponseWriter, r *http.Request) {
	s.setProjectStatus(w, r, "active")
}

// setProjectStatus 执行项目归档或恢复，并在变更前检查状态和后台作业。
func (s *Server) setProjectStatus(w http.ResponseWriter, r *http.Request, status string) {
	s.projectMu.Lock()
	defer s.projectMu.Unlock()
	if s.m == nil || s.m.pg == nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "项目 id 无效")
		return
	}
	project, err := s.m.pg.Project(id)
	if err != nil {
		writeErr(w, 404, "项目不存在")
		return
	}
	if status == "archived" && project.Status != "active" {
		writeErr(w, http.StatusConflict, "只有正常项目可以归档")
		return
	}
	if status == "active" && project.Status != "archived" {
		writeErr(w, http.StatusConflict, "只有已归档项目可以恢复")
		return
	}
	if status == "archived" {
		if err := s.projectIdle(id); err != nil {
			writeErr(w, 409, err.Error())
			return
		}
	}
	if err := s.m.pg.SetProjectStatus(id, status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, 404, "项目不存在")
			return
		}
		writeErr(w, 409, err.Error())
		return
	}
	action := "restored"
	if status == "archived" {
		action = "archived"
	}
	s.auditProject(id, project.Name, action, map[string]any{"status": status})
	if status == "archived" {
		for _, t := range s.m.List() {
			if t.ProjectID == id {
				s.engine.StopTask(t.ID)
			}
		}
	}
	p, err := s.m.pg.Project(id)
	if err != nil {
		writeErr(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, p)
}
