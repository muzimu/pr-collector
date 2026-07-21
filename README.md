# PR Collector

GitHub PR 贡献徽章服务 — 在 Profile README 中嵌入 SVG 徽章，展示个人 Pull Request 统计。

![Go](https://img.shields.io/badge/Go-1.26.2-00ADD8?logo=go)
![License](https://img.shields.io/badge/License-MIT-green)

---

## 快速开始

将 `muzimu` 替换为你的 GitHub 用户名，插入 Profile README：

```markdown
[![PR Stats](http://your-host:8080/card?username=muzimu&top=3&style=default)](http://your-host:8080/pr?username=muzimu)
```

点击徽章跳转详情页查看完整 PR 列表。

---

## 功能特性

| 功能 | 说明 |
|------|------|
| SVG 徽章 | 支持 `default` / `dark` 风格，强缓存 24h |
| PR 详情页 | 展示仓库、标题、状态、时间，可跳转 GitHub |
| 排行榜首页 | 按 PR 数量与仓库 Star 综合评分，支持分页和统计卡片 |
| 卡片预览 | 在首页实时生成徽章 Markdown 和预览图 |
| HTMX 交互 | 服务端渲染 HTML fragment，支持无 JavaScript 完整页面回退 |
| 首次同步 | 用户嵌入徽章后立即抓取真实数据 |
| 定时同步 | 每日凌晨自动刷新所有已注册用户 |
| 手动刷新 | 详情页一键刷新 |
| 限流保护 | 令牌桶限流，防刷接口 |
| 优雅关闭 | 信号触发后等待进行中任务完成再退出 |

---

## 技术栈

```
Web 框架    Gin
存储        Redis
定时任务    robfig/cron v3
日志        zerolog + lumberjack（本地轮转）
配置        YAML
数据源      GitHub GraphQL API v4（PAT Token）
前端交互    htmx 2.0.10（本地嵌入）+ 少量原生 JavaScript
反向代理    Caddy v2（自动 HTTPS + 限流）
进程守护    systemd
```

---

## 环境要求

- **Go** 1.26.2（见 `go.mod`）
- **Redis** 7.0+
- **系统** Linux（systemd 部署）/ macOS、Windows（本地运行或对应平台二进制）
- **GitHub Token** Personal Access Token（classic，scope: `read:user`）

---

## 本地开发

### 1. 克隆 & 配置

```bash
git clone <repo-url> && cd pr-collector
cp config.example.yaml config.yaml
# 编辑 config.yaml，填入 github.token 和 redis.addr
```

### 2. 启动 Redis

```bash
# macOS
brew install redis && brew services start redis

# Linux
sudo apt install redis && sudo systemctl start redis
```

### 3. 运行

```bash
go run .
```

访问 `http://localhost:8080/card?username=muzimu` 查看效果。

---

## 配置说明

配置文件路径：`config.yaml`（可通过环境变量 `CONFIG_PATH` 覆盖）

```yaml
server:
  port: 8080                     # HTTP 监听端口
  mode: release                  # gin 模式: debug / release / test

github:
  token: "ghp_xxx"               # GitHub PAT (classic)

redis:
  addr: "127.0.0.1:6379"
  password: ""                   # 留空表示无密码
  db: 0
  pool_size: 20

cron:
  full_sync: "0 0 3 * * *"       # 全量同步（秒 分 时 日 月 周）
  leaderboard_sync: "0 0 4 * * *" # 排行榜缓存刷新
  leaderboard_max_raw: 100000     # 排行榜分数 raw 上限
  svg_cache_ttl: 24h             # SVG 缓存 TTL
  fetch_lock_ttl: 60s            # 分布式锁 TTL
  max_workers: 5                 # 同步并发数

log:
  level: info                    # debug / info / warn / error
  file: logs/app.log
  max_size: 50                   # 单文件 MB
  max_backups: 7
  max_age: 30                    # 保留天数

ratelimit:
  card_rps: 10                   # /card 每秒最大请求
  pr_rps: 5                      # /pr 每秒最大请求
```

---

## API

### `GET /card` — SVG 徽章

```
GET /card?username={username}&style={default|dark}&top={n}
```

| 参数 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `username` | 是 | - | GitHub 用户名 |
| `style` | 否 | `default` | SVG 风格；未知值回退到 `default` |
| `top` | 否 | `0` | 展示 Top N 仓库；`0` 表示不展示仓库列表 |

响应：`Content-Type: image/svg+xml`，`Cache-Control: public, max-age=86400, immutable`

### `GET /` — 排行榜首页

展示排行榜、统计卡片和徽章预览。排行榜默认返回前 50 名，分页接口最多允许一次读取 200 名。

### `GET /pr` — PR 详情页

```
GET /pr?username={username}
```

展示该用户全部 PR 列表，含手动刷新按钮。

### `POST /refresh` — 手动刷新

```
POST /refresh
Content-Type: application/x-www-form-urlencoded

username={username}
```

HTMX 请求（带 `HX-Request: true`）返回刷新状态 HTML fragment；普通表单请求使用 PRG，先返回 `303 See Other`，再跳转到 `/refresh/status` 完整结果页，避免浏览器刷新重复提交。

### `GET /api/leaderboard` — 排行榜分页

```
GET /api/leaderboard?limit=50&offset=0
```

HTMX 请求返回排行榜行和下一页控件；普通浏览器请求返回相同分页数据的完整首页。

### `GET /api/leaderboard/stats` — 排行榜统计

HTMX 请求返回统计卡片 fragment；普通浏览器请求返回完整首页。

### `POST /refresh/leaderboard` — 刷新排行榜缓存

HTMX 请求返回刷新状态 fragment；普通请求返回 `303 See Other`，跳转到 `/?notice=leaderboard-refreshed`。

### `GET /card/preview` — 卡片预览

```
GET /card/preview?username={username}
```

HTMX 请求返回预览和 Markdown fragment；普通表单请求返回包含预览的完整首页。

### `GET /refresh/status` — PR 刷新结果

由普通 `POST /refresh` 请求重定向到此处：

```
GET /refresh/status?username={username}&submitted={true|false}
```

以上支持 HTMX fragment 和完整页面协商的路由都会返回 `Vary: HX-Request`。htmx 会自动发送 `HX-Request: true`；不带该请求头时，服务端返回完整 HTML 页面。

### `GET /health` — 健康检查

```json
GET /health → {"status": "ok"}
```

---

## 部署

### GoReleaser 构建与发布

本项目使用 GoReleaser 生成 Linux、macOS、Windows 的 amd64/arm64 二进制归档。每个归档包含 `config.example.yaml`，发布产物和 `checksums.txt` 会自动上传到 GitHub Release。

本地验证全部目标：

```bash
goreleaser check
goreleaser build --snapshot --clean
```

创建版本 tag 并推送后，GitHub Actions 会自动发布：

```bash
git tag -a v0.2.0 -m "Release v0.2.0"
git push origin v0.2.0
```

PR 只执行 snapshot 构建验证，不会创建 GitHub Release。

### 一键脚本

```bash
# 先准备静态链接二进制和配置文件
CGO_ENABLED=0 go build -ldflags="-s -w" -o pr-collector .
cp config.example.yaml config.yaml
# 编辑 config.yaml，填入真实 token 和 Redis 地址

chmod +x deploy/install.sh
./deploy/install.sh
```

脚本要求当前目录已有 `pr-collector` 和 `config.yaml`，会复制它们、创建 `www` 服务用户、安装 systemd 服务并启动。

### 手动部署

```bash
# 1. 编译（静态链接）
CGO_ENABLED=0 go build -ldflags="-s -w" -o pr-collector .

# 2. 部署文件
sudo mkdir -p /opt/pr-collector/logs
sudo cp pr-collector config.yaml /opt/pr-collector/
sudo chmod 600 /opt/pr-collector/config.yaml   # 保护 token

# 3. 安装 systemd 服务
sudo cp deploy/pr-collector.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pr-collector

# 4. 配置 Caddy（可选，需要 HTTPS）
# 编辑 deploy/Caddyfile 中的域名，然后：
sudo caddy run --config deploy/Caddyfile
```

### 常用命令

```bash
# 服务管理
sudo systemctl status pr-collector
sudo systemctl restart pr-collector
sudo systemctl stop pr-collector

# 日志查看
journalctl -u pr-collector -f
tail -f /opt/pr-collector/logs/app.log
```

### Redis 生产配置建议

```bash
# /etc/redis/redis.conf
maxmemory 128mb
maxmemory-policy allkeys-lru
```

---

## 架构

```
互联网 → Caddy(:443) → Gin(:8080) → Redis
                           ↓
                      robfig/cron → GitHub GraphQL API
```

### 项目结构

```
pr-collector/
├── .github/workflows/release.yml # GitHub Actions 发布流程
├── .goreleaser.yaml          # 多平台构建与归档配置
├── main.go                 # 入口、路由、生命周期
├── config.example.yaml      # 配置模板
├── config/                 # YAML 配置解析
├── github/                 # GraphQL 客户端
├── redis/cache/            # Redis 读写封装
├── handler/                # HTTP 处理器
│   ├── card.go             #   GET /card
│   ├── leaderboard.go      #   首页、排行榜和卡片预览
│   ├── pr.go               #   GET /pr + POST /refresh
│   └── htmx.go             #   HX-Request 响应协商
├── svc/                    # 业务服务层
│   ├── fetcher.go          #   抓取 + worker pool
│   ├── pr_provider.go      #   PR 数据提供（缓存优先）
│   ├── renderer.go         #   模板渲染
│   ├── static/              #   HTMX 与页面静态资源 (embed)
│   └── tmpl/               #   SVG/HTML 模板 (embed)
├── cron/                   # 定时任务调度
├── middleware/              # 限流中间件
├── deploy/                 # Caddyfile + systemd + install.sh
└── DESIGN.md               # 详细设计文档
```

### Redis 数据模型

| Key | 类型 | 说明 |
|-----|------|------|
| `users:all` | Set | 所有已注册用户名 |
| `user:{username}` | Hash | 用户元信息 |
| `pr:{username}` | List | PR 列表（JSON） |
| `svg:{username}:{style}:{top}` | String | SVG 缓存（TTL） |
| `lock:fetch:{username}` | String | 分布式锁（TTL） |
| `stats:card_visits` | String | 卡片访问计数 |
| `stats:pr_visits` | String | 详情页访问计数 |
| `leaderboard:cache` | ZSET | 排行榜用户与分数 |
| `leaderboard:meta` | Hash | 排行榜统计元数据 |
| `leaderboard:user:{username}` | Hash | 单个用户的排行榜详情 |

---

## 故障排查

| 问题 | 排查方向 |
|------|----------|
| Redis 连接失败 | 检查 `redis.addr` 配置，确认 Redis 服务运行中 |
| GitHub 数据为空 | 检查 `github.token` 是否有效，scope 是否包含 `read:user` |
| systemd 启动失败 | `journalctl -u pr-collector -e` 查看错误日志 |
| 徽章不更新 | 缓存 TTL 为 24h，可调用 `/refresh` 手动刷新 |

---

## License

MIT
