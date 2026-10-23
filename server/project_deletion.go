package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
)

// deleteProjectData 幂等清理指定项目的流量、任务归档包和项目文件。
func (s *Server) deleteProjectData(id int64) error {
	if err := s.deleteProjectTraffic(id); err != nil {
		return fmt.Errorf("清理项目流量: %w", err)
	}
	if err := s.deleteProjectArchiveFiles(id); err != nil {
		return fmt.Errorf("清理项目归档包: %w", err)
	}
	if err := removeProjectDataRoot(s.m.dir, id); err != nil {
		return fmt.Errorf("清理项目文件: %w", err)
	}
	return nil
}

// deleteProjectTraffic 清空项目流量；代理未启用但索引仍在时临时打开存储完成清理。
func (s *Server) deleteProjectTraffic(id int64) error {
	tr := s.m.traffic
	owned := false
	if tr == nil {
		trafficDir := filepath.Join(s.m.dir, "traffic")
		if _, err := os.Stat(trafficDir); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		var err error
		tr, err = traffic.Open(trafficDir, "127.0.0.1:0")
		if err != nil {
			return err
		}
		owned = true
	}
	if owned {
		defer tr.Close()
	}
	_, _, err := tr.DeleteAll()
	return err
}

// deleteProjectArchiveFiles 清理项目归档包及可由任务或日志确认归属的暂存目录。
func (s *Server) deleteProjectArchiveFiles(id int64) error {
	return db.WithProject(id, func() error {
		rows, err := s.m.pg.Query(`SELECT id, task_id, archive_path FROM task_archives ORDER BY id`)
		if err != nil {
			return err
		}
		type archive struct {
			id, taskID int64
			path       string
		}
		var archives []archive
		taskIDs := map[int64]struct{}{}
		archiveIDs := map[int64]struct{}{}
		for rows.Next() {
			var item archive
			if err := rows.Scan(&item.id, &item.taskID, &item.path); err != nil {
				rows.Close()
				return err
			}
			archives = append(archives, item)
			taskIDs[item.taskID] = struct{}{}
			archiveIDs[item.id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, item := range archives {
			path := item.path
			if path == "" {
				path = taskArchivePath(s.m.dir, item.id, strconv.FormatInt(item.taskID, 10))
			}
			if err := validateProjectArchivePath(s.m.dir, path); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, root := range []string{
				filepath.Join(taskArchiveRoot(s.m.dir), ".staging", strconv.FormatInt(item.id, 10)),
				filepath.Join(taskArchiveRoot(s.m.dir), ".restore", strconv.FormatInt(item.id, 10)),
			} {
				if err := validateProjectArchivePath(s.m.dir, root); err != nil {
					return err
				}
				if err := os.RemoveAll(root); err != nil {
					return err
				}
			}
		}
		rows, err = s.m.pg.Query(`SELECT id FROM tasks`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var taskID int64
			if err := rows.Scan(&taskID); err != nil {
				rows.Close()
				return err
			}
			taskIDs[taskID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for taskID := range taskIDs {
			matches, err := filepath.Glob(filepath.Join(taskArchiveRoot(s.m.dir), fmt.Sprintf("task-%d-*.tar.zst*", taskID)))
			if err != nil {
				return err
			}
			for _, path := range matches {
				if err := validateProjectArchivePath(s.m.dir, path); err != nil {
					return err
				}
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
		for root, ids := range map[string]map[int64]struct{}{
			".staging": archiveIDs,
			".restore": archiveIDs,
		} {
			parent := filepath.Join(taskArchiveRoot(s.m.dir), root)
			if err := validateProjectArchivePath(s.m.dir, parent); err != nil {
				return err
			}
			entries, err := os.ReadDir(parent)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, entry := range entries {
				name := entry.Name()
				remove := false
				for archiveID := range ids {
					if name == strconv.FormatInt(archiveID, 10) || strings.HasPrefix(name, strconv.FormatInt(archiveID, 10)+"-") {
						remove = true
						break
					}
				}
				if root == ".staging" && entry.IsDir() {
					journalPath := filepath.Join(parent, name, "journal.json")
					if err := validateProjectArchivePath(s.m.dir, journalPath); err != nil {
						return err
					}
					if raw, readErr := os.ReadFile(journalPath); readErr == nil {
						var journal archiveStageJournal
						if json.Unmarshal(raw, &journal) == nil && journal.ProjectID == id {
							remove = true
						}
					} else if !os.IsNotExist(readErr) {
						return readErr
					}
				} else if root == ".restore" && entry.IsDir() {
					manifestPath := filepath.Join(parent, name, "manifest.json")
					if err := validateProjectArchivePath(s.m.dir, manifestPath); err != nil {
						return err
					}
					if raw, readErr := os.ReadFile(manifestPath); readErr == nil {
						var snapshot db.TaskArchiveSnapshot
						if json.Unmarshal(raw, &snapshot) == nil {
							_, remove = taskIDs[snapshot.TaskID]
						}
					} else if !os.IsNotExist(readErr) {
						return readErr
					}
				}
				if !remove {
					continue
				}
				path := filepath.Join(parent, name)
				if err := validateProjectArchivePath(s.m.dir, path); err != nil {
					return err
				}
				if err := os.RemoveAll(path); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// validateProjectArchivePath 限定归档清理路径并拒绝受管目录中的符号链接。
func validateProjectArchivePath(dataDir, candidate string) error {
	if err := validateArchivePath(dataDir, candidate); err != nil {
		return err
	}
	root, err := filepath.Abs(taskArchiveRoot(dataDir))
	if err != nil {
		return err
	}
	path, err := filepath.Abs(candidate)
	if err != nil {
		return err
	}
	for current := path; ; current = filepath.Dir(current) {
		if info, err := os.Lstat(current); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("归档路径包含符号链接: %s", current)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if current == root {
			break
		}
		if filepath.Dir(current) == current {
			return fmt.Errorf("归档路径不在受管目录内")
		}
	}
	for _, parent := range []string{filepath.Dir(root), filepath.Dir(filepath.Dir(root))} {
		if info, err := os.Lstat(parent); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("归档父路径包含符号链接: %s", parent)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// removeProjectDataRoot 只删除 projects/<id> 子目录，并校验父目录和目标目录。
func removeProjectDataRoot(dataDir string, id int64) error {
	root := filepath.Join(dataDir, "projects", strconv.FormatInt(id, 10))
	base, err := filepath.Abs(filepath.Join(dataDir, "projects"))
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || len(rel) == 0 {
		return fmt.Errorf("项目目录路径无效")
	}
	if fi, err := os.Lstat(base); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("项目目录父路径是符号链接")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if fi, err := os.Lstat(abs); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("项目目录是符号链接")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(abs); err != nil {
		return err
	}
	return nil
}

// processProjectDeletion 按阶段恢复并执行项目外部存储清理和 schema 删除。
func (s *Server) processProjectDeletion(job *db.ProjectDeletionJob) error {
	project, err := s.m.pg.Project(job.ProjectID)
	if err == nil {
		if project.Status != "deleting" {
			return fmt.Errorf("项目状态已变化，无法继续删除")
		}
		if err := s.m.pg.UpdateProjectDeletionPhase(job.ID, "traffic"); err != nil {
			return err
		}
		if err := db.WithProject(job.ProjectID, func() error { return s.deleteProjectData(job.ProjectID) }); err != nil {
			return err
		}
		if err := s.m.pg.UpdateProjectDeletionPhase(job.ID, "drop_schema"); err != nil {
			return err
		}
		if err := s.m.pg.DeleteProject(job.ProjectID); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	} else if job.Phase != "drop_schema" && job.Phase != "complete" {
		return fmt.Errorf("项目注册记录已丢失，删除作业阶段为 %s", job.Phase)
	}
	if err := s.m.pg.UpdateProjectDeletionPhase(job.ID, "complete"); err != nil {
		return err
	}
	s.auditProject(job.ProjectID, job.ProjectName, "deleted", map[string]any{"job_id": job.ID, "attempts": job.Attempts})
	if err := s.m.pg.CompleteProjectDeletionJob(job.ID); err != nil {
		return err
	}
	s.m.dropProjectTasks(job.ProjectID)
	return nil
}

// runProjectDeletionJob 领取并同步执行一个指定的等待作业。
func (s *Server) runProjectDeletionJob(jobID int64) error {
	job, err := s.m.pg.ClaimProjectDeletionJob(jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("删除作业不在等待队列")
	}
	if err := s.processProjectDeletion(job); err != nil {
		if failErr := s.m.pg.FailProjectDeletionJob(job.ID, job.ProjectID, err); failErr != nil {
			return errors.Join(err, failErr)
		}
		return err
	}
	return nil
}

// startProjectDeletionWorker 恢复中断作业并周期处理持久化删除队列。
func (s *Server) startProjectDeletionWorker() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	if err := s.m.pg.RecoverProjectDeletionJobs(); err != nil {
		log.Printf("[project-delete] recover jobs: %v", err)
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}
			s.projectMu.Lock()
			job, err := s.m.pg.ClaimNextProjectDeletionJob()
			if err == nil && job != nil {
				if err := s.processProjectDeletion(job); err != nil {
					if failErr := s.m.pg.FailProjectDeletionJob(job.ID, job.ProjectID, err); failErr != nil {
						log.Printf("[project-delete] job #%d failure: %v; journal: %v", job.ID, err, failErr)
					} else {
						log.Printf("[project-delete] job #%d failed: %v", job.ID, err)
					}
				}
			} else if err != nil {
				log.Printf("[project-delete] claim job: %v", err)
			}
			s.projectMu.Unlock()
		}
	}()
}
