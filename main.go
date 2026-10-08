package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var assets embed.FS

type Link struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Icon        string `json:"icon"`
	Description string `json:"description,omitempty"`
}
type Category struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Links []Link `json:"links"`
}
type Navigation struct {
	Version         int        `json:"version"`
	BackgroundImage string     `json:"backgroundImage,omitempty"`
	Categories      []Category `json:"categories"`
}
type SaveRequest struct {
	Revision        string     `json:"revision"`
	BackgroundImage string     `json:"backgroundImage,omitempty"`
	Categories      []Category `json:"categories"`
}
type Session struct {
	Expires time.Time
}
type Limit struct {
	Count int
	Until time.Time
}
type App struct {
	mu           sync.Mutex
	sessions     map[string]*Session
	attempts     map[string]Limit
	passwordHash [32]byte
	file         string
	secureCookie bool
	static       http.Handler
}

const (
	sessionTTL    = 7 * 24 * time.Hour
	attemptWindow = 15 * time.Minute
	maxAttempts   = 10
)

func main() {
	password := os.Getenv("NAV_PASSWORD")
	if len([]rune(password)) < 8 {
		log.Fatal("请设置至少 8 位的 NAV_PASSWORD（见 .env.example）")
	}
	port := getenv("PORT", "8787")
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		log.Fatal("PORT 必须为 1–65535")
	}
	folder := getenv("DATA_DIR", "./data")
	if err := os.MkdirAll(folder, 0700); err != nil {
		log.Fatal(err)
	}
	app, err := newApp(password, filepath.Join(folder, "navigation.json"), os.Getenv("COOKIE_SECURE") == "1")
	if err != nil {
		log.Fatal(err)
	}
	addr := net.JoinHostPort(getenv("HOST", "0.0.0.0"), port)
	log.Printf("NAV LITE 已启动：http://%s", addr)
	server := &http.Server{Addr: addr, Handler: app, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func newApp(password, file string, secure bool) (*App, error) {
	sub, err := fs.Sub(assets, "web")
	if err != nil {
		return nil, err
	}
	a := &App{sessions: make(map[string]*Session), attempts: make(map[string]Limit), passwordHash: sha256.Sum256([]byte(password)), file: file, secureCookie: secure, static: http.FileServer(http.FS(sub))}
	if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
		seed := Navigation{Version: 1, Categories: []Category{
			{ID: "cat-daily", Name: "常用", Links: []Link{{ID: "link-chatgpt", Name: "ChatGPT", URL: "https://chatgpt.com", Icon: "✦"}, {ID: "link-github", Name: "GitHub", URL: "https://github.com", Icon: "⌘"}, {ID: "link-bilibili", Name: "哔哩哔哩", URL: "https://www.bilibili.com", Icon: "▶"}}},
			{ID: "cat-dev", Name: "开发", Links: []Link{{ID: "link-docker", Name: "Docker Hub", URL: "https://hub.docker.com", Icon: "◫"}, {ID: "link-mdn", Name: "MDN", URL: "https://developer.mozilla.org/zh-CN", Icon: "M"}}},
			{ID: "cat-nas", Name: "NAS", Links: []Link{}},
		}}
		if err := a.writeData(seed); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self'; img-src 'self' data: http: https:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	if r.URL.Path == "/media/background" {
		a.serveBackground(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		a.api(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, 405, map[string]any{"error": "请求方法不支持"})
		return
	}
	if r.URL.Path == "/" {
		b, err := assets.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, "页面加载失败", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
		return
	}
	a.static.ServeHTTP(w, r)
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	method, path := r.Method, r.URL.Path
	if method == "GET" && path == "/api/me" {
		a.mu.Lock()
		s := a.session(r)
		a.mu.Unlock()
		writeJSON(w, 200, map[string]any{"loggedIn": s != nil})
		return
	}
	if method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if (!strings.HasPrefix(contentType, "multipart/form-data") && contentType != "application/json") || r.Header.Get("X-Nav-Request") != "1" || !sameOrigin(r) {
			writeJSON(w, 403, map[string]any{"error": "请求未通过验证"})
			return
		}
	}
	if method == "PUT" {
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Nav-Request") != "1" || !sameOrigin(r) {
			writeJSON(w, 403, map[string]any{"error": "请求未通过验证"})
			return
		}
	}
	if method == "DELETE" {
		if r.Header.Get("X-Nav-Request") != "1" || !sameOrigin(r) {
			writeJSON(w, 403, map[string]any{"error": "请求未通过验证"})
			return
		}
	}
	if method == "POST" && path == "/api/login" {
		if a.checkAttempt(r) {
			writeJSON(w, 429, map[string]any{"error": "尝试次数过多，15 分钟后重试"})
			return
		}
		var input struct {
			Password string `json:"password"`
		}
		if err := readJSON(r, &input); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if !a.verify(input.Password) {
			a.failAttempt(r)
			writeJSON(w, 401, map[string]any{"error": "密码错误"})
			return
		}
		a.resetAttempt(r)
		token, err := newToken()
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": "服务端错误"})
			return
		}
		a.mu.Lock()
		a.sessions[digest(token)] = &Session{Expires: time.Now().Add(sessionTTL)}
		a.mu.Unlock()
		a.setCookie(w, token, int(sessionTTL.Seconds()))
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	a.mu.Lock()
	s := a.session(r)
	a.mu.Unlock()
	if s == nil {
		writeJSON(w, 401, map[string]any{"error": "请先解锁页面"})
		return
	}
	if method == "POST" && path == "/api/logout" {
		token, _ := r.Cookie("nav_session")
		a.mu.Lock()
		if token != nil {
			delete(a.sessions, digest(token.Value))
		}
		a.mu.Unlock()
		a.setCookie(w, "", -1)
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if method == "GET" && path == "/api/data" {
		a.mu.Lock()
		data, rev, err := a.load()
		a.mu.Unlock()
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": "无法读取导航数据"})
			return
		}
		writeJSON(w, 200, map[string]any{"version": 1, "backgroundImage": data.BackgroundImage, "categories": data.Categories, "revision": rev})
		return
	}
	if method == "GET" && path == "/api/fetch-title" {
		targetURL := strings.TrimSpace(r.URL.Query().Get("url"))
		if targetURL == "" {
			writeJSON(w, 200, map[string]any{"url": "", "title": ""})
			return
		}
		title := fetchHTMLTitle(targetURL)
		writeJSON(w, 200, map[string]any{"url": targetURL, "title": title})
		return
	}
	if method == "GET" && path == "/api/backup" {
		backup, err := a.buildBackup(time.Now())
		if err != nil {
			log.Printf("backup export error: %v", err)
			writeJSON(w, 500, map[string]any{"error": "备份导出失败"})
			return
		}
		filename := "nas-nav-backup-" + time.Now().UTC().Format("20060102-150405") + ".zip"
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(backup)
		return
	}
	if method == "POST" && path == "/api/backup" {
		r.Body = http.MaxBytesReader(w, r.Body, maxBackupUpload+(1<<20))
		if err := r.ParseMultipartForm(maxBackupUpload + (1 << 20)); err != nil {
			writeJSON(w, 400, map[string]any{"error": "备份文件最大 8MB"})
			return
		}
		file, _, err := r.FormFile("backup")
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "请选择备份 ZIP"})
			return
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, maxBackupUpload+1))
		_ = file.Close()
		if readErr != nil || len(raw) > maxBackupUpload {
			writeJSON(w, 400, map[string]any{"error": "备份文件最大 8MB"})
			return
		}
		imported, err := parseBackup(raw)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		data, revision, err := a.restoreBackup(imported, r.FormValue("revision"))
		if errors.Is(err, errRevisionConflict) {
			writeJSON(w, 409, map[string]any{"error": "数据已被其他页面修改，请刷新后重试"})
			return
		}
		if err != nil {
			log.Printf("backup import error: %v", err)
			writeJSON(w, 500, map[string]any{"error": "备份导入失败"})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "categories": data.Categories, "backgroundImage": data.BackgroundImage, "revision": revision})
		return
	}
	if method == "POST" && path == "/api/background" {
		bg, revision, err := a.saveBackground(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "backgroundImage": bg, "revision": revision})
		return
	}
	if method == "DELETE" && path == "/api/background" {
		revision, err := a.clearBackground()
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": "背景清除失败"})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "backgroundImage": "", "revision": revision})
		return
	}
	if method == "PUT" && path == "/api/data" {
		var input SaveRequest
		if err := readJSON(r, &input); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		data := Navigation{Version: 1, BackgroundImage: input.BackgroundImage, Categories: input.Categories}
		if err := validate(data); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		a.mu.Lock()
		_, currentRev, err := a.load()
		if err == nil && currentRev != input.Revision {
			a.mu.Unlock()
			writeJSON(w, 409, map[string]any{"error": "数据已被其他页面修改，请刷新后重试"})
			return
		}
		var revision string
		if err == nil {
			err = a.writeData(data)
			if err == nil {
				_, revision, err = a.load()
			}
		}
		a.mu.Unlock()
		if err != nil {
			log.Printf("save error: %v", err)
			writeJSON(w, 500, map[string]any{"error": "保存失败，请检查目录权限"})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "revision": revision})
		return
	}
	writeJSON(w, 404, map[string]any{"error": "接口不存在"})
}
func (a *App) verify(password string) bool {
	v := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(v[:], a.passwordHash[:]) == 1
}
func (a *App) session(r *http.Request) *Session { // a.mu 必须已持有
	cookie, err := r.Cookie("nav_session")
	if err != nil || len(cookie.Value) != 64 {
		return nil
	}
	s := a.sessions[digest(cookie.Value)]
	if s == nil {
		return nil
	}
	if time.Now().After(s.Expires) {
		delete(a.sessions, digest(cookie.Value))
		return nil
	}
	return s
}
func (a *App) setCookie(w http.ResponseWriter, v string, age int) {
	http.SetCookie(w, &http.Cookie{Name: "nav_session", Value: v, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.secureCookie, MaxAge: age})
}
func (a *App) checkAttempt(r *http.Request) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.attempts[ipOf(r)]
	return ok && time.Now().Before(v.Until) && v.Count >= maxAttempts
}
func (a *App) failAttempt(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := ipOf(r)
	v := a.attempts[key]
	if time.Now().After(v.Until) {
		v = Limit{Until: time.Now().Add(attemptWindow)}
	}
	v.Count++
	a.attempts[key] = v
}
func (a *App) resetAttempt(r *http.Request) { a.mu.Lock(); delete(a.attempts, ipOf(r)); a.mu.Unlock() }
func ipOf(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
func digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host) && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 512*1024+1))
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("JSON 数据格式错误: %w", err)
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		return fmt.Errorf("JSON 格式错误或数据过大")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *App) load() (Navigation, string, error) {
	b, err := os.ReadFile(a.file)
	if err != nil {
		return Navigation{}, "", err
	}
	var data Navigation
	if err := json.Unmarshal(b, &data); err != nil {
		return Navigation{}, "", err
	}
	if data.Categories == nil {
		data.Categories = []Category{}
	}
	for i := range data.Categories {
		if data.Categories[i].Links == nil {
			data.Categories[i].Links = []Link{}
		}
	}
	return data, digest(string(b)), nil
}
func (a *App) writeData(data Navigation) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(a.file)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".navigation-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), a.file)
}

func (a *App) serveBackground(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	s := a.session(r)
	a.mu.Unlock()
	if s == nil {
		http.NotFound(w, r)
		return
	}
	file, contentType, err := a.backgroundFile()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, file)
}

func (a *App) saveBackground(r *http.Request) (string, string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, 6<<20)
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		return "", "", fmt.Errorf("背景图片最大 5MB")
	}
	file, _, err := r.FormFile("background")
	if err != nil {
		return "", "", fmt.Errorf("请选择背景图片")
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, 5<<20+1))
	if err != nil {
		return "", "", fmt.Errorf("背景图片读取失败")
	}
	if len(b) == 0 || len(b) > 5<<20 {
		return "", "", fmt.Errorf("背景图片最大 5MB")
	}
	contentType := http.DetectContentType(b)
	ext := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/gif":  ".gif",
		"image/webp": ".webp",
	}[contentType]
	if ext == "" {
		return "", "", fmt.Errorf("只支持 JPG、PNG、GIF 或 WebP 图片")
	}
	dir := filepath.Dir(a.file)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("保存目录不可写")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.removeBackgroundFiles(); err != nil {
		return "", "", fmt.Errorf("旧背景清理失败")
	}
	path := filepath.Join(dir, "background"+ext)
	if err := os.WriteFile(path, b, 0600); err != nil {
		return "", "", fmt.Errorf("背景图片保存失败")
	}
	data, _, err := a.load()
	if err != nil {
		return "", "", fmt.Errorf("导航数据读取失败")
	}
	hash := digest(string(b))[:12]
	data.BackgroundImage = "/media/background?v=" + hash
	if err := a.writeData(data); err != nil {
		return "", "", fmt.Errorf("导航数据保存失败")
	}
	_, revision, err := a.load()
	if err != nil {
		return "", "", fmt.Errorf("导航数据读取失败")
	}
	return data.BackgroundImage, revision, nil
}

func (a *App) clearBackground() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.removeBackgroundFiles(); err != nil {
		return "", err
	}
	data, _, err := a.load()
	if err != nil {
		return "", err
	}
	data.BackgroundImage = ""
	if err := a.writeData(data); err != nil {
		return "", err
	}
	_, revision, err := a.load()
	return revision, err
}

func (a *App) removeBackgroundFiles() error {
	return a.removeBackgroundFilesExcept("")
}

func (a *App) removeBackgroundFilesExcept(keep string) error {
	var matches []string
	for _, pattern := range []string{"background.*", "background-*.*"} {
		found, err := filepath.Glob(filepath.Join(filepath.Dir(a.file), pattern))
		if err != nil {
			return err
		}
		matches = append(matches, found...)
	}
	for _, match := range matches {
		if match == keep {
			continue
		}
		if err := os.Remove(match); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (a *App) backgroundFile() (string, string, error) {
	data, _, err := a.load()
	if err != nil || data.BackgroundImage == "" {
		return "", "", fmt.Errorf("背景不存在")
	}
	dir := filepath.Dir(a.file)
	hash := strings.TrimPrefix(data.BackgroundImage, "/media/background?v=")
	patterns := []string{filepath.Join(dir, "background-"+hash+".*"), filepath.Join(dir, "background.*")}
	var matches []string
	for _, pattern := range patterns {
		found, globErr := filepath.Glob(pattern)
		if globErr != nil {
			return "", "", globErr
		}
		matches = append(matches, found...)
	}
	if len(matches) == 0 {
		return "", "", fmt.Errorf("背景不存在")
	}
	ext := strings.ToLower(filepath.Ext(matches[0]))
	contentType := map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp"}[ext]
	if contentType == "" {
		return "", "", fmt.Errorf("背景格式无效")
	}
	return matches[0], contentType, nil
}

// addressKey uses host (including its effective port) and URL path as the
// duplicate identity. Query strings and fragments are intentionally ignored.
// Default http/https ports are treated the same as explicitly written ports.
func addressKey(u *url.URL) (string, error) {
	hostname := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if hostname == "" {
		return "", fmt.Errorf("网址缺少域名")
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("网址端口必须在 1–65535 之间")
		}
		port = strconv.Itoa(n)
	} else if u.Scheme == "https" {
		port = "443"
	} else {
		port = "80"
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return hostname + "\x00" + port + "\x00" + path, nil
}

func validate(v Navigation) error {
	if len(v.Categories) > 40 {
		return fmt.Errorf("分类最多 40 个")
	}
	if v.BackgroundImage != "" && !strings.HasPrefix(v.BackgroundImage, "/media/background?v=") {
		return fmt.Errorf("背景图片地址无效")
	}
	ids := make(map[string]bool)
	addresses := make(map[string]string)
	count := 0
	check := func(s string, n int) bool { return s != "" && len([]rune(s)) <= n && strings.TrimSpace(s) == s }
	for _, cat := range v.Categories {
		if !check(cat.ID, 100) || !check(cat.Name, 40) || ids[cat.ID] {
			return fmt.Errorf("分类名称或 ID 无效")
		}
		ids[cat.ID] = true
		if len(cat.Links) > 500 {
			return fmt.Errorf("分类网址过多")
		}
		for _, link := range cat.Links {
			count++
			if count > 500 {
				return fmt.Errorf("网址最多 500 个")
			}
			if !check(link.ID, 100) || ids[link.ID] || !check(link.Name, 60) || len([]rune(link.Icon)) > 8 || len([]rune(link.Description)) > 120 {
				return fmt.Errorf("网址名称或 ID 无效")
			}
			ids[link.ID] = true
			if len(link.URL) > 2048 || strings.TrimSpace(link.URL) != link.URL {
				return fmt.Errorf("网址长度无效")
			}
			u, err := url.Parse(link.URL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
				return fmt.Errorf("只允许 http / https 地址")
			}
			key, err := addressKey(u)
			if err != nil {
				return err
			}
			if previous, exists := addresses[key]; exists {
				return fmt.Errorf("网址重复：%s 与 %s 的域名（含端口）和路径相同", previous, cat.Name+" / "+link.Name)
			}
			addresses[key] = cat.Name + " / " + link.Name
		}
	}
	return nil
}

var titleRegexp = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func fetchHTMLTitle(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}

	client := &http.Client{
		Timeout: 3500 * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	limitReader := io.LimitReader(resp.Body, 64*1024)
	buf, err := io.ReadAll(limitReader)
	if err != nil && !errors.Is(err, io.EOF) {
		return ""
	}

	matches := titleRegexp.FindSubmatch(buf)
	if len(matches) < 2 {
		return ""
	}

	rawTitle := string(matches[1])
	rawTitle = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(rawTitle, " "))
	rawTitle = html.UnescapeString(rawTitle)
	runes := []rune(rawTitle)
	if len(runes) > 60 {
		rawTitle = string(runes[:60])
	}
	return rawTitle
}

