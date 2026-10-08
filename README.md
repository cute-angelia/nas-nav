# NAS Nav Lite · 私人导航页

轻量白色导航首页。Go 1.23 + Vue 3.5.13；Vue 脚本随 Go `//go:embed` 编入程序，访问时无需外部 CDN；部署无需 Node.js、npm、数据库。

## 界面预览

### 导航编辑

![NAS Nav Lite 导航编辑界面](docs/images/navigation-edit.jpg)

### 登录

<p align="center">
  <img src="docs/images/login.jpg" alt="NAS Nav Lite 登录界面" width="720">
</p>

## 功能

- 浏览模式：紧凑网址网格、实时搜索、点击打开（新标签页）。
- 页面访问密码；编辑需要再次输入密码。编辑授权有效期 15 分钟；可点击「完成」提前锁定编辑，或点击「锁定」退出访问。
- 编辑模式：鼠标/触摸拖动排序网址和分类；可跨分类移动网址；新增、修改、删除分类和网址。
- 编辑模式：支持上传自定义背景图片（JPG / PNG / GIF / WebP，最大 5MB），背景保存在本地数据目录。
- 分类、网址与背景设置保存到 `data/navigation.json`，背景图片保存为 `data/background.*`，容器更新后保留。
- 响应式布局；纯浅色；不含欢迎语、时间、侧边栏、底部装饰模块。

## Docker Compose 部署（推荐）

```bash
unzip nas-nav-lite.zip
cd nas-nav-lite
cp .env.example .env
```

编辑 `.env`，将 `NAV_PASSWORD=` 后面填上你的私人密码（至少 8 位）。然后：

```bash
docker compose up -d --build
```

浏览器访问 `http://NAS-IP:8787`。端口可以在 `.env` 中通过 `PORT` 修改。

**请妥善保存 `.env` 文件。** 忘记访问密码时，修改 `.env` 中的 `NAV_PASSWORD`，重启容器即可；旧会话会在重启后失效。

## 直接运行

需要 Go 1.23 或更新版本：

```bash
# Linux / macOS
export NAV_PASSWORD='换成至少八位的密码'
go run .
# 浏览器访问 http://localhost:8787
```

生成单个 Go 可执行文件：

```bash
go build -trimpath -ldflags='-s -w' -o nas-nav-lite .
NAV_PASSWORD='换成至少八位的密码' ./nas-nav-lite
```

其中 `web/vue.global.js` 是本地打包的 Vue 3 生产版。修改 `web` 中的页面后必须重新 `go build` 或 `docker compose up -d --build`，Go embed 才会更新。

## 数据和安全

- 存储：`data/navigation.json`，每次编辑原子写入，建议定期备份这个文件。
- 背景：上传图片会保存到 `data/background.*`，只有登录后才会读取。
- 不使用云数据库、统计脚本、CDN 或外部 API；图标默认使用文本/Emoji。
- HTTP-only SameSite Cookie 维护会话；会话在服务重启后失效。
- 未登录时接口无法读取导航数据；浏览模式下服务端拒绝写入。
- 登录和编辑解锁都有失败次数限制；多页面同时编辑时会提示数据冲突。
- 仅在家庭局域网使用可通过 HTTP 访问；要从公网访问，请通过可信的 HTTPS 反向代理或 VPN，并设置 `COOKIE_SECURE=1`，避免直接裸露端口。

## 操作

1. 登录后点击「编辑」，再次输入密码。
2. 点击「背景」上传图片；如果已有背景，可点击「清除背景」恢复默认浅色背景。
3. 每个卡片旁有 `⠿`，按住拖动到目标位置；移到另一个分类可以跨分类排序。
4. 点击卡片文字可编辑；分类标题右侧可以添加网址或修改分类。
5. 「完成」退出编辑；「锁定」退出整个页面。

## 目录说明

```text
nas-nav-lite/
├─ main.go             Go HTTP API、密码/会话、JSON 持久化、embed
├─ web/
│  ├─ index.html       Vue 页面模板
│  ├─ style.css        紧凑浅色样式
│  ├─ app.js           Vue 交互、表单、拖动排序
│  └─ vue.global.js    Vue 3.5.13 production（随包本地提供）
├─ go.mod
├─ Dockerfile
├─ compose.yaml
├─ .env.example
└─ data/               持久化数据目录
```

第三方依赖：Vue 3（MIT License）。见 `THIRD_PARTY_NOTICES.md`。
