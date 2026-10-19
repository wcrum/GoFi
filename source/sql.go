//go:build !gofi_nosql

package source

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/wcrum/gofi/spec"
)

// sqlDriverNames maps a source's driver to its database/sql driver name.
var sqlDriverNames = map[string]string{"postgres": "pgx", "mysql": "mysql", "sqlserver": "sqlserver"}

// runSQL runs a query every interval (pollLoop: no overlapping polls).
func runSQL(ctx context.Context, flow string, f *spec.FetchSource, emit Emit) error {
	p, err := newSQLPoller(f.SQL)
	if err != nil {
		return err
	}
	defer p.db.Close()
	every, _ := f.SQL.Timings()
	return pollLoop(ctx, flow, "sql", p.origin, every, func(ctx context.Context) error { return p.poll(ctx, emit) })
}

// sqlPoller holds a SQL fetch source's pool and in-memory cursor.
type sqlPoller struct {
	cfg     *spec.SQLFetch
	db      *sql.DB
	timeout time.Duration
	max     int
	origin  string
	cursor  any // last cursorColumn value handed off (or cursorStart)
}

func newSQLPoller(cfg *spec.SQLFetch) (*sqlPoller, error) {
	dsn, err := sqlDSN(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(sqlDriverNames[cfg.Driver], dsn)
	if err != nil {
		return nil, fmt.Errorf("source.fetch.sql: %w", err)
	}
	// One poll at a time needs one connection; keep it between polls.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	_, timeout := cfg.Timings()
	max := cfg.MaxRows
	if max <= 0 {
		max = spec.DefaultSQLMaxRows
	}
	p := &sqlPoller{cfg: cfg, db: db, timeout: timeout, max: max, origin: sqlOrigin(cfg)}
	if cfg.CursorColumn != "" {
		p.cursor = cursorStart(cfg.CursorStart)
	}
	return p, nil
}

// sqlOrigin names the source for live traffic without credentials:
// "sql:postgres://db:5432/orders".
func sqlOrigin(cfg *spec.SQLFetch) string {
	host, port := cfg.Address()
	origin := "sql:" + cfg.Driver + "://" + net.JoinHostPort(host, strconv.Itoa(port))
	if cfg.Database != "" {
		origin += "/" + cfg.Database
	}
	return origin
}

// cursorStart types the configured start value so drivers bind it like the
// column values that replace it: an integer, an RFC 3339 time, or text.
func cursorStart(value string) any {
	value = strings.TrimSpace(value)
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return n
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	return value
}

// sqlDSN builds the driver DSN from the connection fields, or adjusts a
// given DSN where GoFi needs a setting (MySQL parseTime).
func sqlDSN(cfg *spec.SQLFetch) (string, error) {
	host, port := cfg.Address()
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var user *url.Userinfo
	if cfg.Username != "" || cfg.Password != "" {
		user = url.UserPassword(cfg.Username, cfg.Password)
	}
	switch cfg.Driver {
	case "postgres":
		if cfg.DSN != "" {
			return cfg.DSN, nil
		}
		q := url.Values{}
		if cfg.TLS != "" {
			q.Set("sslmode", cfg.TLS) // disable | require | verify-full
		}
		for k, v := range cfg.Params {
			q.Set(k, v)
		}
		u := url.URL{Scheme: "postgres", User: user, Host: addr, Path: "/" + cfg.Database, RawQuery: q.Encode()}
		return u.String(), nil
	case "mysql":
		m := mysql.NewConfig()
		if cfg.DSN != "" {
			parsed, err := mysql.ParseDSN(cfg.DSN)
			if err != nil {
				return "", fmt.Errorf("source.fetch.sql.dsn: %w", err)
			}
			m = parsed
		} else {
			m.User, m.Passwd, m.Net, m.Addr, m.DBName = cfg.Username, cfg.Password, "tcp", addr, cfg.Database
			m.TLSConfig = map[string]string{"": "", "disable": "false", "require": "skip-verify", "verify-full": "true"}[cfg.TLS]
			if len(cfg.Params) > 0 {
				m.Params = map[string]string{}
				for k, v := range cfg.Params {
					m.Params[k] = v
				}
			}
		}
		m.ParseTime = true // DATETIME/TIMESTAMP as time.Time
		// Never let a DSN or params enable LOAD DATA LOCAL INFILE (a server
		// could then read any file this process can) or weak password methods.
		m.AllowAllFiles, m.AllowCleartextPasswords, m.AllowOldPasswords = false, false, false
		for key := range m.Params {
			switch strings.ToLower(key) {
			case "allowallfiles", "allowcleartextpasswords", "allowoldpasswords":
				delete(m.Params, key)
			}
		}
		return m.FormatDSN(), nil
	case "sqlserver":
		if cfg.DSN != "" {
			return cfg.DSN, nil
		}
		q := url.Values{}
		if cfg.Database != "" {
			q.Set("database", cfg.Database)
		}
		switch cfg.TLS {
		case "disable":
			q.Set("encrypt", "disable")
		case "require":
			q.Set("encrypt", "true")
			q.Set("TrustServerCertificate", "true")
		case "verify-full":
			q.Set("encrypt", "true")
			q.Set("TrustServerCertificate", "false")
		}
		for k, v := range cfg.Params {
			q.Set(k, v)
		}
		u := url.URL{Scheme: "sqlserver", User: user, Host: addr, RawQuery: q.Encode()}
		return u.String(), nil
	}
	return "", fmt.Errorf("source.fetch.sql.driver %q is not supported", cfg.Driver)
}

type sqlQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// poll runs the query once and hands off up to maxRows rows, then advances
// the cursor to the last row's cursorColumn value.
func (p *sqlPoller) poll(ctx context.Context, emit Emit) error {
	frames, last, err := p.query(ctx)
	if err != nil {
		return err
	}
	for _, frame := range frames {
		emit(ctx, frame, p.origin)
	}
	if p.cfg.CursorColumn != "" && len(frames) > 0 {
		p.cursor = last
	}
	return nil
}

func (p *sqlPoller) query(ctx context.Context) (frames [][]byte, cursor any, err error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	var q sqlQueryer = p.db
	if p.cfg.Driver != "sqlserver" { // go-mssqldb has no read-only transactions
		// Defense in depth only: a read-only database user is the boundary.
		tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, nil, err
		}
		defer tx.Rollback()
		q = tx
	}
	var args []any
	if p.cfg.CursorColumn != "" {
		args = []any{p.cursor}
	}
	rows, err := q.QueryContext(ctx, p.cfg.Query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		return nil, nil, err
	}
	cursorAt := -1
	for i, column := range columns {
		if p.cfg.CursorColumn != "" && strings.EqualFold(column.Name(), p.cfg.CursorColumn) {
			cursorAt = i
		}
	}
	if p.cfg.CursorColumn != "" && cursorAt < 0 {
		return nil, nil, fmt.Errorf("query result has no column %q (cursorColumn)", p.cfg.CursorColumn)
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	for len(frames) < p.max && rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return nil, nil, err
		}
		frame, err := rowJSON(columns, values)
		if err != nil {
			return nil, nil, err
		}
		if len(frame) > MaxMessageBytes {
			return nil, nil, fmt.Errorf("row exceeds %d bytes as JSON", MaxMessageBytes)
		}
		frames = append(frames, frame)
		if cursorAt >= 0 {
			if values[cursorAt] == nil {
				return nil, nil, fmt.Errorf("cursorColumn %q is NULL in a result row", p.cfg.CursorColumn)
			}
			cursor = values[cursorAt]
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return frames, cursor, nil
}

// rowJSON encodes one row as a JSON object with keys in column order.
func rowJSON(columns []*sql.ColumnType, values []any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, column := range columns {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(column.Name())
		buf.Write(key)
		buf.WriteByte(':')
		value, err := json.Marshal(jsonValue(strings.ToUpper(column.DatabaseTypeName()), values[i]))
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", column.Name(), err)
		}
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// jsonValue converts a scanned SQL value: times become RFC 3339 strings,
// numeric/decimal text stays an exact JSON number, JSON columns embed their
// document, binary data becomes base64, and other bytes become text.
func jsonValue(dbType string, value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return strconv.FormatFloat(v, 'g', -1, 64)
		}
		return v
	case float32:
		return jsonValue(dbType, float64(v))
	case string:
		return textValue(dbType, v)
	case []byte:
		if dbType == "UNIQUEIDENTIFIER" && len(v) == 16 {
			var id mssql.UniqueIdentifier
			if id.Scan(v) == nil {
				return id.String()
			}
		}
		if isBinaryType(dbType) || !utf8.Valid(v) {
			return base64.StdEncoding.EncodeToString(v)
		}
		return textValue(dbType, string(v))
	case int64, int32, int, bool, json.RawMessage:
		return v
	}
	if _, err := json.Marshal(value); err != nil {
		return fmt.Sprint(value)
	}
	return value
}

func textValue(dbType, text string) any {
	switch {
	case strings.Contains(dbType, "NUMERIC") || strings.Contains(dbType, "DECIMAL") || strings.Contains(dbType, "MONEY"):
		if json.Valid([]byte(text)) {
			var n json.Number = json.Number(strings.TrimSpace(text))
			if _, err := strconv.ParseFloat(string(n), 64); err == nil {
				return n
			}
		}
	case dbType == "JSON" || dbType == "JSONB":
		if json.Valid([]byte(text)) {
			return json.RawMessage(text)
		}
	}
	return text
}

func isBinaryType(dbType string) bool {
	for _, marker := range []string{"BLOB", "BINARY", "BYTEA", "IMAGE", "VARBINARY", "BIT VARYING", "GEOMETRY"} {
		if strings.Contains(dbType, marker) {
			return true
		}
	}
	return false
}
