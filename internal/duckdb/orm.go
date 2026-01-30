package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

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
}

// NewTable creates a new Table[T] instance.
// T must be a struct with `duckdb` tags.
func NewTable[T any](db Execer, tableName string) *Table[T] {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic("Table generic type T must be a struct")
	}

	var columns []string
	var pkColumns []string
	immutableFields := make(map[string]bool)
	fieldMap := make(map[string]int)

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
	}
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
	if val.Kind() == reflect.Ptr {
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

	return retry.Do(ctx, cfg, func() error {
		_, err := t.db.ExecContext(ctx, query, values...)
		return err
	}, isTransactionConflict)
}

// Insert inserts a new item into the database.
// It generates a plain INSERT statement without ON CONFLICT handling.
// Use this when you know the item doesn't exist and want to fail on duplicates.
func (t *Table[T]) Insert(ctx context.Context, item *T) error {
	placeholders := make([]string, len(t.columns))
	values := make([]interface{}, len(t.columns))

	val := reflect.ValueOf(item)
	if val.Kind() == reflect.Ptr {
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

	return retry.Do(ctx, cfg, func() error {
		_, err := t.db.ExecContext(ctx, query, values...)
		return err
	}, isTransactionConflict)
}

// BatchUpsert inserts multiple items in a single transaction using prepared statements.
// This is critical for performance.
func (t *Table[T]) BatchUpsert(ctx context.Context, items []*T) error {
	if len(items) == 0 {
		return nil
	}

	// 1. Construct Query (Same as Upsert)
	placeholders := make([]string, len(t.columns))
	updates := make([]string, 0, len(t.columns))

	for i, col := range t.columns {
		placeholders[i] = "?"
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

	// 2. Transact
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

	// 3. Prepare Statement
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare stmt: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	// 4. Exec Loop
	for _, item := range items {
		values := make([]interface{}, len(t.columns))
		val := reflect.ValueOf(item)
		if val.Kind() == reflect.Ptr {
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

	// 5. Commit (only if we started the tx)
	if _, started := t.db.(*sql.DB); started {
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
	}

	return nil
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
	if val.Kind() == reflect.Ptr {
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

	return retry.Do(ctx, cfg, func() error {
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
	}, isTransactionConflict)
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

	return retry.Do(ctx, cfg, func() error {
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
	}, isTransactionConflict)
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

func isTransactionConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Detect various DuckDB transaction conflict patterns
	return strings.Contains(msg, "Conflict on update") ||
		strings.Contains(msg, "conflict") ||
		strings.Contains(msg, "transaction") ||
		strings.Contains(msg, "serialization") ||
		strings.Contains(msg, "TransactionContext Error") ||
		(strings.Contains(msg, "PRIMARY KEY") && strings.Contains(msg, "constraint violated"))
}
