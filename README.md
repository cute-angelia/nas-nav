# NAS Nav Lite · 私人导航页

轻量白色导航首页。Go 1.23 + Vue 3.5.13；Vue 脚本随 Go `//go:embed` 编入程序，访问时无需外部 CDN；部署无需 Node.js、npm、数据库。

## 功能

- 浏览模式：紧凑网址网格、实时搜索、点击打开（新标签页）。
- 页面访问密码；编辑需要再次输入密码。编辑授权有效期 15 分钟；可点击「完成」提前锁定编辑，或点击「锁定」退出访问。
- 编辑模式：鼠标/触摸拖动排序网址和分类；可跨分类移动网址；新增、修改、删除分类和网址。
- 编辑模式：支持上传自定义背景图片（JPG / PNG / GIF / WebP，最大 5MB），背景保存在本地数据目录。
- 新增或修改网址时，自动检测**全部分类**中是否已有相同的「域名 + 端口 + 路径」；输入时提示冲突，保存时由前后端双重校验。
- 分类、网址与背景设置保存到 `data/navigation.json`，背景图片保存为 `data/background.*`，容器更新后保留。旧版本 `nas-home` 的 JSON 数据可直接迁移。
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

## 旧版导航数据迁移

如果你正在使用上一版 `nas-home` (Node.js 版)，先停止旧服务，备份旧数据，再将它复制到新版目录：

```bash
# 命令执行位置：新版 nas-nav-lite 目录下
cp /你的旧目录/nas-home/data/navigation.json ./data/navigation.json
```

新版读取同样的 `categories[] / links[]` JSON 结构，已支持旧的名称、链接、图标和说明字段；旧的分类颜色字段在首次保存时会被省略（本版界面不依赖颜色）。建议原文件留一个备份。两个版本不要同时写同一份数据文件。

## 数据和安全

- 存储：`data/navigation.json`，每次编辑原子写入，建议定期备份这个文件。
- 背景：上传图片会保存到 `data/background.*`，只有登录后才会读取。
- 不使用云数据库、统计脚本、CDN 或外部 API；图标默认使用文本/Emoji。
- HTTP-only SameSite Cookie 维护会话；会话在服务重启后失效。
- 未登录时接口无法读取导航数据；浏览模式下服务端拒绝写入。
- 登录和编辑解锁都有失败次数限制；多页面同时编辑时会提示数据冲突。
- 仅在家庭局域网使用可通过 HTTP 访问；要从公网访问，请通过可信的 HTTPS 反向代理或 VPN，并设置 `COOKIE_SECURE=1`，避免直接裸露端口。

## 网址去重规则（v1.1）

- 判重键为 **主机名（含端口）+ URL 路径**，所有分类共用一套判重规则，不允许跨分类重复。
- 端口算在内：`http://nas.local:5000/app` 与 `http://nas.local:5001/app` 可以同时保存。
- 无显式端口时使用协议默认端口：`http` 为 `80`、`https` 为 `443`；因此 `https://a.test/x` 与 `https://a.test:443/x` 重复。
- 使用相同端口时，无论 `http` 还是 `https`，主机名与路径相同就会提示重复。
- 域名大小写不敏感；查询参数 `?q=...` 和锚点 `#...` 不参与判重。路径区分大小写和末尾 `/`。
- 修改现有网址的名称、图标、分类或保留原网址，均不会把自身误判成重复。
- 网址检查仅检查**已收藏的地址**，不会连接目标主机验证站点是否在线；内网 NAS 地址也可直接保存。
- 已有数据不会自动修改。如旧数据原先存在重复项，下一次保存时服务端会指出冲突，需要删除或修改重复项。

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
