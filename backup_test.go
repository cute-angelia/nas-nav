package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func newBackupTestApp(t *testing.T) *App {
	t.Helper()
	a, err := newApp("test-very-private-password", filepath.Join(t.TempDir(), "navigation.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func loginBackupTestApp(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	body := strings.NewReader(`{"password":"test-very-private-password"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/login", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Nav-Request", "1")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}

func backupRequest(a *App, method string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/backup", nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func uploadBackupTestBackground(t *testing.T, a *App, cookie *http.Cookie, data []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("background", "background.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/background", &body)
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-Nav-Request", "1")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("background upload failed: %d %s", w.Code, w.Body.String())
	}
}

func TestBackupExportRequiresLogin(t *testing.T) {
	a := newBackupTestApp(t)
	w := backupRequest(a, http.MethodGet, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous export returned %d: %s", w.Code, w.Body.String())
	}
}

func TestBackupExportContainsNavigationAndBackground(t *testing.T) {
	a := newBackupTestApp(t)
	cookie := loginBackupTestApp(t, a)
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	uploadBackupTestBackground(t, a, cookie, png)

	w := backupRequest(a, http.MethodGet, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("content type = %q", got)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, `attachment; filename="nas-nav-backup-`) || !strings.HasSuffix(got, `.zip"`) {
		t.Fatalf("content disposition = %q", got)
	}

	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(zr.File))
	entries := make(map[string][]byte)
	for _, file := range zr.File {
		names = append(names, file.Name)
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name], err = io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(names)
	want := []string{"background.png", "manifest.json", "navigation.json"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}
	var manifest struct {
		Format     string    `json:"format"`
		Version    int       `json:"version"`
		ExportedAt time.Time `json:"exportedAt"`
	}
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != "nas-nav-backup" || manifest.Version != 1 || manifest.ExportedAt.IsZero() {
		t.Fatalf("invalid manifest: %+v", manifest)
	}
	var navigation Navigation
	if err := json.Unmarshal(entries["navigation.json"], &navigation); err != nil {
		t.Fatal(err)
	}
	if navigation.BackgroundImage != "" {
		t.Fatalf("export leaked internal background URL: %q", navigation.BackgroundImage)
	}
	if !bytes.Equal(entries["background.png"], png) {
		t.Fatal("exported background does not match uploaded image")
	}
}

type backupEntry struct {
	name string
	data []byte
}

func makeBackupZIP(t *testing.T, entries ...backupEntry) []byte {
	t.Helper()
	var body bytes.Buffer
	zw := zip.NewWriter(&body)
	for _, item := range entries {
		entry, err := zw.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func getBackupTestData(t *testing.T, a *App, cookie *http.Cookie) (Navigation, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("get data failed: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		BackgroundImage string     `json:"backgroundImage"`
		Categories      []Category `json:"categories"`
		Revision        string     `json:"revision"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return Navigation{Version: 1, BackgroundImage: result.BackgroundImage, Categories: result.Categories}, result.Revision
}

func importBackupRequest(a *App, cookie *http.Cookie, raw []byte, revision string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("backup", "backup.zip")
	_, _ = part.Write(raw)
	_ = writer.WriteField("revision", revision)
	_ = writer.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/backup", &body)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-Nav-Request", "1")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func readBackupTestBackground(t *testing.T, a *App) []byte {
	t.Helper()
	path, _, err := a.backgroundFile()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBackupExportWithoutBackgroundHasTwoEntries(t *testing.T) {
	a := newBackupTestApp(t)
	cookie := loginBackupTestApp(t, a)
	w := backupRequest(a, http.MethodGet, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", w.Code, w.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 || zr.File[0].Name != "manifest.json" || zr.File[1].Name != "navigation.json" {
		t.Fatalf("archive entries = %v", zr.File)
	}
}

func TestBackupImportRestoresNavigationAndBackground(t *testing.T) {
	a := newBackupTestApp(t)
	cookie := loginBackupTestApp(t, a)
	originalPNG := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3, 4}
	uploadBackupTestBackground(t, a, cookie, originalPNG)
	exported := backupRequest(a, http.MethodGet, cookie)
	if exported.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", exported.Code, exported.Body.String())
	}

	a.mu.Lock()
	changed, _, err := a.load()
	if err == nil {
		changed.Categories[0].Name = "Changed after export"
		err = a.writeData(changed)
	}
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	changedPNG := append(append([]byte(nil), originalPNG...), 9)
	uploadBackupTestBackground(t, a, cookie, changedPNG)
	_, revision := getBackupTestData(t, a, cookie)

	imported := importBackupRequest(a, cookie, exported.Body.Bytes(), revision)
	if imported.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", imported.Code, imported.Body.String())
	}
	data, _ := getBackupTestData(t, a, cookie)
	if data.Categories[0].Name != "常用" {
		t.Fatalf("navigation was not restored: %q", data.Categories[0].Name)
	}
	if data.BackgroundImage == "" || !bytes.Equal(readBackupTestBackground(t, a), originalPNG) {
		t.Fatal("background was not restored")
	}
}

func TestBackupImportWithoutBackgroundClearsCurrentBackground(t *testing.T) {
	a := newBackupTestApp(t)
	cookie := loginBackupTestApp(t, a)
	exported := backupRequest(a, http.MethodGet, cookie)
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 5, 6, 7, 8}
	uploadBackupTestBackground(t, a, cookie, png)
	_, revision := getBackupTestData(t, a, cookie)

	imported := importBackupRequest(a, cookie, exported.Body.Bytes(), revision)
	if imported.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", imported.Code, imported.Body.String())
	}
	data, _ := getBackupTestData(t, a, cookie)
	if data.BackgroundImage != "" || readBackupTestBackground(t, a) != nil {
		t.Fatal("background was not cleared by backup without background")
	}
}

func TestBackupImportRequiresLogin(t *testing.T) {
	a := newBackupTestApp(t)
	w := importBackupRequest(a, nil, []byte("not a zip"), "revision")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous import returned %d: %s", w.Code, w.Body.String())
	}
}

func TestBackupImportRejectsInvalidArchivesWithoutChangingState(t *testing.T) {
	validManifest := []byte(`{"format":"nas-nav-backup","version":1,"exportedAt":"2026-10-08T00:00:00Z"}`)
	validNavigation := []byte(`{"version":1,"categories":[{"id":"cat","name":"Kept","links":[]}]}`)
	validPNG := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3, 4}
	tests := []struct {
		name string
		raw  func(*testing.T) []byte
	}{
		{name: "malformed zip", raw: func(*testing.T) []byte { return []byte("not a zip") }},
		{name: "unknown entry", raw: func(t *testing.T) []byte {
			return makeBackupZIP(t,
				backupEntry{"manifest.json", validManifest}, backupEntry{"navigation.json", validNavigation}, backupEntry{"notes.txt", []byte("no")})
		}},
		{name: "nested entry", raw: func(t *testing.T) []byte {
			return makeBackupZIP(t,
				backupEntry{"manifest.json", validManifest}, backupEntry{"navigation.json", validNavigation}, backupEntry{"folder/background.png", validPNG})
		}},
		{name: "path traversal", raw: func(t *testing.T) []byte {
			return makeBackupZIP(t,
				backupEntry{"manifest.json", validManifest}, backupEntry{"navigation.json", validNavigation}, backupEntry{"../background.png", validPNG})
		}},
		{name: "duplicate entry", raw: func(t *testing.T) []byte {
			return makeBackupZIP(t,
				backupEntry{"manifest.json", validManifest}, backupEntry{"navigation.json", validNavigation}, backupEntry{"navigation.json", validNavigation})
		}},
		{name: "invalid manifest", raw: func(t *testing.T) []byte {
			return makeBackupZIP(t,
				backupEntry{"manifest.json", []byte(`{"format":"other","version":1}`)}, backupEntry{"navigation.json", validNavigation})
		}},
		{name: "oversized navigation", raw: func(t *testing.T) []byte {
			return makeBackupZIP(t,
				backupEntry{"manifest.json", validManifest}, backupEntry{"navigation.json", bytes.Repeat([]byte("x"), 512<<10+1)})
		}},
		{name: "invalid background", raw: func(t *testing.T) []byte {
			return makeBackupZIP(t,
				backupEntry{"manifest.json", validManifest}, backupEntry{"navigation.json", validNavigation}, backupEntry{"background.png", []byte("not an image")})
		}},
		{name: "background extension mismatch", raw: func(t *testing.T) []byte {
			return makeBackupZIP(t,
				backupEntry{"manifest.json", validManifest}, backupEntry{"navigation.json", validNavigation}, backupEntry{"background.jpg", validPNG})
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := newBackupTestApp(t)
			cookie := loginBackupTestApp(t, a)
			uploadBackupTestBackground(t, a, cookie, validPNG)
			before, revision := getBackupTestData(t, a, cookie)
			beforeBackground := readBackupTestBackground(t, a)

			w := importBackupRequest(a, cookie, test.raw(t), revision)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid backup returned %d: %s", w.Code, w.Body.String())
			}
			after, afterRevision := getBackupTestData(t, a, cookie)
			if afterRevision != revision || !bytes.Equal(beforeBackground, readBackupTestBackground(t, a)) || !equalNavigation(before, after) {
				t.Fatal("failed import changed current state")
			}
		})
	}
}

func TestBackupImportRejectsStaleRevisionWithoutChangingState(t *testing.T) {
	a := newBackupTestApp(t)
	cookie := loginBackupTestApp(t, a)
	exported := backupRequest(a, http.MethodGet, cookie)
	before, revision := getBackupTestData(t, a, cookie)
	w := importBackupRequest(a, cookie, exported.Body.Bytes(), "stale-"+revision)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale import returned %d: %s", w.Code, w.Body.String())
	}
	after, afterRevision := getBackupTestData(t, a, cookie)
	if revision != afterRevision || !equalNavigation(before, after) {
		t.Fatal("stale import changed current state")
	}
}

func equalNavigation(a, b Navigation) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}
