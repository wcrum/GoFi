//go:build !gofi_nosql

package source

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/wcrum/gofi/spec"
)

// fakeDB is a database/sql driver serving one table, ordered by id, filtered
// by "id > $1" when the query binds a cursor.
type fakeDB struct {
	mu       sync.Mutex
	rows     [][]driver.Value // id, name, at, blob, amount, doc
	args     []any
	readOnly []bool
}

var fakeColumns = []struct{ name, dbType string }{{"id", "INT8"}, {"name", "TEXT"}, {"at", "TIMESTAMPTZ"}, {"blob", "BYTEA"}, {"amount", "NUMERIC"}, {"doc", "JSONB"}}

var fakeDBs sync.Map // dsn -> *fakeDB

func init() { sql.Register("gofi-fake", fakeDriver{}) }

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) {
	db, ok := fakeDBs.Load(dsn)
	if !ok {
		return nil, errors.New("unknown fake database")
	}
	return &fakeConn{db: db.(*fakeDB)}, nil
}

type fakeConn struct{ db *fakeDB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }
func (c *fakeConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.db.mu.Lock()
	c.db.readOnly = append(c.db.readOnly, opts.ReadOnly)
	c.db.mu.Unlock()
	return fakeTx{}, nil
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.db.mu.Lock()
	defer c.db.mu.Unlock()
	var after int64 = -1 << 63
	if len(args) > 0 {
		c.db.args = append(c.db.args, args[0].Value)
		after = args[0].Value.(int64)
	}
	if strings.Contains(query, "boom") {
		return nil, errors.New("syntax error")
	}
	out := &fakeRows{}
	for _, row := range c.db.rows {
		if row[0].(int64) > after {
			out.rows = append(out.rows, row)
		}
	}
	return out, nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeRows struct {
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string {
	names := make([]string, len(fakeColumns))
	for i, c := range fakeColumns {
		names[i] = c.name
	}
	return names
}
func (r *fakeRows) ColumnTypeDatabaseTypeName(i int) string { return fakeColumns[i].dbType }
func (r *fakeRows) Close() error                            { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

func withFakeSQL(t *testing.T, rows [][]driver.Value) (*fakeDB, *spec.SQLFetch) {
	t.Helper()
	db := &fakeDB{rows: rows}
	fakeDBs.Store(t.Name(), db)
	previous := sqlDriverNames["postgres"]
	sqlDriverNames["postgres"] = "gofi-fake"
	t.Cleanup(func() { sqlDriverNames["postgres"] = previous })
	return db, &spec.SQLFetch{Driver: "postgres", DSN: t.Name(), Query: "SELECT * FROM t WHERE id > $1 ORDER BY id", CursorColumn: "id", CursorStart: "0"}
}

func TestSQLIncrementalRowsAsJSON(t *testing.T) {
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	db, cfg := withFakeSQL(t, [][]driver.Value{
		{int64(1), "a", at, []byte{0xff, 0x00}, []byte("12.50"), []byte(`{"k":[1]}`)},
		{int64(2), "b", nil, nil, "7", "not json"},
		{int64(3), "c", at, []byte("ok"), []byte("x"), nil},
	})
	cfg.MaxRows = 2
	if err := (spec.FlowSource{Fetch: &spec.FetchSource{SQL: cfg}}).Validate(); err != nil {
		t.Fatal(err)
	}
	p, err := newSQLPoller(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.db.Close()
	var got origins
	if err := p.poll(context.Background(), got.emit); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"id":1,"name":"a","at":"2026-02-03T04:05:06Z","blob":"/wA=","amount":12.50,"doc":{"k":[1]}}`,
		`{"id":2,"name":"b","at":null,"blob":null,"amount":7,"doc":"not json"}`,
	}
	if strings.Join(got.frames, "\n") != strings.Join(want, "\n") {
		t.Fatalf("frames:\n%s", strings.Join(got.frames, "\n"))
	}
	if !strings.HasPrefix(got.from[0], "sql:postgres://") {
		t.Fatalf("origin %s", got.from[0])
	}
	got = origins{}
	if err := p.poll(context.Background(), got.emit); err != nil || len(got.frames) != 1 || !strings.HasPrefix(got.frames[0], `{"id":3,`) {
		t.Fatalf("second poll %q %v", got.frames, err)
	}
	got = origins{}
	if err := p.poll(context.Background(), got.emit); err != nil || len(got.frames) != 0 {
		t.Fatalf("third poll %q %v", got.frames, err)
	}
	if len(db.args) != 3 || db.args[0] != int64(0) || db.args[1] != int64(2) || db.args[2] != int64(3) {
		t.Fatalf("cursor args %v", db.args)
	}
	for _, ro := range db.readOnly {
		if !ro {
			t.Fatal("postgres polls must run in a read-only transaction")
		}
	}
}

func TestSQLMissingCursorColumnAndQueryErrors(t *testing.T) {
	_, cfg := withFakeSQL(t, [][]driver.Value{{int64(1), "a", nil, nil, nil, nil}})
	cfg.CursorColumn = "updated_at"
	p, err := newSQLPoller(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.db.Close()
	var got origins
	if err := p.poll(context.Background(), got.emit); err == nil || !strings.Contains(err.Error(), "updated_at") || len(got.frames) != 0 {
		t.Fatalf("want a missing cursor column error, got %v %q", err, got.frames)
	}
	cfg.CursorColumn, cfg.Query = "id", "SELECT boom $1"
	if err := p.poll(context.Background(), got.emit); err == nil || p.cursor != int64(0) {
		t.Fatalf("a failed query must not advance the cursor: %v %v", err, p.cursor)
	}
}

func TestSQLValidationDSNAndAddress(t *testing.T) {
	ok := func(edit func(*spec.SQLFetch)) error {
		s := &spec.SQLFetch{Driver: "mysql", Host: "db.example", Query: "SELECT id FROM t WHERE id > ? ORDER BY id", CursorColumn: "id", CursorStart: "0"}
		edit(s)
		return (spec.FlowSource{Fetch: &spec.FetchSource{SQL: s}}).Validate()
	}
	for _, tc := range []struct {
		edit func(*spec.SQLFetch)
		want string
	}{
		{func(s *spec.SQLFetch) { s.Driver = "sqlite" }, "driver"},
		{func(s *spec.SQLFetch) { s.Query = "  delete from t" }, "read-only"},
		{func(s *spec.SQLFetch) { s.Query = "SELECT id FROM t" }, "?"},
		{func(s *spec.SQLFetch) { s.CursorStart = "" }, "cursorStart"},
		{func(s *spec.SQLFetch) { s.DSN = "u:p@tcp(db:3306)/x" }, "not both"},
		{func(s *spec.SQLFetch) { s.Host = "" }, "dsn or host"},
		{func(s *spec.SQLFetch) { s.TLS = "maybe" }, "tls"},
		{func(s *spec.SQLFetch) { s.Timeout = "10m" }, "timeout"},
	} {
		if err := ok(tc.edit); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want error containing %q, got %v", tc.want, err)
		}
	}
	if err := ok(func(*spec.SQLFetch) {}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		cfg  spec.SQLFetch
		want string
	}{
		{spec.SQLFetch{Driver: "postgres", Host: "pg.db.svc.cluster.local", Database: "orders", Username: "ro", Password: "p@ss", TLS: "require"}, "postgres://ro:p%40ss@pg.db.svc.cluster.local:5432/orders?sslmode=require"},
		{spec.SQLFetch{Driver: "mysql", Host: "my", Port: 3307, Database: "d", Username: "u", Password: "p", TLS: "verify-full"}, "u:p@tcp(my:3307)/d?parseTime=true&tls=true"},
		{spec.SQLFetch{Driver: "mysql", DSN: "u:p@tcp(my:3306)/d"}, "u:p@tcp(my:3306)/d?parseTime=true"},
		{spec.SQLFetch{Driver: "sqlserver", Host: "ms", Database: "d", Username: "sa", Password: "x", TLS: "disable"}, "sqlserver://sa:x@ms:1433?database=d&encrypt=disable"},
	} {
		if got, err := sqlDSN(&tc.cfg); err != nil || got != tc.want {
			t.Errorf("dsn %s, want %s (%v)", got, tc.want, err)
		}
	}

	for _, tc := range []struct {
		cfg  spec.SQLFetch
		want string
	}{
		{spec.SQLFetch{Driver: "postgres", DSN: "postgres://u:p@10.0.0.5:6543/db"}, "postgres://10.0.0.5:6543"},
		{spec.SQLFetch{Driver: "postgres", DSN: "host=pg.example port=5433 user=u"}, "postgres://pg.example:5433"},
		{spec.SQLFetch{Driver: "mysql", DSN: "u:p@tcp(my.example)/d"}, "mysql://my.example:3306"},
		{spec.SQLFetch{Driver: "sqlserver", DSN: "server=ms.example,1444;user id=sa"}, "sqlserver://ms.example:1444"},
		{spec.SQLFetch{Driver: "postgres", DSN: "host=/var/run/postgresql"}, ""},
	} {
		if got := tc.cfg.EndpointURL(); got != tc.want {
			t.Errorf("%s: endpoint %q, want %q", tc.cfg.DSN, got, tc.want)
		}
	}
}

func TestSQLDSNMySQLRefusesUnsafeOptions(t *testing.T) {
	for _, cfg := range []*spec.SQLFetch{
		{Driver: "mysql", DSN: "u:p@tcp(db:3306)/app?allowAllFiles=true&allowCleartextPasswords=true&allowOldPasswords=true"},
		{Driver: "mysql", Host: "db", Username: "u", Password: "p", Params: map[string]string{"allowAllFiles": "true", "allowCleartextPasswords": "true", "allowOldPasswords": "true"}},
	} {
		dsn, err := sqlDSN(cfg)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.AllowAllFiles || parsed.AllowCleartextPasswords || parsed.AllowOldPasswords {
			t.Errorf("sqlDSN(%+v) = %q enables an unsafe option", cfg, dsn)
		}
	}
}
