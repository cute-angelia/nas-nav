package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageAuthEditAndPersistence(t *testing.T) {
	a, err := newApp("long-private-password", filepath.Join(t.TempDir(), "navigation.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	send := func(path, method string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&buf).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(method, path, &buf)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Nav-Request", "1")
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if send("/api/data", "GET", nil, nil).Code != 401 {
		t.Fatal("anonymous access")
	}
	login := send("/api/login", "POST", map[string]any{"password": "long-private-password"}, nil)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	out := send("/api/data", "GET", nil, cookie)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	var data struct {
		Revision   string     `json:"revision"`
		Categories []Category `json:"categories"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	data.Categories[0].Name = "Changed"
	saveReq := map[string]any{"revision": data.Revision, "categories": data.Categories}
	if send("/api/data", "PUT", saveReq, cookie).Code != 403 {
		t.Fatal("edit lock bypass")
	}
	if send("/api/edit/unlock", "POST", map[string]any{"password": "long-private-password"}, cookie).Code != 200 {
		t.Fatal("unlock failed")
	}
	save := send("/api/data", "PUT", saveReq, cookie)
	if save.Code != 200 {
		t.Fatal(save.Body.String())
	}
	if send("/api/data", "PUT", saveReq, cookie).Code != 409 {
		t.Fatal("revision conflict not detected")
	}
	if send("/api/edit/lock", "POST", map[string]any{}, cookie).Code != 200 {
		t.Fatal("edit lock failed")
	}
	if send("/api/data", "PUT", saveReq, cookie).Code != 403 {
		t.Fatal("edit lock bypass after re-lock")
	}
	if send("/api/logout", "POST", map[string]any{}, cookie).Code != 200 {
		t.Fatal("logout failed")
	}
	if send("/api/data", "GET", nil, cookie).Code != 401 {
		t.Fatal("logout did not invalidate session")
	}
}

func TestURLUniquenessIncludesPortAndPath(t *testing.T) {
	key := func(raw string) string {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		k, err := addressKey(u)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	same := [][2]string{
		{"https://EXAMPLE.com/docs?q=1#section", "https://example.com:443/docs?q=2"},
		{"http://nas.local", "http://nas.local:80/"},
		{"https://nas.local:5000/ui", "http://nas.local:5000/ui"},
		{"http://192.168.1.20:5000/", "http://192.168.1.20:05000/?other=1"},
	}
	for _, test := range same {
		if key(test[0]) != key(test[1]) {
			t.Fatalf("expected duplicate URLs: %q %q", test[0], test[1])
		}
	}
	different := [][2]string{
		{"http://nas.local:5000/ui", "http://nas.local:5001/ui"},
		{"https://nas.local:5000/ui", "https://nas.local:5000/other"},
		{"https://nas.local/a", "https://nas.local/a/"},
		{"http://nas.local/ui", "https://nas.local/ui"},
		{"https://a.local:5000/app", "https://b.local:5000/app"},
	}
	for _, test := range different {
		if key(test[0]) == key(test[1]) {
			t.Fatalf("distinct URLs collided: %q %q", test[0], test[1])
		}
	}
	for _, invalid := range []string{"http://nas.local:0/", "https://nas.local:65536/"} {
		u, err := url.Parse(invalid)
		if err == nil {
			if _, err = addressKey(u); err == nil {
				t.Fatalf("invalid port was accepted: %q", invalid)
			}
		}
	}
}

func TestValidateDuplicateAcrossCategories(t *testing.T) {
	data := Navigation{Version: 1, Categories: []Category{
		{ID: "cat1", Name: "NAS 服务", Links: []Link{{ID: "one", Name: "管理台", URL: "http://nas.local:5000/admin"}}},
		{ID: "cat2", Name: "常用", Links: []Link{{ID: "two", Name: "重复入口", URL: "https://NAS.local:5000/admin?from=home"}}},
	}}
	if err := validate(data); err == nil || !strings.Contains(err.Error(), "网址重复") {
		t.Fatalf("duplicate accepted: %v", err)
	}
	data.Categories[1].Links[0].URL = "http://nas.local:5001/admin"
	if err := validate(data); err != nil {
		t.Fatalf("different port rejected: %v", err)
	}
	data.Categories[1].Links[0].URL = "http://nas.local:5000/app"
	if err := validate(data); err != nil {
		t.Fatalf("different path rejected: %v", err)
	}
	data.Categories[1].Links = nil
	data.Categories[0].Links[0].Name = "修改后的名称"
	if err := validate(data); err != nil {
		t.Fatalf("editing same URL rejected: %v", err)
	}
}

func TestAPIServerRejectsDuplicateSave(t *testing.T) {
	a, err := newApp("test-very-private-password", filepath.Join(t.TempDir(), "navigation.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	send := func(path, method string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&buf).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(method, path, &buf)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Nav-Request", "1")
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	login := send("/api/login", "POST", map[string]any{"password": "test-very-private-password"}, nil)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	if r := send("/api/edit/unlock", "POST", map[string]any{"password": "test-very-private-password"}, cookie); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	out := send("/api/data", "GET", nil, cookie)
	var data struct {
		Revision   string     `json:"revision"`
		Categories []Category `json:"categories"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	data.Categories[1].Links = append(data.Categories[1].Links, Link{ID: "new-duplicate", Name: "ChatGPT 镜像", URL: "https://CHATGPT.com:443/?a=1"})
	// The default seed already has ChatGPT in the first category.
	req := map[string]any{"revision": data.Revision, "categories": data.Categories}
	bad := send("/api/data", "PUT", req, cookie)
	if bad.Code != 400 || !strings.Contains(bad.Body.String(), "网址重复") {
		t.Fatalf("duplicate was not rejected: %v %s", bad.Code, bad.Body.String())
	}
	data.Categories[1].Links[len(data.Categories[1].Links)-1].URL = "https://chatgpt.com:5000/"
	req = map[string]any{"revision": data.Revision, "categories": data.Categories}
	good := send("/api/data", "PUT", req, cookie)
	if good.Code != 200 {
		t.Fatalf("different port rejected: %d %s", good.Code, good.Body.String())
	}
}
