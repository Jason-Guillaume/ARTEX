package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Functional tables live in a per-project schema. System tables stay in public
// and are reached through search_path ("proj_N, public"), so LLM、Agent、工具、
// 拦截规则、通知渠道这些系统配置不随项目切换。
var functionalTables = []string{
	"companies", "assets", "company_scope",
	"explorations", "exploration_nodes", "exploration_edges", "exploration_anchors",
	"task_constraints", "activity", "main_sessions",
	"task_categories", "tasks", "task_archives", "task_templates", "project_preferences",
	"task_relations", "task_asset_links", "task_llm_profiles", "task_scope",
	"conversations", "conversation_activities",
	"side_question_sessions", "side_question_requests",
	"findings", "finding_retests", "traffic_evidence_snapshots", "finding_traffic_bindings",
	"task_intercept_rules", "intercept_pending",
	"llm_records", "asset_sync_batches",
}

// systemTables must remain in public; project provisioning does not create them.
var systemTables = []string{
	"projects", "settings", "llm_profiles", "llm_profile_health", "llm_usage",
	"agents", "agent_prompts", "agent_prompt_vars", "agent_triggers",
	"mcp_servers", "mcp_tools_cache", "agent_visibility", "agent_skill_visibility",
	"skill_usage", "tool_usage", "tools", "scheduler_state",
	"intercept_rules", "server_logs", "asset_intercept_rules",
	"notification_channels", "notification_events", "notification_deliveries", "project_audit", "project_deletion_jobs", "schema_migrations",
}

var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

type projectContextKey struct{}

func ContextWithProject(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, projectContextKey{}, id)
}

func ProjectFromContext(ctx context.Context) int64 {
	id, _ := ctx.Value(projectContextKey{}).(int64)
	return id
}

func searchPathForContext(ctx context.Context) (string, error) {
	id := ProjectFromContext(ctx)
	if id <= 0 {
		id = CurrentProject()
	}
	if id > 0 {
		s, ok := schemaOf(id)
		if !ok || !identRe.MatchString(s) {
			return "", fmt.Errorf("项目不存在")
		}
		return s + ", public", nil
	}
	return SearchPath(), nil
}

// Project is one pentest engagement. Its functional rows live in Schema.
type Project struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Schema      string    `json:"schema_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var (
	projMu     sync.Mutex
	projByGID  = map[uint64]int64{}
	schemaByID = map[int64]string{}
)

var defaultSearchPath atomic.Value // string

func init() {
	defaultSearchPath.Store("public")
}

func goroutineID() uint64 {
	var buf [48]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine 123 ["
	s := string(buf[:n])
	s = strings.TrimPrefix(s, "goroutine ")
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	id, _ := strconv.ParseUint(s, 10, 64)
	return id
}

// CurrentProject is the functional schema bound to this goroutine. 0 means
// "use the default project" (startup, tests, and system background work that
// has not picked a project yet).
func CurrentProject() int64 {
	projMu.Lock()
	defer projMu.Unlock()
	return projByGID[goroutineID()]
}

// DefaultProjectID is the project that holds data migrated from the
// single-tenant installation. It is 1 after bootstrap.
func DefaultProjectID() int64 {
	projMu.Lock()
	defer projMu.Unlock()
	if _, ok := schemaByID[1]; ok {
		return 1
	}
	for id := range schemaByID {
		return id
	}
	return 1
}

func rememberProject(id int64, schema string) {
	projMu.Lock()
	schemaByID[id] = schema
	projMu.Unlock()
}

func forgetProject(id int64) {
	projMu.Lock()
	delete(schemaByID, id)
	projMu.Unlock()
}

func schemaOf(id int64) (string, bool) {
	projMu.Lock()
	defer projMu.Unlock()
	s, ok := schemaByID[id]
	return s, ok
}

// SearchPath is the PostgreSQL search_path for the current goroutine.
func SearchPath() string {
	if id := CurrentProject(); id > 0 {
		if s, ok := schemaOf(id); ok && identRe.MatchString(s) {
			return s + ", public"
		}
	}
	if v := defaultSearchPath.Load(); v != nil {
		if s, _ := v.(string); s != "" {
			return s
		}
	}
	return "public"
}

// PushProject binds id for this goroutine until the returned function runs.
// id <= 0 leaves the current binding unchanged.
func PushProject(id int64) func() {
	if id <= 0 {
		return func() {}
	}
	gid := goroutineID()
	projMu.Lock()
	prev, ok := projByGID[gid]
	projByGID[gid] = id
	projMu.Unlock()
	return func() {
		projMu.Lock()
		if ok {
			projByGID[gid] = prev
		} else {
			delete(projByGID, gid)
		}
		projMu.Unlock()
	}
}

// WithProject runs fn with id bound to this goroutine.
func WithProject(id int64, fn func() error) error {
	if id <= 0 {
		return fn()
	}
	restore := PushProject(id)
	defer restore()
	return fn()
}

// AcrossProjects calls fn once per project, with that project's schema bound.
// fn runs sequentially. An empty registry falls back to one unbound call.
func (d *DB) AcrossProjects(fn func(projectID int64) error) error {
	projects, err := d.ListProjects()
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return fn(0)
	}
	for _, p := range projects {
		if err := WithProject(p.ID, func() error { return fn(p.ID) }); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) ListProjects() ([]Project, error) {
	rows, err := d.Query(`SELECT id, name, description, schema_name, status, created_at, updated_at FROM public.projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Schema, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if !identRe.MatchString(p.Schema) {
			return nil, fmt.Errorf("项目 %d 的 schema 名无效", p.ID)
		}
		rememberProject(p.ID, p.Schema)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) Project(id int64) (Project, error) {
	var p Project
	err := d.QueryRow(`SELECT id, name, description, schema_name, status, created_at, updated_at FROM public.projects WHERE id=$1`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.Schema, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, sql.ErrNoRows
	}
	if err != nil {
		return Project{}, err
	}
	if !identRe.MatchString(p.Schema) {
		return Project{}, fmt.Errorf("项目 schema 无效")
	}
	rememberProject(p.ID, p.Schema)
	return p, nil
}

// KnownProject reports whether id was loaded into the in-process registry.
func KnownProject(id int64) bool {
	_, ok := schemaOf(id)
	return ok
}

func (d *DB) tableIn(schema, table string) (bool, error) {
	var one int
	err := d.QueryRow(`SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2`, schema, table).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (d *DB) bootstrapProjects(ctx context.Context, conn *sql.Conn) error {
	defaultSearchPath.Store("public")
	if err := applySQLWithRetry(ctx, conn, scopedSchema("system"), time.Sleep); err != nil {
		return fmt.Errorf("系统数据库迁移: %w", err)
	}
	if err := recordSystemSchemaMigration(d); err != nil {
		return fmt.Errorf("记录系统数据库迁移版本: %w", err)
	}
	// 必须先登记默认项目：旧库中的通知事件在补 project_id 时会默认归属项目 1，
	// 随后的外键约束验证需要该项目已存在。
	if err := d.ensureProjectRegistry(); err != nil {
		return err
	}
	// Upgrade in place while the single-tenant tables are still in public,
	// then move them. Later boots only migrate each project schema.
	inPublic, err := d.tableIn("public", "tasks")
	if err != nil {
		return err
	}
	if inPublic || !d.projectSchemaReady() {
		if err := applySchemaWithRetry(ctx, conn, time.Sleep); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}
	// These two tables are created from Go, not schema.sql. Do not call the
	// Ensure* helpers here: they take the schema advisory lock, and this
	// function already holds that lock on another connection.
	if _, err := d.Exec(llmUsageSchema); err != nil {
		return fmt.Errorf("llm_usage: %w", err)
	}
	if inPublic {
		// 只在从单项目版本升级时在 public 里建/补 llm_records（随后连同数据
		// 一起搬进 proj_1）。平时每次启动若也在 public 建一遍，moveFunctionalTables
		// 会因 proj_1 已存在而跳过，public 里就永远残留一份空副本。
		// 新装库的 llm_records 由 provisionProjectSchema 在项目 schema 里创建。
		if _, err := d.Exec(llmRecordsSchema); err != nil {
			return fmt.Errorf("llm_records: %w", err)
		}
		if _, err := d.Exec(llmRecordsMigrate); err != nil {
			return fmt.Errorf("llm_records: %w", err)
		}
	}
	if err := d.moveFunctionalTables("proj_1"); err != nil {
		return err
	}
	if err := d.moveSequencesToPublic("proj_1"); err != nil {
		return err
	}
	projects, err := d.ListProjects()
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return fmt.Errorf("没有可用项目")
	}
	defaultSearchPath.Store(projects[0].Schema + ", public")
	for _, p := range projects {
		if err := d.provisionProjectSchema(ctx, p); err != nil {
			return fmt.Errorf("项目 %s: %w", p.Name, err)
		}
	}
	// Default reads and tests land in the migrated project.
	if s, ok := schemaOf(DefaultProjectID()); ok {
		defaultSearchPath.Store(s + ", public")
	}
	return nil
}

func (d *DB) projectSchemaReady() bool {
	ok, err := d.tableIn("proj_1", "tasks")
	return err == nil && ok
}

func (d *DB) ensureProjectRegistry() error {
	// schema.sql creates public.projects. Insert the default engagement that
	// receives every row written before projects existed.
	_, err := d.Exec(`
INSERT INTO public.projects(id, name, description, schema_name)
VALUES (1, '默认项目', '升级前的全部功能数据', 'proj_1')
ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("创建默认项目: %w", err)
	}
	_, _ = d.Exec(`SELECT setval('public.projects_id_seq', GREATEST((SELECT COALESCE(MAX(id),1) FROM public.projects), 1))`)
	rememberProject(1, "proj_1")
	return nil
}

func (d *DB) moveFunctionalTables(schema string) error {
	if !identRe.MatchString(schema) {
		return fmt.Errorf("非法 schema %q", schema)
	}
	if _, err := d.Exec(`CREATE SCHEMA IF NOT EXISTS ` + schema); err != nil {
		return err
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, table := range functionalTables {
		inPublic, err := tableExistsTx(tx, "public", table)
		if err != nil {
			return err
		}
		if !inPublic {
			continue
		}
		inDest, err := tableExistsTx(tx, schema, table)
		if err != nil {
			return err
		}
		if inDest {
			// 目标已存在（历史启动在 public 留下过同名表）。public 副本若是空的
			// 就是残留——真实数据在项目 schema 里，删掉以免干扰裸连接和
			// information_schema 统计；非空则保留（无法安全判断归属，留人工核对）。
			var n int
			if err := tx.QueryRow(`SELECT count(*) FROM public.` + table).Scan(&n); err == nil && n == 0 {
				if _, err := tx.Exec(`DROP TABLE public.` + table + ` CASCADE`); err != nil {
					return fmt.Errorf("删除 public 残留空表 %s: %w", table, err)
				}
			}
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE public.` + table + ` SET SCHEMA ` + schema); err != nil {
			return fmt.Errorf("移动 %s: %w", table, err)
		}
	}
	return tx.Commit()
}

func tableExistsTx(tx *sql.Tx, schema, table string) (bool, error) {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2`, schema, table).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (d *DB) moveSequencesToPublic(schema string) error {
	if !identRe.MatchString(schema) {
		return fmt.Errorf("非法 schema %q", schema)
	}
	rows, err := d.Query(`
SELECT c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind = 'S'`, schema)
	if err != nil {
		return err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if identRe.MatchString(name) {
			names = append(names, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range names {
		inPublic, err := d.sequenceIn("public", name)
		if err != nil {
			return err
		}
		if inPublic {
			continue
		}
		// serial 列持有的序列不能直接 SET SCHEMA（SQLSTATE 0A000），
		// 先解除持有关系再移动。列默认值按 OID 引用序列，跨 schema 移动后仍有效。
		if _, err := d.Exec(`ALTER SEQUENCE ` + schema + `.` + name + ` OWNED BY NONE`); err != nil {
			return fmt.Errorf("解除序列持有 %s: %w", name, err)
		}
		if _, err := d.Exec(`ALTER SEQUENCE ` + schema + `.` + name + ` SET SCHEMA public`); err != nil {
			return fmt.Errorf("移动序列 %s: %w", name, err)
		}
	}
	return nil
}

func (d *DB) sequenceIn(schema, name string) (bool, error) {
	var one int
	err := d.QueryRow(`
SELECT 1 FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname=$1 AND c.relname=$2 AND c.relkind='S'`, schema, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (d *DB) provisionProjectSchema(ctx context.Context, p Project) error {
	if !identRe.MatchString(p.Schema) {
		return fmt.Errorf("非法 schema %q", p.Schema)
	}
	if _, err := d.Exec(`CREATE SCHEMA IF NOT EXISTS ` + p.Schema); err != nil {
		return err
	}
	return WithProject(p.ID, func() error {
		if err := applySQLWithRetry(ctx, d, scopedSchema("project"), time.Sleep); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if _, err := d.Exec(llmRecordsSchema); err != nil {
			return err
		}
		if _, err := d.Exec(llmRecordsMigrate); err != nil {
			return err
		}
		if err := recordProjectSchemaMigration(d); err != nil {
			return err
		}
		return d.shareSerials(p.Schema)
	})
}

// shareSerials points a project schema's serial columns at the public sequences
// owned by the migrated default project, so task and asset ids stay globally unique.
type projectSQL interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
}

func (d *DB) shareSerials(schema string) error { return shareProjectSerials(d, schema) }

func shareProjectSerials(q projectSQL, schema string) error {
	rows, err := q.Query(`
SELECT c.relname, a.attname, s.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
JOIN pg_depend dep ON dep.refobjid = c.oid AND dep.refobjsubid = a.attnum AND dep.deptype = 'a'
JOIN pg_class s ON s.oid = dep.objid AND s.relkind = 'S'
JOIN pg_namespace sn ON sn.oid = s.relnamespace
WHERE n.nspname = $1 AND sn.nspname = $1`, schema)
	if err != nil {
		return err
	}
	defer rows.Close()
	type serial struct{ table, column, seq string }
	var items []serial
	for rows.Next() {
		var it serial
		if err := rows.Scan(&it.table, &it.column, &it.seq); err != nil {
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range items {
		if !identRe.MatchString(it.table) || !identRe.MatchString(it.column) || !identRe.MatchString(it.seq) {
			continue
		}
		var inPublic bool
		check, err := q.Query(`SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1 AND c.relkind='S')`, it.seq)
		if err != nil {
			return err
		}
		if check.Next() {
			err = check.Scan(&inPublic)
		}
		check.Close()
		if err != nil {
			return err
		}
		if !inPublic {
			// 与 moveSequencesToPublic 同理：先解除 serial 列的持有关系才能跨 schema 移动。
			if _, err := q.Exec(`ALTER SEQUENCE ` + schema + `.` + it.seq + ` OWNED BY NONE`); err != nil {
				return err
			}
			if _, err := q.Exec(`ALTER SEQUENCE ` + schema + `.` + it.seq + ` SET SCHEMA public`); err != nil {
				return err
			}
			continue
		}
		if _, err := q.Exec(fmt.Sprintf(
			`ALTER TABLE %s.%s ALTER COLUMN %s SET DEFAULT nextval('public.%s')`,
			schema, it.table, it.column, it.seq)); err != nil {
			return err
		}
		if _, err := q.Exec(`ALTER SEQUENCE ` + schema + `.` + it.seq + ` OWNED BY NONE`); err != nil {
			return err
		}
		if _, err := q.Exec(`DROP SEQUENCE IF EXISTS ` + schema + `.` + it.seq); err != nil {
			return err
		}
	}
	return nil
}

// CreateProject allocates a schema, clones the functional DDL into it, and
// shares serial sequences with public so ids never collide across projects.
func (d *DB) CreateProject(ctx context.Context, name, description string) (*Project, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return nil, fmt.Errorf("项目名称不能为空")
	}
	if len([]rune(name)) > 80 {
		return nil, fmt.Errorf("项目名称过长")
	}
	if len([]rune(description)) > 500 {
		return nil, fmt.Errorf("项目说明过长")
	}
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, schemaMigrationLockKey); err != nil {
		return nil, err
	}
	var id int64
	placeholder := fmt.Sprintf("proj_new_%d", time.Now().UnixNano())
	if err := tx.QueryRow(`
INSERT INTO public.projects(name, description, schema_name)
VALUES ($1, $2, $3)
RETURNING id`, name, description, placeholder).Scan(&id); err != nil {
		return nil, err
	}
	schema := fmt.Sprintf("proj_%d", id)
	if _, err := tx.Exec(`UPDATE public.projects SET schema_name=$2 WHERE id=$1`, id, schema); err != nil {
		return nil, err
	}
	p := Project{ID: id, Name: name, Description: description, Schema: schema, Status: "active"}
	rememberProject(id, schema)
	err = WithProject(id, func() error {
		if _, err := tx.Exec(`CREATE SCHEMA ` + schema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, scopedSchema("project")); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, llmRecordsSchema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, llmRecordsMigrate); err != nil {
			return err
		}
		if err := recordProjectSchemaMigration(tx); err != nil {
			return err
		}
		if err := shareProjectSerials(tx, schema); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		forgetProject(id)
		return nil, err
	}
	p, err = d.Project(id)
	return &p, err
}

// UpdateProject renames a project. The schema name stays put.
func (d *DB) UpdateProject(id int64, name, description string) error {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return fmt.Errorf("项目名称不能为空")
	}
	if len([]rune(name)) > 80 || len([]rune(description)) > 500 {
		return fmt.Errorf("项目名称最多 80 字，说明最多 500 字")
	}
	res, err := d.Exec(`UPDATE public.projects SET name=$2, description=$3 WHERE id=$1`, id, name, description)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteProject 在删除作业状态下原子移除项目 schema、日志及注册记录。
func (d *DB) DeleteProject(id int64) error {
	if id == DefaultProjectID() {
		return fmt.Errorf("默认项目不能删除")
	}
	p, err := d.Project(id)
	if err != nil {
		return err
	}
	projects, err := d.ListProjects()
	if err != nil {
		return err
	}
	if len(projects) <= 1 {
		return fmt.Errorf("至少保留一个项目")
	}
	if p.Status != "deleting" {
		return fmt.Errorf("项目尚未进入删除流程")
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, schemaMigrationLockKey); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP SCHEMA IF EXISTS ` + p.Schema + ` CASCADE`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM public.server_logs WHERE project_id=$1`, id); err != nil {
		return fmt.Errorf("清理项目日志: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM public.projects WHERE id=$1`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	forgetProject(id)
	return nil
}

func (d *DB) SetProjectStatus(id int64, status string) error {
	if status != "active" && status != "archived" {
		return fmt.Errorf("项目状态无效")
	}
	if id == DefaultProjectID() && status != "active" {
		return fmt.Errorf("默认项目不能归档")
	}
	res, err := d.Exec(`UPDATE public.projects SET status=$2 WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
