package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStructuredRequestLogsExcludeCredentialsAndQueries(t *testing.T) {
	var out bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	defer slog.SetDefault(previous)
	handler := structuredRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	request := httptest.NewRequest("POST", "/gofi.v1.GoFiService/UpsertListener?token=query-secret", strings.NewReader("body-secret"))
	request.Header.Set("Authorization", "Bearer header-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var record map[string]any
	if e := json.Unmarshal(out.Bytes(), &record); e != nil {
		t.Fatal(e)
	}
	if record["method"] != "POST" || record["status"] != float64(201) || record["path"] != "/gofi.v1.GoFiService/UpsertListener" {
		t.Fatal(record)
	}
	for _, secret := range []string{"query-secret", "body-secret", "header-secret"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("request log leaked credential or payload")
		}
	}
}
func TestStructuredRecoveryPreservesJSONLogs(t *testing.T) {
	var out bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	defer slog.SetDefault(previous)
	handler := structuredRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("secret-panic-payload") }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/rpc", nil))
	if response.Code != 500 {
		t.Fatal(response.Code)
	}
	decoder := json.NewDecoder(&out)
	records := 0
	for decoder.More() {
		var record map[string]any
		if e := decoder.Decode(&record); e != nil {
			t.Fatal(e)
		}
		records++
		if strings.Contains(record["msg"].(string), "secret-panic") {
			t.Fatal("panic payload leaked")
		}
	}
	if records != 2 {
		t.Fatalf("expected structured error+request records, got %d", records)
	}
}
