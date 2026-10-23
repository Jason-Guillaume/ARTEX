package db

import (
	"context"
	"database/sql/driver"
	"fmt"
)

// scopeConnector sets search_path on every operation from the goroutine's
// current project. System tables stay in public and stay visible because the
// path is always "proj_N, public".
type scopeConnector struct {
	base driver.Connector
}

func newScopeConnector(base driver.Connector) driver.Connector {
	return scopeConnector{base: base}
}

func (c scopeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &scopeConn{Conn: raw}, nil
}

func (c scopeConnector) Driver() driver.Driver { return c.base.Driver() }

type scopeConn struct {
	driver.Conn
	// lastPath 是该连接当前已生效的 search_path（set_config 是会话级设置）。
	// 空串表示本连接还没有应用过。
	lastPath string
	txPath   string // nonempty only for an explicitly project-bound transaction
}

func (c *scopeConn) apply(ctx context.Context) error {
	want, err := searchPathForContext(ctx)
	if err != nil {
		return err
	}
	if c.txPath != "" {
		if ProjectFromContext(ctx) > 0 && want != c.txPath {
			return fmt.Errorf("事务不能跨项目执行")
		}
		want = c.txPath
	}
	if want == c.lastPath {
		// 路径没变就不发 set_config。这不只是省一次往返：事务处于 aborted
		// 状态时，任何多余语句都会以 25P02 失败并掩盖真正的语句——
		// ROLLBACK TO SAVEPOINT 必须能原样送达（见 notify 事件保存点）。
		return nil
	}
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return fmt.Errorf("postgres 连接不支持按项目切换 search_path")
	}
	_, err = execer.ExecContext(ctx, `SELECT set_config('search_path', $1, false)`, []driver.NamedValue{{
		Ordinal: 1,
		Value:   want,
	}})
	if err != nil {
		return err
	}
	c.lastPath = want
	return nil
}

func (c *scopeConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.apply(ctx); err != nil {
		return nil, err
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *scopeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.apply(ctx); err != nil {
		return nil, err
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *scopeConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := c.apply(ctx); err != nil {
		return nil, err
	}
	if prep, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return prep.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

func (c *scopeConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if err := c.apply(ctx); err != nil {
		return nil, err
	}
	var tx driver.Tx
	var err error
	if begin, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = begin.BeginTx(ctx, opts)
	} else {
		tx, err = c.Conn.Begin()
	}
	if err != nil {
		return nil, err
	}
	if ProjectFromContext(ctx) > 0 {
		c.txPath = c.lastPath
	}
	return &scopeTx{Tx: tx, conn: c}, nil
}

type scopeTx struct {
	driver.Tx
	conn *scopeConn
}

func (t *scopeTx) Commit() error {
	defer t.reset()
	return t.Tx.Commit()
}

func (t *scopeTx) Rollback() error {
	defer t.reset()
	return t.Tx.Rollback()
}

func (t *scopeTx) reset() { t.conn.txPath = ""; t.conn.lastPath = "" }

func (c *scopeConn) CheckNamedValue(v *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(v)
	}
	return nil
}

func (c *scopeConn) ResetSession(ctx context.Context) error {
	if reset, ok := c.Conn.(driver.SessionResetter); ok {
		return reset.ResetSession(ctx)
	}
	return nil
}

func (c *scopeConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *scopeConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}
