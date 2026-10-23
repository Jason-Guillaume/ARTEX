package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

func (m *Manager) projectWorkDir(id int64) string {
	if id <= 0 {
		id = db.DefaultProjectID()
	}
	return filepath.Join(m.dir, "projects", strconv.FormatInt(id, 10), "workspace")
}

func (m *Manager) ensureProjectWorkDir(id int64) error {
	root := m.projectWorkDir(id)
	rel, err := filepath.Rel(m.dir, root)
	if err != nil {
		return err
	}
	path := m.dir
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		path = filepath.Join(path, part)
		fi, err := os.Lstat(path)
		if os.IsNotExist(err) {
			err = os.Mkdir(path, 0o755)
			if os.IsExist(err) {
				fi, err = os.Lstat(path)
				if err == nil && (!fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
					return fmt.Errorf("项目目录无效: %s", path)
				}
			}
		} else if err == nil && (!fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("项目目录无效: %s", path)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Legacy paths have globally unique task/conversation ids. Move only files
// whose owner is known from PostgreSQL, never guess the owner of an arbitrary
// file in the old application root. Rename is atomic and can be retried.
func migrateProjectPath(source, destination string) error {
	if _, err := os.Lstat(source); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("旧文件与项目文件同时存在，保留原件等待核对: %s", source)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return os.Rename(source, destination)
}

func (m *Manager) migrateTaskFiles(projectID int64, taskID string, expID int64) error {
	root := m.projectWorkDir(projectID)
	if err := migrateProjectPath(filepath.Join(m.dir, "tasks", taskID), filepath.Join(root, "tasks", taskID)); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(m.dir, "transcripts"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	prefix := fmt.Sprintf("exp%d-", expID)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			if err := migrateProjectPath(filepath.Join(m.dir, "transcripts", e.Name()), filepath.Join(root, "transcripts", e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) prepareProjectFiles() error {
	return m.pg.AcrossProjects(func(id int64) error {
		root := m.projectWorkDir(id)
		if err := m.ensureProjectWorkDir(id); err != nil {
			return err
		}
		rows, err := m.pg.Query(`SELECT id, exploration_id FROM tasks`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var taskID string
			var expID int64
			if err := rows.Scan(&taskID, &expID); err != nil {
				rows.Close()
				return err
			}
			if err := m.migrateTaskFiles(id, taskID, expID); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		rows, err = m.pg.Query(`SELECT id FROM conversations`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var cid int64
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return err
			}
			key := "conv-" + strconv.FormatInt(cid, 10)
			for _, rel := range []string{filepath.Join("sessions", key), filepath.Join("transcripts", key+".jsonl"), filepath.Join("noa", "chat-"+key)} {
				if err := migrateProjectPath(filepath.Join(m.dir, rel), filepath.Join(root, rel)); err != nil {
					rows.Close()
					return err
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if id == db.DefaultProjectID() {
			return migrateProjectPath(filepath.Join(m.dir, "drafts"), filepath.Join(root, "drafts"))
		}
		return nil
	})
}
