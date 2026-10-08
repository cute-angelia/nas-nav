package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfileCopiesAllGoSources(t *testing.T) {
	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "COPY *.go ./") {
		t.Fatal("Dockerfile must copy every Go source file used by the main package")
	}
}

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
	if r := send("/api/data", "PUT", map[string]any{}, nil); r.Code != 401 {
		t.Fatalf("anonymous write was not rejected: %d %s", r.Code, r.Body.String())
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
	save := send("/api/data", "PUT", saveReq, cookie)
	if save.Code != 200 {
		t.Fatalf("authenticated save failed without a second password: %d %s", save.Code, save.Body.String())
	}
	if send("/api/data", "PUT", saveReq, cookie).Code != 409 {
		t.Fatal("revision conflict not detected")
	}
	if send("/api/logout", "POST", map[string]any{}, cookie).Code != 200 {
		t.Fatal("logout failed")
	}
	if send("/api/data", "GET", nil, cookie).Code != 401 {
		t.Fatal("logout did not invalidate session")
	}
}

func TestBackgroundUploadServeAndClear(t *testing.T) {
	a, err := newApp("long-private-password", filepath.Join(t.TempDir(), "navigation.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	sendJSON := func(path, method string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
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
		}
		if method != http.MethodGet && method != http.MethodHead {
			r.Header.Set("X-Nav-Request", "1")
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	login := sendJSON("/api/login", "POST", map[string]any{"password": "long-private-password"}, nil)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("background", "bg.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	uploadReq := httptest.NewRequest("POST", "/api/background", &body)
	uploadReq.AddCookie(cookie)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Header.Set("X-Nav-Request", "1")
	upload := httptest.NewRecorder()
	a.ServeHTTP(upload, uploadReq)
	if upload.Code != 200 {
		t.Fatalf("upload failed: %d %s", upload.Code, upload.Body.String())
	}
	var uploadOut struct {
		BackgroundImage string `json:"backgroundImage"`
	}
	if err := json.Unmarshal(upload.Body.Bytes(), &uploadOut); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uploadOut.BackgroundImage, "/media/background?v=") {
		t.Fatalf("invalid background URL: %q", uploadOut.BackgroundImage)
	}
	anonMedia := httptest.NewRecorder()
	a.ServeHTTP(anonMedia, httptest.NewRequest("GET", "/media/background", nil))
	if anonMedia.Code != 404 {
		t.Fatalf("anonymous background access code: %d", anonMedia.Code)
	}
	mediaReq := httptest.NewRequest("GET", "/media/background", nil)
	mediaReq.AddCookie(cookie)
	media := httptest.NewRecorder()
	a.ServeHTTP(media, mediaReq)
	if media.Code != 200 || media.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("background not served: %d %q", media.Code, media.Header().Get("Content-Type"))
	}
	clear := sendJSON("/api/background", "DELETE", nil, cookie)
	if clear.Code != 200 {
		t.Fatalf("clear failed: %d %s", clear.Code, clear.Body.String())
	}
	data := sendJSON("/api/data", "GET", nil, cookie)
	if !strings.Contains(data.Body.String(), `"backgroundImage":""`) {
		t.Fatalf("background was not cleared: %s", data.Body.String())
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

func TestFetchHTMLTitleAPI(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html><html><head><title>  测试网页标题 &amp; Example  </title></head><body><h1>Hello</h1></body></html>`))
	}))
	defer ts.Close()

	a, err := newApp("test-password", filepath.Join(t.TempDir(), "navigation.json"), false)
	if err != nil {
		t.Fatal(err)
	}

	// 登录获取 session
	loginReq := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"test-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-Nav-Request", "1")
	loginRec := httptest.NewRecorder()
	a.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != 200 {
		t.Fatalf("login failed: %d", loginRec.Code)
	}
	cookie := loginRec.Result().Cookies()[0]

	// 1. 无参数或空 url，应该返回 200 且 title 为空，不报错打断
	reqEmpty := httptest.NewRequest("GET", "/api/fetch-title", nil)
	reqEmpty.AddCookie(cookie)
	wEmpty := httptest.NewRecorder()
	a.ServeHTTP(wEmpty, reqEmpty)
	if wEmpty.Code != 200 {
		t.Fatalf("empty url status %d != 200", wEmpty.Code)
	}
	var resEmpty map[string]string
	if err := json.Unmarshal(wEmpty.Body.Bytes(), &resEmpty); err != nil {
		t.Fatal(err)
	}
	if resEmpty["title"] != "" {
		t.Fatalf("expected empty title, got %q", resEmpty["title"])
	}

	// 2. 有效 url，正确提取网页标题并解码 html 实体
	reqValid := httptest.NewRequest("GET", "/api/fetch-title?url="+url.QueryEscape(ts.URL), nil)
	reqValid.AddCookie(cookie)
	wValid := httptest.NewRecorder()
	a.ServeHTTP(wValid, reqValid)
	if wValid.Code != 200 {
		t.Fatalf("fetch title status %d != 200", wValid.Code)
	}
	var resValid map[string]string
	if err := json.Unmarshal(wValid.Body.Bytes(), &resValid); err != nil {
		t.Fatal(err)
	}
	if resValid["title"] != "测试网页标题 & Example" {
		t.Fatalf("expected '测试网页标题 & Example', got %q", resValid["title"])
	}

	// 3. 无效或无法访问的 url，静默返回空标题，不返回 400 或 500
	reqBad := httptest.NewRequest("GET", "/api/fetch-title?url=http://127.0.0.1:54321/not-found", nil)
	reqBad.AddCookie(cookie)
	wBad := httptest.NewRecorder()
	a.ServeHTTP(wBad, reqBad)
	if wBad.Code != 200 {
		t.Fatalf("bad url status %d != 200", wBad.Code)
	}
}

func TestFaviconEndpoint(t *testing.T) {
	a, err := newApp("test-password", filepath.Join(t.TempDir(), "navigation.json"), false)
	if err != nil {
		t.Fatal(err)
	}

	// 1. /favicon.ico
	wICO := httptest.NewRecorder()
	a.ServeHTTP(wICO, httptest.NewRequest("GET", "/favicon.ico", nil))
	if wICO.Code != 200 {
		t.Fatalf("expected 200 for /favicon.ico, got %d", wICO.Code)
	}
	if !strings.Contains(wICO.Header().Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("expected image/svg+xml, got %q", wICO.Header().Get("Content-Type"))
	}
	if !strings.Contains(wICO.Body.String(), "<svg") {
		t.Fatalf("expected svg content in /favicon.ico")
	}

	// 2. /favicon.svg
	wSVG := httptest.NewRecorder()
	a.ServeHTTP(wSVG, httptest.NewRequest("GET", "/favicon.svg", nil))
	if wSVG.Code != 200 {
		t.Fatalf("expected 200 for /favicon.svg, got %d", wSVG.Code)
	}
	if !strings.Contains(wSVG.Body.String(), "<svg") {
		t.Fatalf("expected svg content in /favicon.svg")
	}
}

func TestTitleAndLogoPersistenceAndUpload(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "navigation.json")
	a, err := newApp("secret-pass", tmpFile, false)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 未登录访问 /api/me，应该正常返回且无需密码
	reqMe := httptest.NewRequest("GET", "/api/me", nil)
	wMe := httptest.NewRecorder()
	a.ServeHTTP(wMe, reqMe)
	if wMe.Code != 200 {
		t.Fatalf("expected 200 for /api/me, got %d", wMe.Code)
	}

	// 2. 登录
	loginBody := bytes.NewBufferString(`{"password":"secret-pass"}`)
	reqLogin := httptest.NewRequest("POST", "/api/login", loginBody)
	reqLogin.Header.Set("Content-Type", "application/json")
	reqLogin.Header.Set("X-Nav-Request", "1")
	wLogin := httptest.NewRecorder()
	a.ServeHTTP(wLogin, reqLogin)
	if wLogin.Code != 200 {
		t.Fatalf("login failed: %d %s", wLogin.Code, wLogin.Body.String())
	}
	cookie := wLogin.Result().Cookies()[0]

	// 3. 读取当前 revision
	reqData := httptest.NewRequest("GET", "/api/data", nil)
	reqData.AddCookie(cookie)
	wData := httptest.NewRecorder()
	a.ServeHTTP(wData, reqData)
	var dataResp struct {
		Revision   string     `json:"revision"`
		Categories []Category `json:"categories"`
	}
	if err := json.Unmarshal(wData.Body.Bytes(), &dataResp); err != nil {
		t.Fatal(err)
	}

	// 4. 保存自定义 title
	savePayload := map[string]any{
		"revision":   dataResp.Revision,
		"title":      "我的专属NAS导航",
		"categories": dataResp.Categories,
	}
	saveBuf, _ := json.Marshal(savePayload)
	reqSave := httptest.NewRequest("PUT", "/api/data", bytes.NewReader(saveBuf))
	reqSave.AddCookie(cookie)
	reqSave.Header.Set("Content-Type", "application/json")
	reqSave.Header.Set("X-Nav-Request", "1")
	wSave := httptest.NewRecorder()
	a.ServeHTTP(wSave, reqSave)
	if wSave.Code != 200 {
		t.Fatalf("save title failed: %d %s", wSave.Code, wSave.Body.String())
	}

	// 5. 上传自定义 Logo (SVG 或 PNG)
	var logoBuf bytes.Buffer
	writer := multipart.NewWriter(&logoBuf)
	part, err := writer.CreateFormFile("logo", "logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	svgContent := `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><circle cx="16" cy="16" r="16" fill="red"/></svg>`
	part.Write([]byte(svgContent))
	writer.Close()

	reqLogo := httptest.NewRequest("POST", "/api/logo", &logoBuf)
	reqLogo.AddCookie(cookie)
	reqLogo.Header.Set("Content-Type", writer.FormDataContentType())
	reqLogo.Header.Set("X-Nav-Request", "1")
	wLogo := httptest.NewRecorder()
	a.ServeHTTP(wLogo, reqLogo)
	if wLogo.Code != 200 {
		t.Fatalf("upload logo failed: %d %s", wLogo.Code, wLogo.Body.String())
	}

	var logoResp struct {
		OK   bool   `json:"ok"`
		Logo string `json:"logo"`
	}
	if err := json.Unmarshal(wLogo.Body.Bytes(), &logoResp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(logoResp.Logo, "/media/logo?v=") {
		t.Fatalf("unexpected logo url: %s", logoResp.Logo)
	}

	// 6. 公开访问 /media/logo，应该返回 200 和 svg 内容
	reqMedia := httptest.NewRequest("GET", "/media/logo", nil)
	wMedia := httptest.NewRecorder()
	a.ServeHTTP(wMedia, reqMedia)
	if wMedia.Code != 200 {
		t.Fatalf("expected 200 for /media/logo, got %d", wMedia.Code)
	}
	if !strings.Contains(wMedia.Body.String(), "<svg") {
		t.Fatalf("expected svg content from /media/logo")
	}

	// 7. /api/me 应该带有自定义 title 和 logo
	wMe2 := httptest.NewRecorder()
	a.ServeHTTP(wMe2, httptest.NewRequest("GET", "/api/me", nil))
	var meResp struct {
		Title string `json:"title"`
		Logo  string `json:"logo"`
	}
	if err := json.Unmarshal(wMe2.Body.Bytes(), &meResp); err != nil {
		t.Fatal(err)
	}
	if meResp.Title != "我的专属NAS导航" {
		t.Fatalf("expected title '我的专属NAS导航', got %q", meResp.Title)
	}
	if meResp.Logo != logoResp.Logo {
		t.Fatalf("expected logo %q, got %q", logoResp.Logo, meResp.Logo)
	}
}


