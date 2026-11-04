package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	duckdbDriver "github.com/marcboeker/go-duckdb"

	"github.com/coral-mesh/coral/internal/retry"
)

// Execer is an interface that matches both *sql.DB and *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Table represents a generic database table wrapper for type T.
type Table[T any] struct {
	db              Execer
	tableName       string
	columns         []string
	pkColumns       []string
	immutableFields map[string]bool // Fields that can't be updated
	fieldMap        map[string]int  // Map column name to field index

	// stageExprs holds, per column, the SELECT expression that types the
	// column of the BatchUpsert staging table. It is nil when a field type
	// is not supported by the appender, in which case BatchUpsert falls back
	// to row-by-row prepared statements.
	stageExprs []string
}

// stageSeqColumn records each row's position within a BatchUpsert batch so
// that duplicate keys resolve in input order.
const stageSeqColumn = "coral_stage_seq"

var (
	timeType        = reflect.TypeOf(time.Time{})
	int64SliceType  = reflect.TypeOf([]int64(nil))
	byteSliceType   = reflect.TypeOf([]byte(nil))
	driverValuerTyp = reflect.TypeOf((*driver.Valuer)(nil)).Elem()
)

// NewTable creates a new Table[T] instance.
// T must be a struct with `duckdb` tags.
func NewTable[T any](db Execer, tableName string) *Table[T] {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic("Table generic type T must be a struct")
	}

	var columns []string
	var pkColumns []string
	immutableFields := make(map[string]bool)
	fieldMap := make(map[string]int)
	stageExprs := []string{}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("duckdb")
		if tag == "" || tag == "-" {
			continue
		}

		parts := strings.Split(tag, ",")
		colName := strings.TrimSpace(parts[0])
		columns = append(columns, colName)
		fieldMap[colName] = i

		if stageExprs != nil {
			if expr, ok := stagingColumnExpr(field.Type, colName); ok {
				stageExprs = append(stageExprs, expr)
			} else {
				stageExprs = nil
			}
		}

		for _, p := range parts[1:] {
			opt := strings.TrimSpace(p)
			switch opt {
			case "pk":
				pkColumns = append(pkColumns, colName)
			case "immutable":
				immutableFields[colName] = true
			}
		}
	}

	return &Table[T]{
		db:              db,
		tableName:       tableName,
		columns:         columns,
		pkColumns:       pkColumns,
		immutableFields: immutableFields,
		fieldMap:        fieldMap,
		stageExprs:      stageExprs,
	}
}

// stagingColumnExpr returns the SELECT expression that types a staging table
// column for a field of type rt. Types mirror what database/sql's default
// converter hands to the DuckDB driver on the prepared-statement path, so that
// INSERT ... SELECT from the staging table applies the same casts. time.Time
// takes the target column's type, as the driver binds it against the
// parameter type. It reports false for types the appender cannot take.
func stagingColumnExpr(rt reflect.Type, col string) (string, bool) {
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Implements(driverValuerTyp) || reflect.PointerTo(rt).Implements(driverValuerTyp) {
		return "", false
	}

	var sqlType string
	switch rt {
	case timeType:
		return quoteIdent(col), true
	case int64SliceType:
		sqlType = "BIGINT[]"
	case byteSliceType:
		sqlType = "BLOB"
	default:
		switch rt.Kind() {
		case reflect.Bool:
			sqlType = "BOOLEAN"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			sqlType = "BIGINT"
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			sqlType = "UBIGINT"
		case reflect.Float32, reflect.Float64:
			sqlType = "DOUBLE"
		case reflect.String:
			sqlType = "VARCHAR"
		default:
			return "", false
		}
	}
	return fmt.Sprintf("CAST(NULL AS %s) AS %s", sqlType, quoteIdent(col)), true
}

// appenderValue converts a struct field to a value accepted by the DuckDB
// appender for the staging column built by stagingColumnExpr.
func appenderValue(v reflect.Value) driver.Value {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint()
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.String:
		return v.String()
	default:
		return v.Interface()
	}
}

// quoteIdent quotes a SQL identifier.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// onConflictClause returns the ON CONFLICT clause for upserts, or "" when the
// table has no primary key. PK and immutable columns are never updated.
func (t *Table[T]) onConflictClause() string {
	if len(t.pkColumns) == 0 {
		return ""
	}
	updates := make([]string, 0, len(t.columns))
	for _, col := range t.columns {
		if !t.isPK(col) && !t.immutableFields[col] {
			updates = append(updates, fmt.Sprintf("%s = excluded.%s", col, col))
		}
	}
	action := "DO NOTHING"
	if len(updates) > 0 {
		action = "DO UPDATE SET " + strings.Join(updates, ", ")
	}
	return fmt.Sprintf(" ON CONFLICT (%s) %s", strings.Join(t.pkColumns, ", "), action)
}

// isPK reports whether col is part of the primary key.
func (t *Table[T]) isPK(col string) bool {
	for _, pk := range t.pkColumns {
		if pk == col {
			return true
		}
	}
	return false
}

// convertFieldValue converts a Go value to a DuckDB-compatible parameter.
// The DuckDB Go driver doesn't support Go slices as query parameters,
// so slice types must be converted to DuckDB array literal strings.
func convertFieldValue(v interface{}) interface{} {
	switch val := v.(type) {
	case []int64:
		return Int64ArrayToString(val)
	default:
		return v
	}
}

// Upsert inserts or updates an item in the database.
// It generates an INSERT ... ON CONFLICT statement.
func (t *Table[T]) Upsert(ctx context.Context, item *T) error {
	placeholders := make([]string, len(t.columns))
	values := make([]interface{}, len(t.columns))
	updates := make([]string, 0, len(t.columns))

	val := reflect.ValueOf(item)
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}

	for i, col := range t.columns {
		placeholders[i] = "?"
		fieldIdx := t.fieldMap[col]
		values[i] = convertFieldValue(val.Field(fieldIdx).Interface())

		// Exclude PKs and immutable fields from update set
		isPK := false
		for _, pk := range t.pkColumns {
			if pk == col {
				isPK = true
				break
			}
		}
		// Skip if it's a PK or marked as immutable
		if !isPK && !t.immutableFields[col] {
			updates = append(updates, fmt.Sprintf("%s = excluded.%s", col, col))
		}
	}

	// #nosec G201 - table and column names are not user input, they come from struct tags
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		t.tableName,
		strings.Join(t.columns, ", "),
		strings.Join(placeholders, ", "),
	)

	if len(t.pkColumns) > 0 {
		conflictTarget := strings.Join(t.pkColumns, ", ")
		updateClause := ""
		if len(updates) > 0 {
			updateClause = fmt.Sprintf("DO UPDATE SET %s", strings.Join(updates, ", "))
		} else {
			updateClause = "DO NOTHING"
		}
		query += fmt.Sprintf(" ON CONFLICT (%s) %s", conflictTarget, updateClause)
	}

	// Retry mechanism for conflicts with increased attempts for high concurrency.
	cfg := retry.Config{
		MaxRetries:     10,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		Jitter:         0.1,
	}

	return t.withRetry(ctx, cfg, func() error {
		_, err := t.db.ExecContext(ctx, query, values...)
		return err
	})
}

// Insert inserts a new item into the database.
// It generates a plain INSERT statement without ON CONFLICT handling.
// Use this when you know the item doesn't exist and want to fail on duplicates.
func (t *Table[T]) Insert(ctx context.Context, item *T) error {
	placeholders := make([]string, len(t.columns))
	values := make([]interface{}, len(t.columns))

	val := reflect.ValueOf(item)
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}

	for i, col := range t.columns {
		placeholders[i] = "?"
		fieldIdx := t.fieldMap[col]
		values[i] = convertFieldValue(val.Field(fieldIdx).Interface())
	}

	// #nosec G201 - table and column names are not user input, they come from struct tags
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		t.tableName,
		strings.Join(t.columns, ", "),
		strings.Join(placeholders, ", "),
	)

	// Retry mechanism for conflicts.
	cfg := retry.Config{
		MaxRetries:     10,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		Jitter:         0.1,
	}

	return t.withRetry(ctx, cfg, func() error {
		_, err := t.db.ExecContext(ctx, query, values...)
		return err
	})
}

// BatchUpsert inserts or updates multiple items atomically.
//
// On a *sql.DB backed by the DuckDB driver, rows are streamed through the
// DuckDB appender into a connection-local staging table and merged into the
// target with a single INSERT ... SELECT ... ON CONFLICT statement. When a
// batch holds several items with the same primary key, the result matches
// applying them one by one: mutable columns take the last item's values and
// immutable columns keep the first item's values.
//
// Inside a *sql.Tx, or when a field type is not supported by the appender, it
// falls back to executing a prepared statement per item. Inside a *sql.Tx,
// conflicts are not retried: DuckDB aborts the transaction on its first error,
// so the caller must retry the whole transaction.
func (t *Table[T]) BatchUpsert(ctx context.Context, items []*T) error {
	if len(items) == 0 {
		return nil
	}

	cfg := retry.Config{
		MaxRetries:     30,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		Jitter:         0.1,
	}

	if db, ok := t.db.(*sql.DB); ok && t.stageExprs != nil {
		err := retry.Do(ctx, cfg, func() error {
			return t.batchUpsertStaged(ctx, db, items)
		}, IsTransactionConflict)
		if !errors.Is(err, errAppenderUnavailable) {
			return err
		}
	}

	return t.batchUpsertPrepared(ctx, cfg, items)
}

// errAppenderUnavailable reports that the underlying driver connection is not
// a DuckDB connection, so the appender cannot be used.
var errAppenderUnavailable = errors.New("duckdb appender unavailable")

// stageTable returns the name of the connection-local staging table.
func (t *Table[T]) stageTable() string {
	return quoteIdent("coral_stage_" + t.tableName)
}

// batchUpsertStaged implements BatchUpsert through a staging table filled by
// the DuckDB appender.
func (t *Table[T]) batchUpsertStaged(ctx context.Context, db *sql.DB, items []*T) error {
	// Temp tables and the appender are bound to a single connection.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	stage := t.stageTable()

	// #nosec G201 - table and column names are not user input, they come from struct tags
	createStage := fmt.Sprintf("CREATE OR REPLACE TEMP TABLE %s AS SELECT %s, CAST(NULL AS BIGINT) AS %s FROM %s LIMIT 0",
		stage, strings.Join(t.stageExprs, ", "), stageSeqColumn, t.tableName)
	if _, err := conn.ExecContext(ctx, createStage); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "DROP TABLE IF EXISTS "+stage)
	}()

	err = conn.Raw(func(driverConn any) error {
		dc, ok := driverConn.(*duckdbDriver.Conn)
		if !ok {
			return errAppenderUnavailable
		}
		appender, err := duckdbDriver.NewAppenderFromConn(dc, "", "coral_stage_"+t.tableName)
		if err != nil {
			return fmt.Errorf("create appender: %w", err)
		}

		row := make([]driver.Value, len(t.columns)+1)
		for seq, item := range items {
			val := reflect.ValueOf(item).Elem()
			for i, col := range t.columns {
				row[i] = appenderValue(val.Field(t.fieldMap[col]))
			}
			row[len(t.columns)] = int64(seq)
			if err := appender.AppendRow(row...); err != nil {
				return errors.Join(fmt.Errorf("append row: %w", err), appender.Close())
			}
		}
		if err := appender.Close(); err != nil {
			return fmt.Errorf("flush appender: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, t.mergeStageQuery(stage)); err != nil {
		return fmt.Errorf("merge staged rows: %w", err)
	}
	return nil
}

// mergeStageQuery builds the INSERT ... SELECT that moves staged rows into the
// target table. With a primary key, rows sharing a key are collapsed first,
// because a single INSERT cannot update the same row twice.
func (t *Table[T]) mergeStageQuery(stage string) string {
	cols := strings.Join(t.columns, ", ")

	if len(t.pkColumns) == 0 {
		// #nosec G201 - table and column names are not user input, they come from struct tags
		return fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s ORDER BY %s",
			t.tableName, cols, cols, stage, stageSeqColumn)
	}

	partition := "PARTITION BY " + strings.Join(t.pkColumns, ", ")
	selects := make([]string, len(t.columns))
	for i, col := range t.columns {
		if t.immutableFields[col] && !t.isPK(col) {
			selects[i] = fmt.Sprintf("first_value(%s) OVER (%s ORDER BY %s) AS %s",
				col, partition, stageSeqColumn, col)
		} else {
			selects[i] = col
		}
	}

	// #nosec G201 - table and column names are not user input, they come from struct tags
	return fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM (SELECT %s, row_number() OVER (%s ORDER BY %s DESC) AS coral_stage_rn FROM %s) WHERE coral_stage_rn = 1%s",
		t.tableName, cols, cols, strings.Join(selects, ", "), partition, stageSeqColumn, stage, t.onConflictClause())
}

// batchUpsertPrepared implements BatchUpsert with one prepared statement
// execution per item inside a transaction.
func (t *Table[T]) batchUpsertPrepared(ctx context.Context, cfg retry.Config, items []*T) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(t.columns)), ", ")

	// #nosec G201 - table and column names are not user input, they come from struct tags
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)%s",
		t.tableName,
		strings.Join(t.columns, ", "),
		placeholders,
		t.onConflictClause(),
	)

	return t.withRetry(ctx, cfg, func() error {
		// 3. Transact
		// Check if db is already a Tx or a DB
		var tx *sql.Tx
		var err error

		switch d := t.db.(type) {
		case *sql.Tx:
			tx = d
		case *sql.DB:
			tx, err = d.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("begin tx: %w", err)
			}
			defer func() {
				if err != nil {
					_ = tx.Rollback()
				}
			}()
		default:
			// Fallback for other Execer types (mocks etc), no explicit tx mgmt if not standard types
			// But BatchUpsert implies we want atomicity if possible.
			// For now assume standard sql.DB/Tx usage or compatible wrapper
			return fmt.Errorf("unsupported Execer type for BatchUpsert: %T", t.db)
		}

		// 4. Prepare Statement
		stmt, err := tx.PrepareContext(ctx, query)
		if err != nil {
			return fmt.Errorf("prepare stmt: %w", err)
		}
		defer func() { _ = stmt.Close() }()

		// 5. Exec Loop
		for _, item := range items {
			values := make([]interface{}, len(t.columns))
			val := reflect.ValueOf(item)
			if val.Kind() == reflect.Pointer {
				val = val.Elem()
			}

			for i, col := range t.columns {
				fieldIdx := t.fieldMap[col]
				values[i] = convertFieldValue(val.Field(fieldIdx).Interface())
			}

			_, err = stmt.ExecContext(ctx, values...)
			if err != nil {
				return fmt.Errorf("batch exec: %w", err)
			}
		}

		// 6. Commit (only if we started the tx)
		if _, started := t.db.(*sql.DB); started {
			if err = tx.Commit(); err != nil {
				return fmt.Errorf("commit: %w", err)
			}
		}

		return nil
	})
}

// Get retrieves a single item by its value in the first PK column.
// For composite PKs, use GetBy(map[string]interface{}) (TODO).
func (t *Table[T]) Get(ctx context.Context, id any) (*T, error) {
	if len(t.pkColumns) == 0 {
		return nil, errors.New("no primary key defined for table")
	}
	// Simple PK lookup support for now
	pk := t.pkColumns[0]

	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?",
		strings.Join(t.columns, ", "),
		t.tableName,
		pk,
	)

	row := t.db.QueryRowContext(ctx, query, id)
	return t.scanRow(row)
}

// Update updates an existing item in the database using a regular UPDATE statement.
// This is useful for updating indexed columns that DuckDB doesn't allow in ON CONFLICT.
// Only non-PK and non-immutable fields will be updated.
func (t *Table[T]) Update(ctx context.Context, item *T) error {
	if len(t.pkColumns) == 0 {
		return errors.New("no primary key defined for table")
	}

	val := reflect.ValueOf(item)
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}

	// Build SET clause (exclude PKs and immutable fields)
	var setClauses []string
	var values []interface{}

	for _, col := range t.columns {
		isPK := false
		for _, pk := range t.pkColumns {
			if pk == col {
				isPK = true
				break
			}
		}
		// Skip PKs and immutable fields
		if !isPK && !t.immutableFields[col] {
			setClauses = append(setClauses, fmt.Sprintf("%s = ?", col))
			fieldIdx := t.fieldMap[col]
			values = append(values, val.Field(fieldIdx).Interface())
		}
	}

	if len(setClauses) == 0 {
		return errors.New("no fields to update (all fields are either PK or immutable)")
	}

	// Build WHERE clause from PK columns
	var whereClauses []string
	for _, pk := range t.pkColumns {
		whereClauses = append(whereClauses, fmt.Sprintf("%s = ?", pk))
		fieldIdx := t.fieldMap[pk]
		values = append(values, val.Field(fieldIdx).Interface())
	}

	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		t.tableName,
		strings.Join(setClauses, ", "),
		strings.Join(whereClauses, " AND "),
	)

	// Retry mechanism for conflicts.
	cfg := retry.Config{
		MaxRetries:     10,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		Jitter:         0.1,
	}

	return t.withRetry(ctx, cfg, func() error {
		result, err := t.db.ExecContext(ctx, query, values...)
		if err != nil {
			return err
		}
		// Check if any rows were affected.
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to get rows affected: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("no rows updated (record may not exist)")
		}
		return nil
	})
}

// UpdateFields updates specific fields of an item by PK.
// This bypasses immutability checks and is useful for updating indexed fields.
// fieldUpdates is a map of column names to new values.
func (t *Table[T]) UpdateFields(ctx context.Context, pk any, fieldUpdates map[string]interface{}) error {
	if len(t.pkColumns) == 0 {
		return errors.New("no primary key defined for table")
	}
	if len(t.pkColumns) > 1 {
		return errors.New("UpdateFields only supports single-column primary keys")
	}
	if len(fieldUpdates) == 0 {
		return errors.New("no fields to update")
	}

	// Build SET clause
	var setClauses []string
	var values []interface{}

	for colName, value := range fieldUpdates {
		// Verify column exists
		if _, exists := t.fieldMap[colName]; !exists {
			return fmt.Errorf("column %s does not exist in table %s", colName, t.tableName)
		}
		// Don't allow updating PK
		if colName == t.pkColumns[0] {
			return fmt.Errorf("cannot update primary key column %s", colName)
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", colName))
		values = append(values, value)
	}

	// Add PK to WHERE clause
	values = append(values, pk)

	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s = ?",
		t.tableName,
		strings.Join(setClauses, ", "),
		t.pkColumns[0],
	)

	// Retry mechanism for conflicts.
	cfg := retry.Config{
		MaxRetries:     10,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		Jitter:         0.1,
	}

	return t.withRetry(ctx, cfg, func() error {
		result, err := t.db.ExecContext(ctx, query, values...)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to get rows affected: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("no rows updated (record may not exist)")
		}
		return nil
	})
}

// Delete removes an item by its value in the first PK column.
func (t *Table[T]) Delete(ctx context.Context, id any) error {
	if len(t.pkColumns) == 0 {
		return errors.New("no primary key defined for table")
	}
	pk := t.pkColumns[0]

	query := fmt.Sprintf("DELETE FROM %s WHERE %s = ?", t.tableName, pk)
	_, err := t.db.ExecContext(ctx, query, id)
	return err
}

// List retrieves all items with optional filters.
// filters are simple "column = value" pairs.
func (t *Table[T]) List(ctx context.Context, filters map[string]interface{}) ([]*T, error) {
	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(t.columns, ", "), t.tableName)
	var args []interface{}

	if len(filters) > 0 {
		var clauses []string
		for col, val := range filters {
			clauses = append(clauses, fmt.Sprintf("%s = ?", col))
			args = append(args, val)
		}
		query += " WHERE " + strings.Join(clauses, " AND ")
	}

	rows, err := t.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []*T
	for rows.Next() {
		item, err := t.scanRows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// scanRow scans a single row into T.
func (t *Table[T]) scanRow(row *sql.Row) (*T, error) {
	var item T
	val := reflect.ValueOf(&item).Elem()
	dest := make([]interface{}, len(t.columns))

	for i, col := range t.columns {
		fieldIdx := t.fieldMap[col]
		dest[i] = val.Field(fieldIdx).Addr().Interface()
	}

	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	return &item, nil
}

// scanRows scans the current row from rows into T.
func (t *Table[T]) scanRows(rows *sql.Rows) (*T, error) {
	var item T
	val := reflect.ValueOf(&item).Elem()
	dest := make([]interface{}, len(t.columns))

	for i, col := range t.columns {
		fieldIdx := t.fieldMap[col]
		dest[i] = val.Field(fieldIdx).Addr().Interface()
	}

	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	return &item, nil
}

// withRetry runs fn, retrying it with cfg while it fails with a transaction
// conflict.
//
// When the table is bound to a caller's *sql.Tx, fn runs exactly once: DuckDB
// aborts a transaction on its first error, so further attempts inside the same
// transaction can only fail with "Current transaction is aborted". Callers that
// own the transaction must retry at the boundary where they begin it.
func (t *Table[T]) withRetry(ctx context.Context, cfg retry.Config, fn func() error) error {
	if _, inTx := t.db.(*sql.Tx); inTx {
		return fn()
	}
	return retry.Do(ctx, cfg, fn, IsTransactionConflict)
}

// IsTransactionConflict reports whether err is a transient DuckDB
// optimistic-concurrency failure that succeeds when retried in a new
// transaction.
//
// It matches TransactionContext errors that report a write-write conflict
// ("Conflict on update!", "Conflict on tuple deletion!", "Catalog write-write
// conflict ...") or a commit that lost a race to insert the same primary key.
// Other TransactionContext errors, such as "Current transaction is aborted",
// are permanent and are not matched, nor are errors from other sources.
func IsTransactionConflict(err error) bool {
	var dErr *duckdbDriver.Error
	if !errors.As(err, &dErr) || dErr.Type != duckdbDriver.ErrorTypeTransaction {
		return false
	}
	return strings.Contains(strings.ToLower(dErr.Msg), "conflict") ||
		strings.Contains(dErr.Msg, "PRIMARY KEY or UNIQUE constraint violated")
}
