package spec

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// S3Fetch lists a bucket prefix every interval. Each object not yet seen is
// one frame (or one frame per Split element). The cursor is in memory: a
// restarted runtime re-reads the prefix unless Since, DeleteAfter, or MoveTo
// keeps old objects out of it (docs/flows.md).
//
// Credentials are inline. When AccessKeyID is empty the runtime uses ambient
// credentials: AWS_*/MINIO_* environment variables, then IRSA web identity,
// then instance metadata.
type S3Fetch struct {
	Endpoint           string `json:"endpoint,omitempty"` // https://minio.example:9000; default AWS S3 for Region
	Region             string `json:"region,omitempty"`   // default us-east-1
	Bucket             string `json:"bucket"`
	Prefix             string `json:"prefix,omitempty"`
	Glob               string `json:"glob,omitempty"` // path.Match on the key after Prefix ("*.json")
	AccessKeyID        string `json:"accessKeyId,omitempty"`
	SecretAccessKey    string `json:"secretAccessKey,omitempty"`
	SessionToken       string `json:"sessionToken,omitempty"`
	PathStyle          bool   `json:"pathStyle,omitempty"` // MinIO/Ceph usually need path-style requests
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
	TLSCA              string `json:"tlsCa,omitempty"` // inline CA certificate, PEM
	Since              string `json:"since,omitempty"` // RFC 3339; skip objects modified at or before it
	MaxObjects         int    `json:"maxObjects,omitempty"`
	DeleteAfter        bool   `json:"deleteAfter,omitempty"` // delete each object after hand-off
	MoveTo             string `json:"moveTo,omitempty"`      // or copy it under this prefix, then delete
	Every              string `json:"every,omitempty"`       // default 30s
	Timeout            string `json:"timeout,omitempty"`     // per request; default 10s
}

// SQLFetch runs a read-only query every interval; each row becomes one JSON
// object frame. With CursorColumn set, the query binds the previous poll's
// last cursor value as its only parameter and must order by that column.
type SQLFetch struct {
	Driver       string            `json:"driver"` // postgres | mysql | sqlserver
	DSN          string            `json:"dsn,omitempty"`
	Host         string            `json:"host,omitempty"`
	Port         int               `json:"port,omitempty"`
	Database     string            `json:"database,omitempty"`
	Username     string            `json:"username,omitempty"`
	Password     string            `json:"password,omitempty"`
	TLS          string            `json:"tls,omitempty"` // "" (driver default) | disable | require | verify-full
	Params       map[string]string `json:"params,omitempty"`
	Query        string            `json:"query"`
	CursorColumn string            `json:"cursorColumn,omitempty"`
	CursorStart  string            `json:"cursorStart,omitempty"`
	MaxRows      int               `json:"maxRows,omitempty"`
	Every        string            `json:"every,omitempty"`
	Timeout      string            `json:"timeout,omitempty"`
}

// Defaults and caps for S3 and SQL fetch sources.
const (
	DefaultS3MaxObjects = 100
	MaxS3MaxObjects     = 10000
	DefaultSQLMaxRows   = 1000
	MaxSQLMaxRows       = 100000
)

// SQLDrivers are the supported SQL fetch drivers and their default ports.
var SQLDrivers = map[string]int{"postgres": 5432, "mysql": 3306, "sqlserver": 1433}

// sqlPlaceholders is the cursor parameter each driver's query must contain.
var sqlPlaceholders = map[string]string{"postgres": "$1", "mysql": "?", "sqlserver": "@p1"}

// SQLPlaceholder is the parameter marker driver uses for the cursor value.
func SQLPlaceholder(driver string) string { return sqlPlaceholders[driver] }

// Timings returns the poll interval and per-request timeout.
func (s *S3Fetch) Timings() (every, timeout time.Duration) {
	every, _ = durationOr(s.Every, DefaultFetchEvery)
	timeout, _ = durationOr(s.Timeout, DefaultFetchTimeout)
	return every, timeout
}

// Timings returns the poll interval and query timeout.
func (s *SQLFetch) Timings() (every, timeout time.Duration) {
	every, _ = durationOr(s.Every, DefaultFetchEvery)
	timeout, _ = durationOr(s.Timeout, DefaultFetchTimeout)
	return every, timeout
}

// SinceTime is Since parsed (zero when unset).
func (s *S3Fetch) SinceTime() time.Time {
	t, _ := time.Parse(time.RFC3339, strings.TrimSpace(s.Since))
	return t
}

// EndpointURL is the URL the runtime connects to: Endpoint, or the AWS S3
// regional endpoint (virtual-hosted style unless PathStyle).
func (s *S3Fetch) EndpointURL() string {
	if strings.TrimSpace(s.Endpoint) != "" {
		return strings.TrimSpace(s.Endpoint)
	}
	region := s.Region
	if region == "" {
		region = "us-east-1"
	}
	host := "s3." + region + ".amazonaws.com"
	if !s.PathStyle && !strings.Contains(s.Bucket, ".") {
		host = s.Bucket + "." + host
	}
	return "https://" + host
}

func checkTimings(field, every, timeout string) error {
	e, err := durationOr(every, DefaultFetchEvery)
	if err != nil || e < time.Second {
		return fmt.Errorf("%s.every must be a duration of at least 1s", field)
	}
	t, err := durationOr(timeout, DefaultFetchTimeout)
	if err != nil || t <= 0 || t > 5*time.Minute {
		return fmt.Errorf("%s.timeout must be a positive duration up to 5m", field)
	}
	return nil
}

// bucketName follows the S3 rules loosely (S3-compatible stores vary).
var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`)

func (f *FetchSource) validateS3() error {
	s := f.S3
	const field = "source.fetch.s3"
	if strings.TrimSpace(s.Endpoint) != "" {
		if err := pulledURL(field+".endpoint", s.Endpoint, "http", "https"); err != nil {
			return err
		}
		if u, _ := url.Parse(strings.TrimSpace(s.Endpoint)); u.Path != "" && u.Path != "/" {
			return fmt.Errorf("%s.endpoint must not have a path; set bucket and prefix instead", field)
		}
	}
	if !bucketName.MatchString(s.Bucket) {
		return fmt.Errorf("%s.bucket must be a bucket name (3-63 lowercase letters, digits, dots, or hyphens)", field)
	}
	if s.Glob != "" {
		if _, err := path.Match(s.Glob, ""); err != nil {
			return fmt.Errorf("%s.glob is not a valid pattern: %v", field, err)
		}
	}
	if (s.AccessKeyID == "") != (s.SecretAccessKey == "") {
		return fmt.Errorf("%s needs both accessKeyId and secretAccessKey, or neither for ambient credentials", field)
	}
	if s.SessionToken != "" && s.AccessKeyID == "" {
		return fmt.Errorf("%s.sessionToken requires accessKeyId and secretAccessKey", field)
	}
	if s.Since != "" {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(s.Since)); err != nil {
			return fmt.Errorf("%s.since must be an RFC 3339 time such as 2026-01-02T15:04:05Z", field)
		}
	}
	if s.MaxObjects < 0 || s.MaxObjects > MaxS3MaxObjects {
		return fmt.Errorf("%s.maxObjects must be between 1 and %d (0 means %d)", field, MaxS3MaxObjects, DefaultS3MaxObjects)
	}
	if s.DeleteAfter && s.MoveTo != "" {
		return fmt.Errorf("%s: set deleteAfter or moveTo, not both", field)
	}
	if s.MoveTo != "" && strings.HasPrefix(s.MoveTo, s.Prefix) {
		// Moved objects would be listed again under the watched prefix.
		return fmt.Errorf("%s.moveTo must not be inside prefix %q", field, s.Prefix)
	}
	if err := checkTimings(field, s.Every, s.Timeout); err != nil {
		return err
	}
	_, err := ParseSplit(f.Split)
	return err
}

// mutatingSQL catches obvious write statements. It is a typo guard, not a
// security boundary: use a read-only database user.
var mutatingSQL = regexp.MustCompile(`(?i)^\s*(insert|update|delete|merge|upsert|replace|create|alter|drop|truncate|grant|revoke|call|exec|execute|copy|load|lock|vacuum|set)\b`)

func (f *FetchSource) validateSQL() error {
	s := f.SQL
	const field = "source.fetch.sql"
	if _, ok := SQLDrivers[s.Driver]; !ok {
		return fmt.Errorf("%s.driver must be postgres, mysql, or sqlserver", field)
	}
	if s.DSN != "" && (s.Host != "" || s.Database != "" || s.Username != "" || s.Password != "" || s.Port != 0 || s.TLS != "" || len(s.Params) > 0) {
		return fmt.Errorf("%s: set dsn or host/port/database/username/password/tls/params, not both", field)
	}
	if s.DSN == "" && strings.TrimSpace(s.Host) == "" {
		return fmt.Errorf("%s needs dsn or host", field)
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("%s.port must be between 1 and 65535", field)
	}
	switch s.TLS {
	case "", "disable", "require", "verify-full":
	default:
		return fmt.Errorf("%s.tls must be disable, require, or verify-full", field)
	}
	query := strings.TrimSpace(s.Query)
	if query == "" {
		return fmt.Errorf("%s.query is required", field)
	}
	if mutatingSQL.MatchString(query) {
		return fmt.Errorf("%s.query must be a read-only query (SELECT or WITH)", field)
	}
	placeholder := SQLPlaceholder(s.Driver)
	if s.CursorColumn != "" {
		if !strings.Contains(query, placeholder) {
			return fmt.Errorf("%s.query must bind the cursor value with %s when cursorColumn is set", field, placeholder)
		}
		if strings.TrimSpace(s.CursorStart) == "" {
			return fmt.Errorf("%s.cursorStart is required with cursorColumn (for example 0 or 1970-01-01T00:00:00Z)", field)
		}
	} else if s.CursorStart != "" {
		return fmt.Errorf("%s.cursorStart requires cursorColumn", field)
	}
	if s.MaxRows < 0 || s.MaxRows > MaxSQLMaxRows {
		return fmt.Errorf("%s.maxRows must be between 1 and %d (0 means %d)", field, MaxSQLMaxRows, DefaultSQLMaxRows)
	}
	if f.Split != "" {
		return fmt.Errorf("source.fetch.split does not apply to sql (each row is already one frame)")
	}
	return checkTimings(field, s.Every, s.Timeout)
}

// Address is the database server's host and port, from the connection
// fields or a best-effort parse of DSN ("" when it cannot be found).
func (s *SQLFetch) Address() (host string, port int) {
	defaultPort := SQLDrivers[s.Driver]
	if s.DSN == "" {
		if s.Port != 0 {
			return s.Host, s.Port
		}
		return s.Host, defaultPort
	}
	dsn := strings.TrimSpace(s.DSN)
	hostPort := ""
	switch {
	case strings.Contains(dsn, "://"):
		if u, err := url.Parse(dsn); err == nil {
			hostPort = u.Host
		}
	case s.Driver == "mysql":
		if m := regexp.MustCompile(`@tcp\(([^)]*)\)`).FindStringSubmatch(dsn); m != nil {
			hostPort = m[1]
		}
	case s.Driver == "sqlserver":
		for _, part := range strings.Split(dsn, ";") {
			key, value, _ := strings.Cut(part, "=")
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "server", "data source", "address", "addr":
				hostPort = strings.Replace(strings.TrimSpace(value), ",", ":", 1)
				hostPort = strings.TrimPrefix(hostPort, "tcp:")
			}
		}
	default: // postgres keyword/value: host=db port=5432
		for _, field := range strings.Fields(dsn) {
			key, value, _ := strings.Cut(field, "=")
			switch key {
			case "host":
				host = strings.Trim(value, `'`)
			case "port":
				port, _ = strconv.Atoi(strings.Trim(value, `'`))
			}
		}
	}
	if hostPort != "" {
		h, p, err := net.SplitHostPort(hostPort)
		if err != nil {
			h = hostPort
		}
		if i := strings.Index(h, `\`); i >= 0 { // sqlserver named instance
			h = h[:i]
		}
		host, port = h, 0
		port, _ = strconv.Atoi(p)
	}
	if strings.Contains(host, ",") { // multi-host: report the first
		host, _, _ = strings.Cut(host, ",")
	}
	if port == 0 {
		port = defaultPort
	}
	return host, port
}

// EndpointURL is a URL naming the database server, for egress rules and
// connectivity checks ("" when DSN gives no host).
func (s *SQLFetch) EndpointURL() string {
	host, port := s.Address()
	if host == "" || strings.HasPrefix(host, "/") { // a Unix socket
		return ""
	}
	return s.Driver + "://" + net.JoinHostPort(host, strconv.Itoa(port))
}
