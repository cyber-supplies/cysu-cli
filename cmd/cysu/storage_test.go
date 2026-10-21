package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStorageDeleteUsesExactPathAndFileID(t *testing.T) {
	oldToken := apiToken
	apiToken = "test-token"
	t.Cleanup(func() { apiToken = oldToken })

	const id = "ZG9jcy9yZXBvcnQudHh0"
	deleted := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/storage/files":
			json.NewEncoder(w).Encode([]map[string]string{{"id": id, "path": "docs/report.txt"}})
		case r.Method == "DELETE" && r.URL.Path == "/api/storage/files/"+id:
			deleted++
			json.NewEncoder(w).Encode(map[string]string{"id": id, "path": "docs/report.txt"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("CYSU_API_URL", server.URL)

	var result map[string]string
	decodePrintedJSON(t, captureStdout(t, func() error {
		return deleteFile("/docs/report.txt")
	}), &result)
	if result["id"] != id || result["path"] != "docs/report.txt" || deleted != 1 {
		t.Fatalf("delete result = %#v, requests = %d", result, deleted)
	}
	if err := deleteFile("docs"); err == nil || !strings.Contains(err.Error(), "no stored file") {
		t.Fatalf("directory deletion error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("directory prefix sent DELETE; requests = %d", deleted)
	}
}

func TestStorageDeleteRequiresPath(t *testing.T) {
	command, ok := storageResource.lookup("delete")
	if !ok {
		t.Fatal("storage delete command is missing")
	}
	if _, err := command.parse(storageResource, nil); err == nil {
		t.Fatal("missing --path was accepted")
	}
}
