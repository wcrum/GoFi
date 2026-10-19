package driver

import (
	"testing"

	"github.com/wcrum/gofi/spec"
)

func TestSourceEgressForS3AndSQLFetch(t *testing.T) {
	s3 := sourceEgress("logs", spec.FlowSource{Fetch: &spec.FetchSource{S3: &spec.S3Fetch{Bucket: "logs", Region: "eu-west-1"}}})
	if s3["remoteHost"] != "logs.s3.eu-west-1.amazonaws.com" || s3["port"] != int64(443) || s3["remoteProtocol"] != "TLS" {
		t.Errorf("AWS S3 egress: %v", s3)
	}
	minio := sourceEgress("logs", spec.FlowSource{Fetch: &spec.FetchSource{S3: &spec.S3Fetch{Endpoint: "http://minio.minio.svc.cluster.local:9000", Bucket: "logs", PathStyle: true}}})
	if minio["remoteNamespace"] != "minio" || minio["port"] != int64(9000) {
		t.Errorf("in-cluster MinIO egress: %v", minio)
	}
	db := sourceEgress("orders", spec.FlowSource{Fetch: &spec.FetchSource{SQL: &spec.SQLFetch{Driver: "postgres", DSN: "postgres://ro@10.1.2.3/orders"}}})
	if db["remoteCidr"] != "10.1.2.3/32" || db["port"] != int64(5432) {
		t.Errorf("SQL egress: %v", db)
	}
}
