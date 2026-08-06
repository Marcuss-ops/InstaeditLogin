package database

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"reflect"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
	"github.com/lib/pq"
)

const instrumentedPostgresDriverName = "postgres_instrumented"

type instrumentedDriver struct{ inner driver.Driver }

type instrumentedConn struct{ inner driver.Conn }
type instrumentedStmt struct{ inner driver.Stmt }

type instrumentedTx struct{ inner driver.Tx }

func init() {
	driver := &instrumentedDriver{inner: &pq.Driver{}}
	// This package is initialized once per process. A distinct driver name
	// avoids replacing lib/pq's standard registration and keeps tests that
	// explicitly open "postgres" unchanged.
	driverRegistry.Register(instrumentedPostgresDriverName, driver)
}

// driverRegistry is a tiny indirection used only to keep registration in one
// place and make the intent explicit. database/sql itself owns the registry.
type sqlDriverRegistry struct{}

func (sqlDriverRegistry) Register(name string, d driver.Driver) { driverRegister(name, d) }

var driverRegistry sqlDriverRegistry

// driverRegister is isolated so the only direct database/sql registration is
// easy to audit.
func driverRegister(name string, d driver.Driver) {
	stdsql.Register(name, d)
}

func (d *instrumentedDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &instrumentedConn{inner: conn}, nil
}

func (c *instrumentedConn) CheckNamedValue(value *driver.NamedValue) error {
	checker, ok := c.inner.(driver.NamedValueChecker)
	if !ok {
		return driver.ErrSkip
	}
	return checker.CheckNamedValue(value)
}

func (c *instrumentedConn) ResetSession(ctx context.Context) error {
	resetter, ok := c.inner.(driver.SessionResetter)
	if !ok {
		return nil
	}
	return resetter.ResetSession(ctx)
}

func (c *instrumentedConn) IsValid() bool {
	validator, ok := c.inner.(driver.Validator)
	return !ok || validator.IsValid()
}

func (c *instrumentedConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.inner.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &instrumentedStmt{inner: stmt}, nil
}

func (c *instrumentedConn) Close() error { return c.inner.Close() }
func (c *instrumentedConn) Begin() (driver.Tx, error) {
	tx, err := c.inner.Begin()
	if err != nil {
		return nil, err
	}
	return &instrumentedTx{inner: tx}, nil
}

func (c *instrumentedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	preparer, ok := c.inner.(driver.ConnPrepareContext)
	if !ok {
		stmt, err := c.inner.Prepare(query)
		if err != nil {
			return nil, err
		}
		return &instrumentedStmt{inner: stmt}, nil
	}
	stmt, err := preparer.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &instrumentedStmt{inner: stmt}, nil
}

func (c *instrumentedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.inner.(driver.ConnBeginTx)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	tx, err := beginner.BeginTx(ctx, opts)
	metrics.ObserveSQL(ctx, time.Since(start))
	if err != nil {
		return nil, err
	}
	return &instrumentedTx{inner: tx}, nil
}

func (c *instrumentedConn) Ping(ctx context.Context) error {
	pinger, ok := c.inner.(driver.Pinger)
	if !ok {
		return driver.ErrSkip
	}
	start := time.Now()
	err := pinger.Ping(ctx)
	metrics.ObserveSQL(ctx, time.Since(start))
	return err
}

func (c *instrumentedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.inner.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	result, err := execer.ExecContext(ctx, query, args)
	metrics.ObserveSQL(ctx, time.Since(start))
	return result, err
}

func (c *instrumentedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.inner.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	rows, err := queryer.QueryContext(ctx, query, args)
	if rows == nil {
		metrics.ObserveSQL(ctx, time.Since(start))
		return nil, err
	}
	return &instrumentedRows{Rows: rows, started: start, ctx: ctx}, err
}

func (c *instrumentedConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	execer, ok := c.inner.(driver.Execer)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	result, err := execer.Exec(query, args)
	metrics.ObserveSQL(context.Background(), time.Since(start))
	return result, err
}

func (c *instrumentedConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	queryer, ok := c.inner.(driver.Queryer)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	rows, err := queryer.Query(query, args)
	metrics.ObserveSQL(context.Background(), time.Since(start))
	return rows, err
}

func (s *instrumentedStmt) Close() error  { return s.inner.Close() }
func (s *instrumentedStmt) NumInput() int { return s.inner.NumInput() }
func (s *instrumentedStmt) Exec(args []driver.Value) (driver.Result, error) {
	start := time.Now()
	result, err := s.inner.Exec(args)
	metrics.ObserveSQL(context.Background(), time.Since(start))
	return result, err
}
func (s *instrumentedStmt) Query(args []driver.Value) (driver.Rows, error) {
	start := time.Now()
	rows, err := s.inner.Query(args)
	metrics.ObserveSQL(context.Background(), time.Since(start))
	return rows, err
}
func (s *instrumentedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	stmt, ok := s.inner.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	result, err := stmt.ExecContext(ctx, args)
	metrics.ObserveSQL(ctx, time.Since(start))
	return result, err
}
func (s *instrumentedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	stmt, ok := s.inner.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	rows, err := stmt.QueryContext(ctx, args)
	metrics.ObserveSQL(ctx, time.Since(start))
	return rows, err
}

type instrumentedRows struct {
	driver.Rows
	started time.Time
	ctx     context.Context
	done    bool
}

func (r *instrumentedRows) HasNextResultSet() bool {
	next, ok := r.Rows.(driver.RowsNextResultSet)
	return ok && next.HasNextResultSet()
}

func (r *instrumentedRows) NextResultSet() error {
	next, ok := r.Rows.(driver.RowsNextResultSet)
	if !ok {
		return driver.ErrSkip
	}
	return next.NextResultSet()
}

func (r *instrumentedRows) ColumnTypeDatabaseTypeName(index int) string {
	columns, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName)
	if !ok {
		return ""
	}
	return columns.ColumnTypeDatabaseTypeName(index)
}

func (r *instrumentedRows) ColumnTypeLength(index int) (length int64, ok bool) {
	columns, exists := r.Rows.(driver.RowsColumnTypeLength)
	if !exists {
		return 0, false
	}
	return columns.ColumnTypeLength(index)
}

func (r *instrumentedRows) ColumnTypeNullable(index int) (nullable, ok bool) {
	columns, exists := r.Rows.(driver.RowsColumnTypeNullable)
	if !exists {
		return false, false
	}
	return columns.ColumnTypeNullable(index)
}

func (r *instrumentedRows) ColumnTypePrecisionScale(index int) (precision, scale int64, ok bool) {
	columns, exists := r.Rows.(driver.RowsColumnTypePrecisionScale)
	if !exists {
		return 0, 0, false
	}
	return columns.ColumnTypePrecisionScale(index)
}

func (r *instrumentedRows) ColumnTypeScanType(index int) reflect.Type {
	columns, ok := r.Rows.(driver.RowsColumnTypeScanType)
	if !ok {
		return nil
	}
	return columns.ColumnTypeScanType(index)
}

func (r *instrumentedRows) Close() error {
	if !r.done {
		r.done = true
		metrics.ObserveSQL(r.ctx, time.Since(r.started))
	}
	return r.Rows.Close()
}

func (t *instrumentedTx) Commit() error   { return t.inner.Commit() }
func (t *instrumentedTx) Rollback() error { return t.inner.Rollback() }

// Keep the standard driver interfaces visible to database/sql where the
// wrapped lib/pq connection supports them.
var (
	_ driver.Driver                         = (*instrumentedDriver)(nil)
	_ driver.Conn                           = (*instrumentedConn)(nil)
	_ driver.Stmt                           = (*instrumentedStmt)(nil)
	_ driver.Tx                             = (*instrumentedTx)(nil)
	_ driver.Pinger                         = (*instrumentedConn)(nil)
	_ driver.NamedValueChecker              = (*instrumentedConn)(nil)
	_ driver.SessionResetter                = (*instrumentedConn)(nil)
	_ driver.Validator                      = (*instrumentedConn)(nil)
	_ driver.ExecerContext                  = (*instrumentedConn)(nil)
	_ driver.QueryerContext                 = (*instrumentedConn)(nil)
	_ driver.ConnPrepareContext             = (*instrumentedConn)(nil)
	_ driver.ConnBeginTx                    = (*instrumentedConn)(nil)
	_ driver.StmtExecContext                = (*instrumentedStmt)(nil)
	_ driver.StmtQueryContext               = (*instrumentedStmt)(nil)
	_ driver.Rows                           = (*instrumentedRows)(nil)
	_ driver.RowsNextResultSet              = (*instrumentedRows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*instrumentedRows)(nil)
	_ driver.RowsColumnTypeLength           = (*instrumentedRows)(nil)
	_ driver.RowsColumnTypeNullable         = (*instrumentedRows)(nil)
	_ driver.RowsColumnTypePrecisionScale   = (*instrumentedRows)(nil)
	_ driver.RowsColumnTypeScanType         = (*instrumentedRows)(nil)
)
