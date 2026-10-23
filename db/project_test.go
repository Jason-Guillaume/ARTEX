package db

import (
	"context"
	"strings"
	"testing"
)

func TestProjectIsolationAndMigration(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a, err := d.CreateProject(t.Context(), "隔离 A", "同域名测试")
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.CreateProject(t.Context(), "隔离 B", "同域名测试")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, p := range []*Project{a, b} {
			_ = d.SetProjectStatus(p.ID, "archived")
			_ = d.DeleteProject(p.ID)
		}
	}()
	for _, p := range []*Project{a, b} {
		if err := WithProject(p.ID, func() error {
			_, err := d.Exec(`INSERT INTO assets(type,domain) VALUES ('root_domain','same.example')`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for _, table := range systemTables {
			var n int
			if err := d.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2`, p.Schema, table).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("项目中出现系统表 %s.%s", p.Schema, table)
			}
		}
		var fks int
		if err := d.QueryRow(`SELECT count(*) FROM pg_constraint WHERE conrelid=($1||'.tasks')::regclass AND confrelid='public.llm_profiles'::regclass`, p.Schema).Scan(&fks); err != nil {
			t.Fatal(err)
		}
		if fks < 2 {
			t.Fatalf("项目任务丢失 LLM 配置外键: %d", fks)
		}
	}
	if err := WithProject(a.ID, func() error { _, err := d.Exec(`DELETE FROM assets WHERE domain='same.example'`); return err }); err != nil {
		t.Fatal(err)
	}
	ctx := ContextWithProject(t.Context(), b.ID)
	var n int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM assets WHERE domain='same.example'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("B 数据受影响: %d %v", n, err)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT count(*) FROM assets WHERE domain='same.example'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("事务丢失项目上下文: %d %v", n, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	other, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.QueryRowContext(ctx, `SELECT count(*) FROM assets WHERE domain='same.example'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("重启迁移丢失项目数据: %d %v", n, err)
	}
	if err := d.DeleteProject(DefaultProjectID()); err == nil {
		t.Fatal("不应允许删除默认项目")
	}
	if err := d.SetProjectStatus(DefaultProjectID(), "archived"); err == nil {
		t.Fatal("不应允许归档默认项目")
	}
	if err := d.UpdateProject(a.ID, strings.Repeat("项", 81), ""); err == nil {
		t.Fatal("修改项目应校验名称长度")
	}
}

func TestProjectCreationRollback(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	before, err := d.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.CreateProject(ctx, "取消创建", ""); err == nil {
		t.Fatal("已取消的创建不应成功")
	}
	after, err := d.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatal("创建失败留下项目记录")
	}
	var orphans int
	if err := d.QueryRow(`SELECT count(*) FROM pg_namespace n WHERE n.nspname LIKE 'proj_%' AND n.nspname NOT IN (SELECT schema_name FROM public.projects)`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("创建失败留下 schema: %d", orphans)
	}
}
