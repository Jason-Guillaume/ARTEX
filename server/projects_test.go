package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestProjectActiveTasks(t *testing.T) {
	a := &Task{ID: "a", ProjectID: 101}
	b := &Task{ID: "b", ProjectID: 102}
	m := &Manager{tasks: map[string]*Task{"a": a, "b": b}, active: "b"}
	for _, task := range []*Task{a, b} {
		_ = db.WithProject(task.ProjectID, func() error {
			if m.ResolveTask("") != task {
				t.Fatal("活动任务跨项目")
			}
			if !m.SetActive(task.ID) {
				t.Fatal("无法设置当前项目任务")
			}
			other := a
			if other == task {
				other = b
			}
			if m.SetActive(other.ID) {
				t.Fatal("允许跨项目切换活动任务")
			}
			return nil
		})
	}
	_ = db.WithProject(103, func() error {
		if m.ResolveTask("active") != nil {
			t.Fatal("空项目返回了其他项目任务")
		}
		return nil
	})
}

func TestProjectWorkspaceIsolation(t *testing.T) {
	m := &Manager{dir: t.TempDir()}
	s := &Server{m: m}
	for _, pid := range []int64{1, 102} {
		_ = db.WithProject(pid, func() error {
			path, ok := s.wsResolve("note.txt")
			if !ok {
				t.Fatal("无法解析工作空间")
			}
			if err := os.WriteFile(path, []byte(strconv.FormatInt(pid, 10)), 0o600); err != nil {
				t.Fatal(err)
			}
			return nil
		})
	}
	_ = db.WithProject(1, func() error {
		path, ok := s.wsResolve("note.txt")
		if !ok {
			t.Fatal("解析失败")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "1" {
			t.Fatalf("默认项目文件交叉: %s %v", data, err)
		}
		if err := os.Symlink(m.projectWorkDir(102), filepath.Join(s.workspaceRoot(), "other")); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.wsResolve("other/note.txt"); ok {
			t.Fatal("允许符号链接跨项目")
		}
		if _, ok := s.wsResolve("other/new.txt"); ok {
			t.Fatal("允许符号链接写入其他项目")
		}
		return nil
	})
	if err := os.WriteFile(filepath.Join(m.dir, "projects", "103"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = db.WithProject(103, func() error {
		if _, ok := s.wsResolve("note.txt"); ok {
			t.Fatal("目录创建失败不应回退共享目录")
		}
		return nil
	})
}

func TestProjectsAPI(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	defer func() { cancel(); s.archiveWG.Wait(); _ = m.Close() }()
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, pid int64, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+token)
		if pid > 0 {
			r.Header.Set("X-ARTEX-Project", strconv.FormatInt(pid, 10))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := do("POST", "/api/projects", 1, map[string]string{"name": "API 隔离项目"})
	if w.Code != 201 {
		t.Fatalf("新建: %d %s", w.Code, w.Body.String())
	}
	var p db.Project
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.pg.SetProjectStatus(p.ID, "archived"); _ = m.pg.DeleteProject(p.ID) }()
	base := "/api/projects/" + strconv.FormatInt(p.ID, 10)
	w = do("POST", base+"/archive", 1, nil)
	if w.Code != 200 {
		t.Fatalf("归档: %d %s", w.Code, w.Body.String())
	}
	w = do("POST", "/api/task-categories", p.ID, map[string]string{"name": "不允许写入"})
	if w.Code != 409 {
		t.Fatalf("归档仍可写: %d %s", w.Code, w.Body.String())
	}
	w = do("GET", "/api/tasks", p.ID, nil)
	if w.Code != 200 {
		t.Fatalf("归档不能查看: %d %s", w.Code, w.Body.String())
	}
	w = do("POST", base+"/restore", 1, nil)
	if w.Code != 200 {
		t.Fatalf("恢复: %d %s", w.Code, w.Body.String())
	}
	w = do("POST", "/api/task-categories", p.ID, map[string]string{"name": "允许写入"})
	if w.Code != 201 {
		t.Fatalf("恢复后不能写: %d %s", w.Code, w.Body.String())
	}
	w = do("PATCH", base, 1, map[string]string{"name": "重命名项目", "description": "更新说明"})
	if w.Code != 200 {
		t.Fatalf("编辑: %d %s", w.Code, w.Body.String())
	}
	w = do("POST", base+"/archive", 1, nil)
	if w.Code != 200 {
		t.Fatalf("再次归档: %d %s", w.Code, w.Body.String())
	}
	w = do("DELETE", base, 1, map[string]string{"name": "错误名称"})
	if w.Code != 400 {
		t.Fatal("删除没有要求项目名称确认")
	}
	w = do("DELETE", base, 1, map[string]string{"name": "重命名项目"})
	if w.Code != 200 {
		t.Fatalf("删除: %d %s", w.Code, w.Body.String())
	}
	w = do("GET", "/api/tasks", p.ID, nil)
	if w.Code != 400 {
		t.Fatalf("删除后仍可访问: %d", w.Code)
	}
}

func TestProjectArchiveTerminalTask(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	defer func() { cancel(); _ = m.Close() }()
	p, err := m.pg.CreateProject(t.Context(), "有历史任务的项目", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.pg.SetProjectStatus(p.ID, "archived"); _ = m.pg.DeleteProject(p.ID) }()
	var task *Task
	if err := db.WithProject(p.ID, func() error {
		var err error
		task, err = m.CreateTask("历史任务", "原始目标", nil, 0, 0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.projectIdle(p.ID); err == nil {
		t.Fatal("有未完成任务仍允许归档")
	}
	if err := db.WithProject(p.ID, func() error { return m.SetTaskStatus(task.ID, "done") }); err != nil {
		t.Fatal(err)
	}
	// Terminal tasks retain an idle engine handle until archive/shutdown. This
	// must not prevent archiving when no work is actually in flight.
	s.engine.started.Store(task.ID, true)
	if err := s.projectIdle(p.ID); err != nil {
		t.Fatalf("终态任务不能归档: %v", err)
	}
	if err := m.pg.SetProjectStatus(p.ID, "archived"); err != nil {
		t.Fatal(err)
	}
	if err := db.WithProject(p.ID, func() error {
		if _, err := m.CreateTask("不应创建", "", nil, 0, 0); err == nil {
			t.Fatal("归档项目仍允许后台创建任务")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.engine.StopTask(task.ID)
	m.dropProjectTasks(p.ID)
	loaded := m.LoadExisting()
	var found bool
	for _, task := range loaded {
		if task.ProjectID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("归档项目重启加载后丢失历史任务")
	}
}
