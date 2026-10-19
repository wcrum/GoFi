//go:build !gofi_nos3

package source

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/wcrum/gofi/spec"
)

// runS3 polls a bucket prefix (pollLoop: no overlapping polls).
func runS3(ctx context.Context, flow string, f *spec.FetchSource, emit Emit) error {
	p, err := newS3Poller(f)
	if err != nil {
		return err
	}
	every, _ := f.S3.Timings()
	return pollLoop(ctx, flow, "s3", "s3://"+f.S3.Bucket+"/"+f.S3.Prefix, every, func(ctx context.Context) error {
		return p.poll(ctx, flow, emit)
	})
}

// s3Poller holds an S3 fetch source's client and in-memory cursor.
//
// Without deleteAfter/moveTo the cursor is a watermark: objects are handed
// off in (LastModified, key) order, and an object is new when it sorts after
// the last one handed off. An object that appears later with an older
// LastModified (a slow multipart upload, a copy that preserves times) is
// missed; use deleteAfter or moveTo when that matters. With either, the
// prefix itself is the queue: every listed object is read, then removed.
type s3Poller struct {
	cfg     *spec.S3Fetch
	client  *minio.Client
	keys    []string // split path; nil when not splitting
	timeout time.Duration
	max     int
	since   time.Time
	consume bool // deleteAfter or moveTo

	mark   time.Time       // LastModified of the last object handed off
	atMark map[string]bool // keys handed off at exactly mark
	// skipped holds objects that cannot be handed off (too large, bad
	// split) at the LastModified they had, so consume mode does not retry
	// them every poll.
	skipped map[string]time.Time
}

func newS3Poller(f *spec.FetchSource) (*s3Poller, error) {
	cfg := f.S3
	keys, err := spec.ParseSplit(f.Split)
	if err != nil {
		return nil, err
	}
	client, err := newS3Client(cfg)
	if err != nil {
		return nil, err
	}
	_, timeout := cfg.Timings()
	max := cfg.MaxObjects
	if max <= 0 {
		max = spec.DefaultS3MaxObjects
	}
	p := &s3Poller{cfg: cfg, client: client, timeout: timeout, max: max, since: cfg.SinceTime(),
		consume: cfg.DeleteAfter || cfg.MoveTo != "", atMark: map[string]bool{}, skipped: map[string]time.Time{}}
	if f.Split != "" {
		p.keys = keys
	}
	p.mark = p.since
	return p, nil
}

func newS3Client(cfg *spec.S3Fetch) (*minio.Client, error) {
	host, secure := "s3.amazonaws.com", true
	if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("source.fetch.s3.endpoint must be an absolute URL")
		}
		host, secure = u.Host, strings.EqualFold(u.Scheme, "https")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.InsecureSkipVerify || cfg.TLSCA != "" {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify}
		if cfg.TLSCA != "" {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(cfg.TLSCA)) {
				return nil, fmt.Errorf("source.fetch.s3.tlsCa has no PEM certificate")
			}
			tlsConfig.RootCAs = pool
		}
		transport.TLSClientConfig = tlsConfig
	}
	var creds *credentials.Credentials
	if cfg.AccessKeyID != "" {
		creds = credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken)
	} else {
		// Ambient: environment, then IRSA web identity or instance/container
		// metadata. With none of these the requests are anonymous.
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{}, &credentials.EnvMinio{},
			&credentials.IAM{Client: &http.Client{Transport: transport}, Region: cfg.Region},
		})
	}
	region := cfg.Region
	if region == "" && cfg.Endpoint == "" {
		region = "us-east-1"
	}
	lookup := minio.BucketLookupAuto
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	return minio.New(host, &minio.Options{Creds: creds, Secure: secure, Region: region, BucketLookup: lookup, Transport: transport})
}

// candidates lists the objects to hand off this poll, oldest first.
func (p *s3Poller) candidates(ctx context.Context) ([]minio.ObjectInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	var out []minio.ObjectInfo
	for object := range p.client.ListObjects(ctx, p.cfg.Bucket, minio.ListObjectsOptions{Prefix: p.cfg.Prefix, Recursive: true}) {
		if object.Err != nil {
			return nil, fmt.Errorf("list s3://%s/%s: %w", p.cfg.Bucket, p.cfg.Prefix, object.Err)
		}
		if strings.HasSuffix(object.Key, "/") {
			continue // a "directory" marker
		}
		if p.cfg.Glob != "" {
			if ok, _ := path.Match(p.cfg.Glob, strings.TrimPrefix(object.Key, p.cfg.Prefix)); !ok {
				continue
			}
		}
		modified := object.LastModified
		if !p.since.IsZero() && !modified.After(p.since) {
			continue
		}
		if at, ok := p.skipped[object.Key]; ok && at.Equal(modified) {
			continue
		}
		if !p.consume && (modified.Before(p.mark) || (modified.Equal(p.mark) && p.atMark[object.Key])) {
			continue
		}
		out = append(out, object)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastModified.Equal(out[j].LastModified) {
			return out[i].LastModified.Before(out[j].LastModified)
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > p.max {
		out = out[:p.max] // the rest are read next poll
	}
	return out, nil
}

func (p *s3Poller) poll(ctx context.Context, flow string, emit Emit) error {
	objects, err := p.candidates(ctx)
	if err != nil {
		return err
	}
	var skipped []error
	for _, object := range objects {
		origin := "s3://" + p.cfg.Bucket + "/" + object.Key
		frames, err := p.read(ctx, object)
		var poison *s3PoisonError
		switch {
		case errors.As(err, &poison):
			// The object will never fit: record it and move on.
			slog.Warn("s3 object skipped", "flow", flow, "object", origin, "error", poison.err)
			skipped = append(skipped, fmt.Errorf("%s: %w", origin, poison.err))
			p.skipped[object.Key] = object.LastModified
			p.advance(object)
			continue
		case err != nil:
			return fmt.Errorf("%s: %w", origin, err) // retried next poll
		}
		for _, frame := range frames {
			emit(ctx, frame, origin)
		}
		p.advance(object)
		if err := p.finish(ctx, object.Key); err != nil {
			return fmt.Errorf("%s was handed off but not removed (it is read again next poll): %w", origin, err)
		}
	}
	return errors.Join(skipped...)
}

// s3PoisonError marks an object that retrying will not fix.
type s3PoisonError struct{ err error }

func (e *s3PoisonError) Error() string { return e.err.Error() }

// read fetches one object and splits it into frames.
func (p *s3Poller) read(ctx context.Context, object minio.ObjectInfo) ([][]byte, error) {
	if object.Size > MaxMessageBytes {
		return nil, &s3PoisonError{fmt.Errorf("object is %d bytes; the limit is %d", object.Size, MaxMessageBytes)}
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	reader, err := p.client.GetObject(ctx, p.cfg.Bucket, object.Key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, MaxMessageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxMessageBytes {
		return nil, &s3PoisonError{fmt.Errorf("object exceeds %d bytes", MaxMessageBytes)}
	}
	if p.keys == nil {
		return [][]byte{data}, nil
	}
	items, err := SplitJSON(data, p.keys)
	if err != nil {
		return nil, &s3PoisonError{err}
	}
	return items, nil
}

// advance moves the watermark past object.
func (p *s3Poller) advance(object minio.ObjectInfo) {
	if object.LastModified.After(p.mark) {
		p.mark = object.LastModified
		clear(p.atMark)
	}
	if object.LastModified.Equal(p.mark) {
		p.atMark[object.Key] = true
	}
}

// finish deletes or moves a handed-off object when configured.
func (p *s3Poller) finish(ctx context.Context, key string) error {
	if !p.consume {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if p.cfg.MoveTo != "" {
		dst := minio.CopyDestOptions{Bucket: p.cfg.Bucket, Object: p.cfg.MoveTo + strings.TrimPrefix(key, p.cfg.Prefix)}
		if _, err := p.client.CopyObject(ctx, dst, minio.CopySrcOptions{Bucket: p.cfg.Bucket, Object: key}); err != nil {
			return fmt.Errorf("copy to %s: %w", dst.Object, err)
		}
	}
	if err := p.client.RemoveObject(ctx, p.cfg.Bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	delete(p.skipped, key)
	return nil
}
