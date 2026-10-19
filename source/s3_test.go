//go:build !gofi_nos3

package source

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wcrum/gofi/spec"
)

// fakeS3 is an in-memory, path-style S3 endpoint with the four calls the
// S3 source makes: ListObjectsV2, GetObject, CopyObject, and DeleteObject.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string]fakeObject // "bucket/key"
	size    map[string]int64      // optional listed-size overrides
}

type fakeObject struct {
	body     string
	modified time.Time
}

func newFakeS3(t *testing.T) (*fakeS3, *httptest.Server) {
	f := &fakeS3{objects: map[string]fakeObject{}, size: map[string]int64{}}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakeS3) put(key, body string, modified time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects["bkt/"+key] = fakeObject{body, modified}
}

func (f *fakeS3) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.objects {
		out = append(out, strings.TrimPrefix(k, "bkt/"))
	}
	sort.Strings(out)
	return out
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
		http.Error(w, "unsigned", http.StatusForbidden)
		return
	}
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && key == "" && r.URL.Query().Get("list-type") == "2":
		type content struct {
			Key          string
			LastModified string
			Size         int64
			ETag         string
		}
		var result struct {
			XMLName     xml.Name `xml:"ListBucketResult"`
			Name        string
			IsTruncated bool
			Contents    []content
		}
		result.Name = bucket
		prefix := r.URL.Query().Get("prefix")
		for full, object := range f.objects {
			k := strings.TrimPrefix(full, bucket+"/")
			if strings.HasPrefix(k, prefix) {
				size, ok := f.size[k]
				if !ok {
					size = int64(len(object.body))
				}
				result.Contents = append(result.Contents, content{k, object.modified.UTC().Format(time.RFC3339Nano), size, `"e"`})
			}
		}
		sort.Slice(result.Contents, func(i, j int) bool { return result.Contents[i].Key < result.Contents[j].Key })
		w.Header().Set("Content-Type", "application/xml")
		xml.NewEncoder(w).Encode(result)
	case r.Method == http.MethodGet:
		object, ok := f.objects[bucket+"/"+key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("Last-Modified", object.modified.UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", `"e"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(object.body)))
		io.WriteString(w, object.body)
	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		src, _ := url.PathUnescape(r.Header.Get("X-Amz-Copy-Source"))
		object, ok := f.objects[strings.TrimPrefix(src, "/")]
		if !ok {
			http.Error(w, "no source", http.StatusNotFound)
			return
		}
		f.objects[bucket+"/"+key] = object
		fmt.Fprintf(w, `<CopyObjectResult><ETag>"e"</ETag><LastModified>%s</LastModified></CopyObjectResult>`, time.Now().UTC().Format(time.RFC3339))
	case r.Method == http.MethodDelete:
		delete(f.objects, bucket+"/"+key)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unsupported", http.StatusNotImplemented)
	}
}

func s3Source(endpoint string, edit func(*spec.S3Fetch)) *spec.FetchSource {
	s := &spec.S3Fetch{Endpoint: endpoint, Region: "us-east-1", Bucket: "bkt", Prefix: "in/", PathStyle: true, AccessKeyID: "AK", SecretAccessKey: "SK"}
	if edit != nil {
		edit(s)
	}
	return &spec.FetchSource{S3: s}
}

type origins struct {
	frames, from []string
}

func (o *origins) emit(_ context.Context, data []byte, remote string) {
	o.frames = append(o.frames, string(data))
	o.from = append(o.from, remote)
}

func TestS3WatermarkReadsNewObjectsInOrderOnce(t *testing.T) {
	store, server := newFakeS3(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.put("in/b.json", "B", t0.Add(2*time.Second))
	store.put("in/a.json", "A", t0.Add(time.Second))
	store.put("in/skip.txt", "glob mismatch", t0)
	store.put("in/dir/", "", t0)
	store.put("other/x.json", "outside prefix", t0)
	store.put("in/big.json", "too big", t0)
	store.size["in/big.json"] = MaxMessageBytes + 1
	f := s3Source(server.URL, func(s *spec.S3Fetch) { s.Glob = "*.json" })
	if err := (spec.FlowSource{Fetch: f}).Validate(); err != nil {
		t.Fatal(err)
	}
	p, err := newS3Poller(f)
	if err != nil {
		t.Fatal(err)
	}
	var got origins
	err = p.poll(context.Background(), "f", got.emit)
	if err == nil || !strings.Contains(err.Error(), "s3://bkt/in/big.json") {
		t.Fatalf("expected the oversized object to be reported, got %v", err)
	}
	if strings.Join(got.frames, ",") != "A,B" || got.from[0] != "s3://bkt/in/a.json" {
		t.Fatalf("first poll %q from %q", got.frames, got.from)
	}
	// Same LastModified as b (a tie) and a later one; nothing re-read.
	store.put("in/c.json", "C", t0.Add(2*time.Second))
	store.put("in/d.json", "D", t0.Add(3*time.Second))
	got = origins{}
	if err := p.poll(context.Background(), "f", got.emit); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.frames, ",") != "C,D" {
		t.Fatalf("second poll %q", got.frames)
	}
	got = origins{}
	if err := p.poll(context.Background(), "f", got.emit); err != nil || len(got.frames) != 0 {
		t.Fatalf("third poll %q %v", got.frames, err)
	}
	if len(store.keys()) != 8 {
		t.Fatalf("watermark mode must not remove objects: %v", store.keys())
	}
}

func TestS3MoveToSplitsAndConsumesObjects(t *testing.T) {
	store, server := newFakeS3(t)
	now := time.Now()
	store.put("in/old.json", `[{"n":0}]`, now.Add(-2*time.Hour))
	store.put("in/1.json", `[{"n":1},{"n":2}]`, now)
	store.put("in/2.json", `not json`, now.Add(time.Second))
	f := s3Source(server.URL, func(s *spec.S3Fetch) {
		s.MoveTo, s.Since, s.MaxObjects = "done/", now.Add(-time.Hour).Format(time.RFC3339), 5
	})
	f.Split = "$[*]"
	if err := (spec.FlowSource{Fetch: f}).Validate(); err != nil {
		t.Fatal(err)
	}
	p, err := newS3Poller(f)
	if err != nil {
		t.Fatal(err)
	}
	var got origins
	err = p.poll(context.Background(), "f", got.emit)
	if err == nil || !strings.Contains(err.Error(), "in/2.json") {
		t.Fatalf("expected a split error for in/2.json, got %v", err)
	}
	if strings.Join(got.frames, ",") != `{"n":1},{"n":2}` {
		t.Fatalf("frames %q", got.frames)
	}
	if keys := strings.Join(store.keys(), ","); keys != "done/1.json,in/2.json,in/old.json" {
		t.Fatalf("objects after move: %s", keys)
	}
	got = origins{}
	if err := p.poll(context.Background(), "f", got.emit); err != nil || len(got.frames) != 0 {
		t.Fatalf("a skipped object must not be retried every poll: %q %v", got.frames, err)
	}
}

func TestS3ValidationAndEndpoints(t *testing.T) {
	for _, tc := range []struct {
		edit func(*spec.S3Fetch)
		want string
	}{
		{func(s *spec.S3Fetch) { s.Bucket = "No_Caps" }, "bucket"},
		{func(s *spec.S3Fetch) { s.SecretAccessKey = "" }, "both accessKeyId"},
		{func(s *spec.S3Fetch) { s.Since = "yesterday" }, "since"},
		{func(s *spec.S3Fetch) { s.MoveTo = "in/done/" }, "moveTo"},
		{func(s *spec.S3Fetch) { s.MoveTo, s.DeleteAfter = "done/", true }, "not both"},
		{func(s *spec.S3Fetch) { s.Endpoint = "ftp://x" }, "endpoint"},
		{func(s *spec.S3Fetch) { s.Every = "100ms" }, "every"},
		{func(s *spec.S3Fetch) { s.Glob = "[" }, "glob"},
	} {
		err := (spec.FlowSource{Fetch: s3Source("https://minio.example:9000", tc.edit)}).Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want error containing %q, got %v", tc.want, err)
		}
	}
	both := spec.FlowSource{Fetch: &spec.FetchSource{HTTP: &spec.HTTPFetch{URL: "https://x"}, S3: &spec.S3Fetch{Bucket: "bkt"}}}
	if err := both.Validate(); err == nil {
		t.Error("http and s3 together must be rejected")
	}
	if got := (&spec.S3Fetch{Bucket: "logs", Region: "eu-west-1"}).EndpointURL(); got != "https://logs.s3.eu-west-1.amazonaws.com" {
		t.Errorf("AWS endpoint %s", got)
	}
}
