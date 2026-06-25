# PR Collector

GitHub PR 贡献徽章服务 — 在你的 Profile README 中嵌入 SVG 徽章，展示个人 Pull Request 统计。

```
![PR Stats](https://shturl.cc/card?username=muzimu&style=dark)
```

点击徽章跳转详情页查看完整 PR 列表。

## 功能

- **SVG 徽章** — 多风格支持（default / dark / compact），带强缓存
- **PR 详情页** — 展示仓库、标题、状态、时间，可跳转 GitHub
- **首次访问同步抓取** — 用户嵌入徽章后立即看到真实数据
- **每日定时全量同步** — 凌晨自动刷新所有已注册用户 PR 数据
- **手动刷新** — 详情页一键刷新
- **限流保护** — 令牌桶限流，防止恶意刷接口
- **优雅关闭** — 信号触发后等待进行中任务完成再退出

## 技术栈

| 组件 | 选型 |
|------|------|
| Web 框架 | Gin |
| 存储 | Redis |
| 定时任务 | robfig/cron v3 |
| 日志 | zerolog（本地轮转） |
| 配置 | YAML |
| 数据源 | GitHub GraphQL API v4（PAT Token） |
| 反向代理 | Caddy v2（自动 HTTPS + 限流） |
| 进程守护 | systemd |

## 环境要求

- Go 1.22+
- Redis 7.0+
- Linux（部署目标）/ macOS（开发测试）
- GitHub Personal Access Token（classic，scope: `read:user`）

## 快速开始

### 1. 克隆并配置

```bash
git clone <repo-url> && cd pr-collector
cp config.example.yaml config.yaml
vim config.yaml   # 填入 github.token 和 redis.addr
```

### 2. 启动依赖

```bash
# macOS
brew install redis && brew services start redis

# Linux
sudo apt install redis && sudo systemctl start redis
```

### 3. 运行

```bash
go run main.go
```

访问 `http://localhost:8080/card?username=muzimu` 查看 SVG 徽章。

## 配置说明

```yaml
server:
  port: 8080           # HTTP 监听端口
  mode: release        # release / debug / test

github:
  token: "ghp_xxx"     # GitHub PAT (classic)

redis:
  addr: "127.0.0.1:6379"
  password: ""
  db: 0
  pool_size: 20

cron:
  full_sync: "0 0 3 * * *"  # 全量同步 cron 表达式（秒分时日月周）
  svg_cache_ttl: 24h
  fetch_lock_ttl: 60s
  max_workers: 5             # 批量同步最大并发数

log:
  level: info           # debug / info / warn / error
  file: logs/app.log
  max_size: 50          # 单文件 MB
  max_backups: 7
  max_age: 30           # 保留天数

ratelimit:
  card_rps: 10          # /card 每秒最大请求
  pr_rps: 5             # /pr 每秒最大请求
```

环境变量 `CONFIG_PATH` 可指定配置文件路径。

## API

### GET /card — SVG 徽章

```
GET /card?username={github_username}&style={default|dark|compact}
```

响应：`Content-Type: image/svg+xml`，强缓存 `Cache-Control: public, max-age=86400, immutable`

| 参数 | 必填 | 说明 |
|------|------|------|
| username | 是 | GitHub 用户名 |
| style | 否 | SVG 风格，默认 default |

### GET /pr — PR 详情页

```
GET /pr?username={github_username}
```

展示该用户全部 PR 列表，含手动刷新按钮。

### POST /refresh — 手动刷新

```
POST /refresh   body: username={github_username}
```

返回 JSON：`{"ok": true, "message": "刷新任务已提交"}`

### GET /health — 健康检查

```
GET /health → {"status": "ok"}
```

## 部署

### 一键脚本

```bash
chmod +x deploy/install.sh
./deploy/install.sh
```

脚本自动完成：编译 → 复制二进制/config → 安装 systemd 服务 → 启动。

### 手动部署

```bash
# 1. 编译
CGO_ENABLED=0 go build -ldflags="-s -w" -o pr-collector .

# 2. 复制文件
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
sudo systemctl status pr-collector    # 查看状态
sudo systemctl restart pr-collector   # 重启（配置变更后）
journalctl -u pr-collector -f         # 查看日志
tail -f /opt/pr-collector/logs/app.log # 应用日志
```

### Redis 配置建议

```bash
# /etc/redis/redis.conf
maxmemory 128mb
maxmemory-policy allkeys-lru
```

## Redis 数据模型

| Key | 类型 | 说明 |
|-----|------|------|
| `users:all` | Set | 所有已注册用户名 |
| `user:{username}` | Hash | 用户元信息（状态/更新时间） |
| `pr:{username}` | List | PR 列表（JSON） |
| `svg:{username}:{style}` | String | SVG 缓存（TTL 24h） |
| `lock:fetch:{username}` | String | 抓取分布式锁（TTL 60s） |
| `stats:card_visits` | String | 卡片访问计数 |
| `stats:pr_visits` | String | 详情页访问计数 |

## 架构

```
互联网 → Caddy(:443) → Gin(:8080) → Redis
                           ↓
                      robfig/cron → GitHub GraphQL API
```

项目结构：

```
pr-collector/
├── main.go              # 入口
├── config/              # YAML 配置
├── github/              # GraphQL 客户端
├── redis/cache/         # Redis 操作
├── handler/             # HTTP 处理器
│   ├── card.go          # GET /card
│   └── pr.go            # GET /pr + POST /refresh
├── svc/                 # 业务服务
│   ├── fetcher.go       # 抓取 + worker pool
│   ├── renderer.go      # 模板渲染
│   └── tmpl/            # SVG/HTML 模板 (embed)
├── cron/                # 定时任务
├── middleware/           # 限流
├── deploy/              # Caddyfile + systemd + install.sh
└── DESIGN.md            # 详细设计文档
```

## 开发

```bash
go run main.go                    # 开发运行
CGO_ENABLED=0 go build           # 编译
go test ./...                     # 测试
```

## License

MIT
