package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ProjectDeletionJob struct {
	ID         int64      `json:"id"`
	ProjectID  int64      `json:"project_id"`
	ProjectName string    `json:"project_name"`
	State      string     `json:"state"`
	Phase      string     `json:"phase"`
	Error      string     `json:"error"`
	Attempts   int        `json:"attempts"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

const projectDeletionJobCols = `id,project_id,project_name,state,phase,error,attempts,created_at,started_at,finished_at`

// scanProjectDeletionJob 将单行查询结果转换成可选时间字段完整的删除作业。
func scanProjectDeletionJob(row interface{ Scan(...any) error }) (*ProjectDeletionJob, error) {
	j := &ProjectDeletionJob{}
	var started, finished sql.NullTime
	err := row.Scan(&j.ID, &j.ProjectID, &j.ProjectName, &j.State, &j.Phase, &j.Error, &j.Attempts, &j.CreatedAt, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if started.Valid {
		j.StartedAt = &started.Time
	}
	if finished.Valid {
		j.FinishedAt = &finished.Time
	}
	return j, nil
}

// QueueProjectDeletion 原子地校验归档状态、写入持久化作业并锁定项目写入。
func (d *DB) QueueProjectDeletion(projectID int64, projectName string) (*ProjectDeletionJob, error) {
	if projectID == DefaultProjectID() {
		return nil, fmt.Errorf("默认项目不能删除")
	}
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, schemaMigrationLockKey); err != nil {
		return nil, err
	}
	var status string
	if err := tx.QueryRow(`SELECT status FROM public.projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&status); err != nil {
		return nil, err
	}
	if status != "archived" && status != "deletion_failed" {
		return nil, fmt.Errorf("项目必须先归档，且不能正在删除")
	}
	var id int64
	err = tx.QueryRow(`INSERT INTO public.project_deletion_jobs(project_id,project_name,state,phase,error,finished_at)
		VALUES ($1,$2,'queued','queued','',NULL)
		ON CONFLICT(project_id) DO UPDATE SET project_name=EXCLUDED.project_name,state='queued',phase='queued',error='',finished_at=NULL,updated_at=now()
		WHERE project_deletion_jobs.state='failed'
		RETURNING id`, projectID, projectName).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("项目删除作业已在队列中")
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE public.projects SET status='deleting' WHERE id=$1`, projectID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.ProjectDeletionJob(projectID)
}

// ClaimProjectDeletionJob 将指定等待作业标记为运行中并增加尝试次数。
func (d *DB) ClaimProjectDeletionJob(id int64) (*ProjectDeletionJob, error) {
	return scanProjectDeletionJob(d.QueryRow(`UPDATE public.project_deletion_jobs
		SET state='running',attempts=attempts+1,started_at=now(),finished_at=NULL,error='',updated_at=now()
		WHERE id=$1 AND state='queued' RETURNING `+projectDeletionJobCols, id))
}

// ClaimNextProjectDeletionJob 并发安全地领取队列中的下一项删除作业。
func (d *DB) ClaimNextProjectDeletionJob() (*ProjectDeletionJob, error) {
	return scanProjectDeletionJob(d.QueryRow(`WITH next_job AS (
		SELECT id FROM public.project_deletion_jobs WHERE state='queued' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE public.project_deletion_jobs j SET state='running',attempts=attempts+1,started_at=now(),finished_at=NULL,error='',updated_at=now()
	FROM next_job WHERE j.id=next_job.id RETURNING j.id,j.project_id,j.project_name,j.state,j.phase,j.error,j.attempts,j.created_at,j.started_at,j.finished_at`))
}

// UpdateProjectDeletionPhase 持久化当前清理阶段，便于失败定位和重启恢复。
func (d *DB) UpdateProjectDeletionPhase(id int64, phase string) error {
	_, err := d.Exec(`UPDATE public.project_deletion_jobs SET phase=$2,updated_at=now() WHERE id=$1 AND state='running'`, id, phase)
	return err
}

// FailProjectDeletionJob 保存失败原因并将项目状态改为可重试。
func (d *DB) FailProjectDeletionJob(id, projectID int64, cause error) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE public.project_deletion_jobs SET state='failed',error=$2,finished_at=now(),updated_at=now() WHERE id=$1`, id, cause.Error()); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE public.projects SET status='deletion_failed' WHERE id=$1 AND status='deleting'`, projectID); err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteProjectDeletionJob 将删除作业标记为完成并记录结束时间。
func (d *DB) CompleteProjectDeletionJob(id int64) error {
	_, err := d.Exec(`UPDATE public.project_deletion_jobs SET state='completed',phase='complete',error='',finished_at=now(),updated_at=now() WHERE id=$1`, id)
	return err
}

// RecoverProjectDeletionJobs 将进程中断时遗留的运行中作业放回队列。
func (d *DB) RecoverProjectDeletionJobs() error {
	_, err := d.Exec(`UPDATE public.project_deletion_jobs SET state='queued',updated_at=now() WHERE state='running'`)
	return err
}

// ProjectDeletionJob 按项目 ID 读取最近一条删除作业记录。
func (d *DB) ProjectDeletionJob(projectID int64) (*ProjectDeletionJob, error) {
	return scanProjectDeletionJob(d.QueryRow(`SELECT `+projectDeletionJobCols+` FROM public.project_deletion_jobs WHERE project_id=$1`, projectID))
}
