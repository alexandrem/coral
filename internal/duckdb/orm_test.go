package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ormTestRow struct {
	ID      string    `duckdb:"id,pk"`
	Bucket  time.Time `duckdb:"bucket,pk"`
	Created string    `duckdb:"created,immutable"`
	Seen    time.Time `duckdb:"seen"`
	Count   int32     `duckdb:"count"`
	Big     uint32    `duckdb:"big"`
	Ratio   float64   `duckdb:"ratio"`
	Flag    bool      `duckdb:"flag"`
	Note    *string   `duckdb:"note"`
	Opt     *int64    `duckdb:"opt"`
	Frames  []int64   `duckdb:"frames,immutable"` // DuckDB cannot DO UPDATE list columns.
	Day     string    `duckdb:"day"`
	Ignored string
}

const ormTestSchema = `CREATE TABLE rows (
	id TEXT NOT NULL,
	bucket TIMESTAMPTZ NOT NULL,
	created TEXT,
	seen TIMESTAMP,
	count INTEGER,
	big BIGINT,
	ratio DOUBLE,
	flag BOOLEAN,
	note TEXT,
	opt BIGINT,
	frames BIGINT[],
	day DATE,
	PRIMARY KEY (id, bucket)
)`

// ormTestKeyOnly has only PK and immutable columns, so upserts use DO NOTHING.
type ormTestKeyOnly struct {
	ID    string `duckdb:"id,pk"`
	Label string `duckdb:"label,immutable"`
}

const ormTestKeyOnlySchema = `CREATE TABLE key_only (id TEXT PRIMARY KEY, label TEXT)`

// ormTestNoPK has no primary key, so every row is inserted.
type ormTestNoPK struct {
	Name  string `duckdb:"name"`
	Value int    `duckdb:"value"`
}

const ormTestNoPKSchema = `CREATE TABLE no_pk (name TEXT, value BIGINT)`

func openORMTestDB(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, err := OpenDB("")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(schema)
	require.NoError(t, err)
	return db
}

// dumpTable returns the table contents as sorted, comparable strings.
func dumpTable(t *testing.T, db *sql.DB, table, orderBy string) []string {
	t.Helper()
	// #nosec G201 - test-only identifiers.
	rows, err := db.Query(fmt.Sprintf("SELECT * FROM %s ORDER BY %s", table, orderBy))
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	require.NoError(t, err)

	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		parts := make([]string, len(vals))
		for i, v := range vals {
			if ts, ok := v.(time.Time); ok {
				v = ts.UTC().Format(time.RFC3339Nano)
			}
			parts[i] = fmt.Sprintf("%s=%v", cols[i], v)
		}
		out = append(out, strings.Join(parts, " "))
	}
	require.NoError(t, rows.Err())
	return out
}

// upsertBothPaths applies seed and batch through the staged appender path and
// through the prepared-statement path, and returns both table dumps.
func upsertBothPaths[T any](t *testing.T, schema, table, orderBy string, seed, batch []*T) (staged, prepared []string) {
	t.Helper()
	ctx := context.Background()

	stagedDB := openORMTestDB(t, schema)
	stagedTable := NewTable[T](stagedDB, table)
	require.NotNil(t, stagedTable.stageExprs, "type must be supported by the staged path")
	if len(seed) > 0 {
		require.NoError(t, stagedTable.BatchUpsert(ctx, seed))
	}
	require.NoError(t, stagedTable.BatchUpsert(ctx, batch))

	preparedDB := openORMTestDB(t, schema)
	for _, items := range [][]*T{seed, batch} {
		if len(items) == 0 {
			continue
		}
		tx, err := preparedDB.Begin()
		require.NoError(t, err)
		require.NoError(t, NewTable[T](tx, table).BatchUpsert(ctx, items))
		require.NoError(t, tx.Commit())
	}

	return dumpTable(t, stagedDB, table, orderBy), dumpTable(t, preparedDB, table, orderBy)
}

func TestBatchUpsert_StagedMatchesPrepared(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	b1 := time.Date(2026, 3, 1, 10, 0, 0, 123456000, est)
	b2 := b1.Add(time.Minute)
	note := "hello"
	opt := int64(-42)

	seed := []*ormTestRow{
		{ID: "a", Bucket: b1, Created: "seed", Seen: b1, Count: 1, Day: "2026-01-01"},
	}
	batch := []*ormTestRow{
		// Conflicts with the seeded row: mutable columns update, created stays "seed".
		{ID: "a", Bucket: b1, Created: "ignored", Seen: b2, Count: 2, Big: 4_000_000_000, Ratio: 0.5,
			Flag: true, Note: &note, Opt: &opt, Frames: []int64{3, 2, 1}, Day: "2026-02-03"},
		// Same key twice in one batch: last mutable values win, first immutable value wins.
		{ID: "b", Bucket: b2, Created: "first", Count: 10, Day: "2026-01-01"},
		{ID: "c", Bucket: b2, Created: "only", Frames: []int64{3, 2, 1}, Day: "2026-01-01"},
		{ID: "b", Bucket: b2, Created: "second", Count: 20, Flag: true, Day: "2026-01-02"},
		{ID: "b", Bucket: b2, Created: "third", Count: 30, Note: &note, Day: "2026-01-03"},
		// Same id, different bucket: a distinct key.
		{ID: "b", Bucket: b1, Created: "other", Count: 5, Day: "2026-01-01"},
	}

	staged, prepared := upsertBothPaths(t, ormTestSchema, "rows", "id, bucket", seed, batch)
	assert.Equal(t, prepared, staged)

	// Sanity-check the semantics the paths agree on.
	require.Len(t, staged, 4)
	assert.Contains(t, staged[0], "created=seed")
	assert.Contains(t, staged[0], "count=2")
	assert.Contains(t, staged[0], "big=4000000000")
	assert.Contains(t, staged[1], "frames=[]")
	assert.Contains(t, staged[0], "opt=-42")
	assert.Contains(t, staged[2], "created=first")
	assert.Contains(t, staged[2], "count=30")
	assert.Contains(t, staged[2], "flag=false")
	assert.Contains(t, staged[2], "note=hello")
	assert.Contains(t, staged[1], "note=<nil>")
}

func TestBatchUpsert_KeyOnlyDoNothing(t *testing.T) {
	seed := []*ormTestKeyOnly{{ID: "x", Label: "seed"}}
	batch := []*ormTestKeyOnly{
		{ID: "x", Label: "ignored"},
		{ID: "y", Label: "first"},
		{ID: "y", Label: "second"},
	}

	staged, prepared := upsertBothPaths(t, ormTestKeyOnlySchema, "key_only", "id", seed, batch)
	assert.Equal(t, prepared, staged)
	assert.Equal(t, []string{"id=x label=seed", "id=y label=first"}, staged)
}

func TestBatchUpsert_NoPrimaryKey(t *testing.T) {
	batch := []*ormTestNoPK{{Name: "a", Value: 1}, {Name: "a", Value: 1}, {Name: "b", Value: 2}}

	staged, prepared := upsertBothPaths(t, ormTestNoPKSchema, "no_pk", "name, value", nil, batch)
	assert.Equal(t, prepared, staged)
	assert.Len(t, staged, 3)
}

func TestBatchUpsert_LargeBatch(t *testing.T) {
	ctx := context.Background()
	db := openORMTestDB(t, ormTestSchema)
	table := NewTable[ormTestRow](db, "rows")

	// Spans several appender data chunks (2048 rows each).
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	items := make([]*ormTestRow, 5000)
	for i := range items {
		items[i] = &ormTestRow{ID: fmt.Sprintf("id-%d", i%7), Bucket: base.Add(time.Duration(i) * time.Second),
			Count: int32(i), Day: "2026-01-01"}
	}
	require.NoError(t, table.BatchUpsert(ctx, items))
	// Re-upserting the same keys updates in place.
	require.NoError(t, table.BatchUpsert(ctx, items))

	var count, sum int64
	require.NoError(t, db.QueryRow("SELECT count(*), sum(count) FROM rows").Scan(&count, &sum))
	assert.Equal(t, int64(5000), count)
	assert.Equal(t, int64(4999*5000/2), sum)

	// The staging table is dropped after use.
	var staging int
	require.NoError(t, db.QueryRow(
		"SELECT count(*) FROM duckdb_tables() WHERE table_name LIKE 'coral_stage_%'").Scan(&staging))
	assert.Zero(t, staging)
}

func TestBatchUpsert_UnsupportedFieldFallsBack(t *testing.T) {
	type row struct {
		ID   string         `duckdb:"id,pk"`
		Name sql.NullString `duckdb:"name"`
	}
	ctx := context.Background()
	db := openORMTestDB(t, `CREATE TABLE nullable (id TEXT PRIMARY KEY, name TEXT)`)
	table := NewTable[row](db, "nullable")
	require.Nil(t, table.stageExprs)

	require.NoError(t, table.BatchUpsert(ctx, []*row{
		{ID: "a", Name: sql.NullString{String: "x", Valid: true}},
		{ID: "b"},
	}))
	assert.Equal(t, []string{"id=a name=x", "id=b name=<nil>"}, dumpTable(t, db, "nullable", "id"))
}

// ormTestCounter is a minimal upsertable row used by the conflict tests.
type ormTestCounter struct {
	ID    string `duckdb:"id,pk"`
	Value int64  `duckdb:"value"`
}

const ormTestCounterSchema = `CREATE TABLE counters (id TEXT PRIMARY KEY, value BIGINT)`

// openConcurrentTestDB opens a database that allows several connections, so
// that concurrent transactions can conflict.
func openConcurrentTestDB(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db := openORMTestDB(t, schema)
	db.SetMaxOpenConns(4)
	return db
}

// lockRow inserts counters row "a" and leaves an uncommitted update of it in
// an open transaction, so that any other transaction writing the row
// fails with a write-write conflict.
func lockRow(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO counters VALUES ('a', 0)`)
	require.NoError(t, err)

	winner, err := db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = winner.Rollback() })
	_, err = winner.Exec(`UPDATE counters SET value = 1 WHERE id = 'a'`)
	require.NoError(t, err)
}

// beginTx starts a transaction that is rolled back when the test ends.
func beginTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestIsTransactionConflict(t *testing.T) {
	db := openConcurrentTestDB(t, ormTestCounterSchema)
	lockRow(t, db)
	loser := beginTx(t, db)
	_, conflictErr := loser.Exec(`UPDATE counters SET value = 2 WHERE id = 'a'`)
	require.ErrorContains(t, conflictErr, "Conflict on update")

	_, abortedErr := loser.Exec(`SELECT 1`)
	require.ErrorContains(t, abortedErr, "Current transaction is aborted")

	_, dupErr := db.Exec(`INSERT INTO counters VALUES ('a', 9)`)
	require.ErrorContains(t, dupErr, "Duplicate key")

	// Two transactions inserting the same key: the second commit loses.
	tx1 := beginTx(t, db)
	tx2 := beginTx(t, db)
	_, err := tx1.Exec(`INSERT INTO counters VALUES ('b', 1)`)
	require.NoError(t, err)
	_, err = tx2.Exec(`INSERT INTO counters VALUES ('b', 2)`)
	require.NoError(t, err)
	require.NoError(t, tx1.Commit())
	commitRaceErr := tx2.Commit()
	require.ErrorContains(t, commitRaceErr, "PRIMARY KEY or UNIQUE constraint violated")

	_, syntaxErr := db.Exec(`SELEC 1`)
	require.Error(t, syntaxErr)

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"update conflict", conflictErr, true},
		{"wrapped update conflict", fmt.Errorf("batch exec: %w", conflictErr), true},
		{"commit lost primary key race", commitRaceErr, true},
		{"aborted transaction", abortedErr, false},
		{"duplicate key", dupErr, false},
		{"syntax error", syntaxErr, false},
		{"non-duckdb error mentioning conflict", errors.New("transaction conflict on serialization"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsTransactionConflict(tt.err), "err: %v", tt.err)
		})
	}
}

func TestBatchUpsert_ExternalTxDoesNotRetry(t *testing.T) {
	db := openConcurrentTestDB(t, ormTestCounterSchema)
	lockRow(t, db)
	tx := beginTx(t, db)

	start := time.Now()
	err := NewTable[ormTestCounter](tx, "counters").BatchUpsert(context.Background(),
		[]*ormTestCounter{{ID: "a", Value: 2}})
	require.Error(t, err)
	assert.True(t, IsTransactionConflict(err), "the original conflict must be returned: %v", err)
	assert.NotContains(t, err.Error(), "failed after", "an aborted transaction must not be retried")
	assert.Less(t, time.Since(start), time.Second)
}

func TestUpsert_ExternalTxDoesNotRetry(t *testing.T) {
	db := openConcurrentTestDB(t, ormTestCounterSchema)
	lockRow(t, db)
	tx := beginTx(t, db)

	err := NewTable[ormTestCounter](tx, "counters").Upsert(context.Background(), &ormTestCounter{ID: "a", Value: 2})
	require.Error(t, err)
	assert.True(t, IsTransactionConflict(err), "the original conflict must be returned: %v", err)
	assert.NotContains(t, err.Error(), "failed after", "an aborted transaction must not be retried")
}

func TestUpsert_RetriesConcurrentConflicts(t *testing.T) {
	db := openConcurrentTestDB(t, ormTestCounterSchema)
	table := NewTable[ormTestCounter](db, "counters")
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 4*50)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				errs <- table.Upsert(ctx, &ormTestCounter{ID: fmt.Sprintf("k%d", i%3), Value: int64(g)})
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func BenchmarkBatchUpsert(b *testing.B) {
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, n := range []int{100, 1000} {
		items := make([]*ormTestRow, n)
		for i := range items {
			items[i] = &ormTestRow{ID: fmt.Sprintf("svc-%d", i%50), Bucket: base.Add(time.Duration(i) * time.Minute),
				Count: int32(i), Ratio: 1.5, Day: "2026-01-01"}
		}
		for _, path := range []string{"staged", "prepared"} {
			b.Run(fmt.Sprintf("%s/%d", path, n), func(b *testing.B) {
				db, err := OpenDB("")
				require.NoError(b, err)
				defer func() { _ = db.Close() }()
				_, err = db.Exec(ormTestSchema)
				require.NoError(b, err)

				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if path == "staged" {
						require.NoError(b, NewTable[ormTestRow](db, "rows").BatchUpsert(ctx, items))
						continue
					}
					tx, err := db.Begin()
					require.NoError(b, err)
					require.NoError(b, NewTable[ormTestRow](tx, "rows").BatchUpsert(ctx, items))
					require.NoError(b, tx.Commit())
				}
			})
		}
	}
}
