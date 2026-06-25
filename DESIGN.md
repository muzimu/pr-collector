# PR Collector — 完整开发文档

> GitHub PR 徽章嵌入服务 | Gin + Redis + Cron + Caddy  
> 目标部署：2核2G 轻量 Linux 服务器

---

## 目录

1. [项目概述](#1-项目概述)
2. [技术架构](#2-技术架构)
3. [工程目录结构](#3-工程目录结构)
4. [配置设计](#4-配置设计)
5. [Redis 数据结构](#5-redis-数据结构)
6. [GitHub GraphQL 抓取逻辑](#6-github-graphql-抓取逻辑)
7. [HTTP 接口设计](#7-http-接口设计)
8. [定时任务设计](#8-定时任务设计)
9. [SVG/HTML 模板设计](#9-svghtml-模板设计)
10. [Caddy 配置](#10-caddy-配置)
11. [systemd 部署](#11-systemd-部署)
12. [稳定性 & 资源适配](#12-稳定性--资源适配)
13. [开发路线图](#13-开发路线图)

---

## 1. 项目概述

### 1.1 业务场景

用户在自己的 GitHub Profile README 中嵌入 SVG 徽章图片，展示个人 PR 贡献统计。点击徽章跳转详情页查看完整 PR 列表。

```
用户 README.md 中嵌入：
![PR Stats](https://shturl.cc/card?username=muzimu&style=dark)

点击徽章 → 跳转 https://shturl.cc/pr?username=muzimu
```

### 1.2 核心功能

| 功能 | 说明 |
|------|------|
| SVG 徽章渲染 | 多风格支持 (default/dark/compact)，强缓存 |
| PR 详情页 | 展示用户全部 PR：仓库、标题、状态、时间、链接 |
| 定时同步 | 每日凌晨全量刷新所有已注册用户 PR 数据 |
| 懒加载抓取 | 首次访问自动触发异步抓取，不阻塞 HTTP 响应 |
| 手动刷新 | 详情页提供刷新按钮，即时更新 PR 数据 |

---

## 2. 技术架构

```
┌──────────────────────────────────────────────────┐
│                    互联网                         │
└──────────────────┬───────────────────────────────┘
                   │ HTTPS :443
           ┌───────▼────────┐
           │  Caddy (反向代理) │
           │  - 自动HTTPS     │
           │  - 静态资源缓存   │
           │  - 基础限流      │
           └───────┬────────┘
                   │ HTTP :8080
           ┌───────▼────────┐
           │  Gin Web 服务    │
           │  - GET /card    │
           │  - GET /pr      │
           │  - GET /refresh │
           └───┬──────┬──────┘
               │      │
      ┌────────▼─┐  ┌▼─────────┐
      │  Redis    │  │  Cron     │
      │  - PR数据  │  │  - 全量同步 │
      │  - SVG缓存 │  │  - 懒加载  │
      │  - 用户集合 │  └─────┬─────┘
      └──────────┘        │
                    ┌──────▼──────┐
                    │ GitHub       │
                    │ GraphQL API  │
                    │ (PAT Token)  │
                    └─────────────┘
```

### 2.1 技术栈

| 组件 | 选型 | 用途 |
|------|------|------|
| Web 框架 | `gin-gonic/gin` | HTTP 路由、中间件、模板渲染 |
| 数据库 | `go-redis/redis` | 用户集、PR 列表、SVG 缓存、分布式锁 |
| 定时任务 | `robfig/cron/v3` | 每日全量同步 + 懒加载异步抓取 |
| 日志 | `rs/zerolog` | 结构化日志，输出到本地文件 |
| 配置 | `gopkg.in/yaml.v3` | YAML 配置文件解析 |
| 模板 | `text/template` + `html/template` | SVG 徽章 + PR 详情页渲染 |
| GitHub | GraphQL API v4 | 批量查询 PR 数据 (PAT Token 鉴权) |
| 反向代理 | Caddy v2 | HTTPS 自动续期、静态缓存、限流 |
| 进程守护 | systemd | 崩溃重启、开机自启、日志持久化 |

### 2.2 模块依赖图 (Go 内部)

```
main.go
├── config/          # YAML 配置加载
├── redis/           # Redis 操作封装
├── github/          # GraphQL 客户端 (参考 pr-collector-py)
├── cron/            # 定时任务调度
├── handler/         # HTTP 处理器
│   ├── card.go      # GET /card
│   └── pr.go        # GET /pr
├── svc/             # 业务逻辑层
│   ├── fetcher.go   # PR 抓取服务
│   └── renderer.go  # SVG/HTML 渲染
├── tmpl/            # Go template 文件 (embed)
│   ├── svg/         # SVG 模板
│   └── html/        # HTML 模板
└── middleware/      # 限流、日志等中间件
```

---

## 3. 工程目录结构

```
pr-collector/
├── main.go                     # 入口：加载配置、初始化各模块、启动服务
├── go.mod / go.sum
├── config.example.yaml         # 配置模板（提交git）
├── config.yaml                 # 真实配置（gitignore）
├── .gitignore
├── DESIGN.md                   # 本文档
│
├── config/
│   └── config.go               # 配置结构体定义 + Load() 函数
│
├── github/
│   └── client.go               # GraphQL 客户端：search PR、处理分页、限流重试
│
├── redis/
│   └── store.go                # Redis 操作：用户集、PR列表、SVG缓存、分布式锁
│
├── handler/
│   ├── card.go                 # GET /card 处理器
│   ├── pr.go                   # GET /pr 处理器
│   └── refresh.go              # POST /refresh 处理器
│
├── svc/
│   ├── fetcher.go              # PR 抓取服务：协调 GitHub → Redis
│   └── renderer.go             # 模板渲染：SVG/HTML
│
├── cron/
│   └── scheduler.go            # Cron 任务注册和执行
│
├── middleware/
│   └── ratelimit.go            # 简易令牌桶限流
│
├── tmpl/
│   ├── svg/
│   │   ├── default.svg         # 默认风格 SVG
│   │   ├── dark.svg            # 暗色风格 SVG
│   │   └── compact.svg         # 紧凑风格 SVG
│   └── html/
│       └── pr_list.html        # PR 详情页 HTML
│
├── deploy/
│   ├── pr-collector.service    # systemd unit 文件
│   ├── Caddyfile               # Caddy 配置文件
│   └── install.sh              # 一键部署脚本
│
└── logs/                       # 日志目录（gitignore）
```

---

## 4. 配置设计

### 4.1 config.example.yaml

```yaml
# PR Collector 配置文件模板
# 复制为 config.yaml 并填入真实值

server:
  port: 8080                     # Gin 监听端口
  mode: release                  # gin 运行模式: debug / release / test

github:
  token: "ghp_xxxxxxxxxxxx"      # GitHub Personal Access Token

redis:
  addr: "127.0.0.1:6379"         # Redis 地址
  password: ""                   # Redis 密码 (留空无密码)
  db: 0                          # Redis DB 编号
  pool_size: 20                  # 连接池大小

cron:
  full_sync: "0 3 * * *"         # 全量同步 cron 表达式 (默认凌晨3点)
  svg_cache_ttl: 24h             # SVG 缓存过期时间
  fetch_lock_ttl: 60s            # 抓取锁过期时间
  max_workers: 5                 # 批量同步最大并发协程数

log:
  level: info                    # 日志级别: debug / info / warn / error
  file: logs/app.log             # 日志文件路径
  max_size: 50                   # 单文件最大 MB
  max_backups: 7                 # 最多保留日志文件数
  max_age: 30                    # 日志保留天数

ratelimit:
  card_rps: 10                   # /card 接口每秒最大请求数
  pr_rps: 5                      # /pr 接口每秒最大请求数
```

### 4.2 config/config.go

```go
package config

import (
    "os"
    "time"
    "gopkg.in/yaml.v3"
)

type Config struct {
    Server    ServerConfig    `yaml:"server"`
    GitHub    GitHubConfig    `yaml:"github"`
    Redis     RedisConfig     `yaml:"redis"`
    Cron      CronConfig      `yaml:"cron"`
    Log       LogConfig       `yaml:"log"`
    RateLimit RateLimitConfig `yaml:"ratelimit"`
}

type ServerConfig struct {
    Port int    `yaml:"port"`
    Mode string `yaml:"mode"`
}

type GitHubConfig struct {
    Token string `yaml:"token"`
}

type RedisConfig struct {
    Addr     string `yaml:"addr"`
    Password string `yaml:"password"`
    DB       int    `yaml:"db"`
    PoolSize int    `yaml:"pool_size"`
}

type CronConfig struct {
    FullSync     string        `yaml:"full_sync"`
    SVGCacheTTL  time.Duration `yaml:"svg_cache_ttl"`
    FetchLockTTL time.Duration `yaml:"fetch_lock_ttl"`
    MaxWorkers   int           `yaml:"max_workers"`
}

type LogConfig struct {
    Level      string `yaml:"level"`
    File       string `yaml:"file"`
    MaxSize    int    `yaml:"max_size"`
    MaxBackups int    `yaml:"max_backups"`
    MaxAge     int    `yaml:"max_age"`
}

type RateLimitConfig struct {
    CardRPS int `yaml:"card_rps"`
    PRRPS   int `yaml:"pr_rps"`
}

func Load(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    cfg := &Config{}
    if err := yaml.Unmarshal(data, cfg); err != nil {
        return nil, err
    }
    // 默认值
    if cfg.Server.Port == 0 { cfg.Server.Port = 8080 }
    if cfg.Cron.MaxWorkers == 0 { cfg.Cron.MaxWorkers = 5 }
    if cfg.Redis.PoolSize == 0 { cfg.Redis.PoolSize = 20 }
    return cfg, nil
}
```

---

## 5. Redis 数据结构

### 5.1 键设计总览

| Key Pattern | 类型 | 用途 | TTL |
|-------------|------|------|-----|
| `users:all` | Set | 所有已注册用户名集合 | 持久 |
| `user:{username}` | Hash | 用户元信息 (last_update, status) | 持久 |
| `pr:{username}` | List | 用户 PR 列表 (JSON 序列化) | 持久 |
| `svg:{username}:{style}` | String | SVG 渲染缓存 | 24h |
| `lock:fetch:{username}` | String | 并发抓取锁 | 60s |
| `stats:visits:card` | String | 卡片访问计数 | 持久 |
| `stats:visits:pr` | String | 详情页访问计数 | 持久 |

### 5.2 数据结构详细说明

#### users:all — Set
```
SMEMBERS users:all
  → ["muzimu", "torvalds", "ruanyf"]
```
用途：Cron 全量同步时遍历。新用户首次访问时 `SADD`。

#### user:{username} — Hash
```
HGETALL user:muzimu
  last_update : "2026-06-25T03:00:00Z"    # ISO8601
  status      : "normal"                   # normal | fail
  fail_reason : ""                          # 失败原因 (仅 status=fail)
  fail_count  : "0"                        # 连续失败次数
```

#### pr:{username} — List
每条 PR 序列化为 JSON 存入 List：
```json
{
  "number": 42,
  "title": "Fix memory leak in connection pool",
  "repo": "gin-gonic/gin",
  "repo_stars": 80000,
  "state": "MERGED",
  "url": "https://github.com/gin-gonic/gin/pull/42",
  "created_at": "2025-06-15T10:30:00Z",
  "merged_at": "2025-06-16T02:00:00Z"
}
```

List 按 `created_at` 降序排列，方便模板渲染时取最新 N 条。

#### svg:{username}:{style} — String
```
SET svg:muzimu:dark "<svg>...</svg>" EX 86400
```
24h TTL 与 Cron 全量同步周期对齐，保证每日刷新后缓存自动失效。

#### lock:fetch:{username} — String
```
SET lock:fetch:muzimu "1" NX EX 60
```
防止并发请求重复触发同一用户抓取。

### 5.3 Redis 内存估算

单用户 PR 数据（假设 50 条 PR，每条 300B JSON）：~15KB  
1000 用户 PR 数据：~15MB  
SVG 缓存（1000用户 × 3 风格 × 2KB）：~6MB  
总计估算：< 30MB，适合 2GB 内存服务器。

---

## 6. GitHub GraphQL 抓取逻辑

> 参考 `/Library/workspace/pr-collector-py/main.py` 的实现

### 6.1 GraphQL 查询

```graphql
query($queryString: String!, $cursor: String) {
  search(query: $queryString, type: ISSUE, first: 100, after: $cursor) {
    issueCount
    pageInfo {
      hasNextPage
      endCursor
    }
    edges {
      node {
        ... on PullRequest {
          number
          title
          state
          url
          createdAt
          mergedAt
          repository {
            nameWithOwner
            stargazerCount
          }
        }
      }
    }
  }
  rateLimit {
    remaining
    resetAt
  }
}
```

查询字符串：
```
is:pr author:{username} archived:false is:public sort:created-desc
```

### 6.2 Go 客户端实现 (`github/client.go`)

```go
package github

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

const graphqlURL = "https://api.github.com/graphql"

type Client struct {
    token      string
    httpClient *http.Client
}

func NewClient(token string) *Client {
    return &Client{
        token:      token,
        httpClient: &http.Client{Timeout: 30 * time.Second},
    }
}

type PRInfo struct {
    Number     int       `json:"number"`
    Title      string    `json:"title"`
    State      string    `json:"state"`
    URL        string    `json:"url"`
    CreatedAt  string    `json:"created_at"`
    MergedAt   string    `json:"merged_at"`
    Repo       string    `json:"repo"`
    RepoStars  int       `json:"repo_stars"`
}

// FetchAllPRs 获取用户全部 PR，处理分页和限流
func (c *Client) FetchAllPRs(username string) ([]PRInfo, error) {
    const query = `...` // 上述 GraphQL 查询
    var allPRs []PRInfo
    cursor := ""

    for {
        variables := map[string]interface{}{
            "queryString": fmt.Sprintf(
                "is:pr author:%s archived:false is:public sort:created-desc",
                username,
            ),
        }
        if cursor != "" {
            variables["cursor"] = cursor
        }

        data, err := c.doRequest(query, variables)
        if err != nil {
            return nil, err
        }

        search := data["search"].(map[string]interface{})
        for _, edge := range search["edges"].([]interface{}) {
            node := edge.(map[string]interface{})["node"].(map[string]interface{})
            repo := node["repository"].(map[string]interface{})
            pr := PRInfo{
                Number:    int(node["number"].(float64)),
                Title:     node["title"].(string),
                State:     node["state"].(string),
                URL:       node["url"].(string),
                CreatedAt: node["createdAt"].(string),
                Repo:      repo["nameWithOwner"].(string),
                RepoStars: int(repo["stargazerCount"].(float64)),
            }
            if mergedAt, ok := node["mergedAt"]; ok && mergedAt != nil {
                pr.MergedAt = mergedAt.(string)
            }
            allPRs = append(allPRs, pr)
        }

        pageInfo := search["pageInfo"].(map[string]interface{})
        if !pageInfo["hasNextPage"].(bool) {
            break
        }
        cursor = pageInfo["endCursor"].(string)
    }

    return allPRs, nil
}
```

### 6.3 限流与重试策略

参考 Python 版本 `_graphql_request()` 的重试逻辑（`main.py:67-102`）：

| 场景 | 处理 |
|------|------|
| 200 + 无 errors | 正常返回 data |
| 200 + rate limit error | 解析 `rateLimit.resetAt`，计算等待时间，sleep 后重试 |
| 403 / 429 | 读取 `x-ratelimit-reset` 响应头计算等待时间 |
| 等待时间 > 60s | 放弃本次抓取，标记用户 fail |
| 连续 10 次重试失败 | 终止该用户抓取 |

Go 实现：
```go
const (
    maxWaitSeconds = 60
    maxRetries     = 10
)

func (c *Client) doRequest(query string, variables map[string]interface{}) (map[string]interface{}, error) {
    for attempt := 0; attempt < maxRetries; attempt++ {
        // ... HTTP 请求 ...
        
        // 限流判断
        if needWait {
            waitSeconds := calcWaitTime(resp)
            if waitSeconds > maxWaitSeconds {
                return nil, fmt.Errorf("wait time %ds exceeds max %ds", waitSeconds, maxWaitSeconds)
            }
            time.Sleep(time.Duration(waitSeconds+1) * time.Second)
            continue
        }
        // 成功返回
        return data, nil
    }
    return nil, fmt.Errorf("max retries (%d) reached", maxRetries)
}
```

---

## 7. HTTP 接口设计

### 7.1 GET /card — SVG 徽章接口

**入参：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| username | string | 是 | GitHub 用户名 |
| style | string | 否 | SVG 样式：default / dark / compact，默认 default |

**处理流程：**

```
GET /card?username=muzimu&style=dark
  │
  ├─ 1. 查 Redis svg:muzimu:dark
  │    └─ 命中 ──→ 返回 SVG + Cache-Control: public, max-age=86400 + ETag
  │
  ├─ 2. 查 Redis user:muzimu 是否存在
  │    ├─ 不存在 ──→ SADD users:all muzimu
  │    │            └─ 异步触发抓取 (加锁 lock:fetch:muzimu)
  │    │            └─ 返回占位 SVG (灰色背景)
  │    │
  │    └─ 存在，status=fail ──→ 返回占位 SVG (红色背景，提示抓取失败)
  │
  └─ 3. 存在，status=normal
       ├─ 读 pr:muzimu 列表
       ├─ 计算统计：总 PR 数、仓库数
       ├─ 渲染 SVG 模板
       ├─ SET svg:muzimu:dark <svg> EX 86400
       └─ 返回 SVG + 强缓存头
```

**响应头：**
```
Content-Type: image/svg+xml; charset=utf-8
Cache-Control: public, max-age=86400, immutable
ETag: "abc123..."
```

**处理逻辑伪代码 (`handler/card.go`)：**
```go
func (h *CardHandler) Handle(c *gin.Context) {
    username := c.Query("username")
    style := c.DefaultQuery("style", "default")

    // 1. SVG 缓存命中
    svg, err := h.redis.GetSVG(ctx, username, style)
    if err == nil && svg != "" {
        h.writeSVG(c, svg)
        return
    }

    // 2. 检查用户是否存在
    exists, status := h.redis.GetUserStatus(ctx, username)
    if !exists {
        h.redis.AddUser(ctx, username)
        go h.asyncFetch(username) // 异步抓取，不阻塞响应
        h.writePlaceholder(c, "loading")
        return
    }
    if status == "fail" {
        h.writePlaceholder(c, "error")
        return
    }

    // 3. 渲染 SVG
    prs := h.redis.GetPRList(ctx, username)
    svg = h.renderer.RenderSVG(style, username, prs)
    h.redis.SetSVG(ctx, username, style, svg)
    h.writeSVG(c, svg)
}
```

### 7.2 GET /pr — PR 详情页

**入参：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| username | string | 是 | GitHub 用户名 |

**处理流程：**
```
GET /pr?username=muzimu
  │
  ├─ 1. 查 Redis pr:muzimu
  │    └─ 无数据 ──→ 渲染空页面 (提示暂无 PR + 手动刷新按钮)
  │
  └─ 2. 有数据 ──→ 渲染 HTML (PR 列表表格)
       └─ 页面包含：总 PR 数、仓库数、每条 PR 详情 + GitHub 链接
       └─ 手动刷新按钮：POST /refresh?username=muzimu
```

### 7.3 POST /refresh — 手动刷新

**入参：** `username` (form/query)

**处理：** 异步触发该用户 PR 更新，返回 JSON `{"ok": true, "message": "刷新任务已提交"}`。

加锁逻辑与懒加载一致，防止并发重复抓取。

---

## 8. 定时任务设计

### 8.1 Cron 调度 (`cron/scheduler.go`)

```go
package cron

import (
    "github.com/robfig/cron/v3"
    "github.com/rs/zerolog"
)

type Scheduler struct {
    cron   *cron.Cron
    redis  *redis.Store
    github *github.Client
    log    zerolog.Logger
    maxWorkers int
}

func New(cfg config.CronConfig, r *redis.Store, g *github.Client, log zerolog.Logger) *Scheduler {
    return &Scheduler{
        cron:   cron.New(cron.WithSeconds()),
        redis:  r,
        github: g,
        log:    log,
        maxWorkers: cfg.MaxWorkers,
    }
}

func (s *Scheduler) Start() {
    // 每日全量同步
    s.cron.AddFunc("0 0 3 * * *", s.fullSync)   // 秒 分 时 日 月 周
    s.cron.Start()
    s.log.Info().Msg("cron scheduler started")
}
```

### 8.2 全量同步流程

```
fullSync()
  │
  ├─ 1. SMEMBERS users:all → usernames[]
  │
  ├─ 2. 使用信号量控制并发 (maxWorkers=5)
  │    └─ for each username:
  │         ├─ 获取锁 lock:fetch:{username}
  │         ├─ 调用 github.FetchAllPRs(username)
  │         ├─ 批量写入 pr:{username} (先 DEL 旧数据, 再 RPUSH 新数据)
  │         ├─ HSET user:{username} last_update now status normal
  │         ├─ DEL svg:{username}:* (清理所有 SVG 缓存)
  │         └─ 释放锁
  │
  └─ 3. 记录日志：成功 N 个，失败 M 个
```

**并发控制示例：**
```go
func (s *Scheduler) fullSync() {
    usernames, _ := s.redis.GetAllUsers(ctx)
    sem := make(chan struct{}, s.maxWorkers)
    var wg sync.WaitGroup

    for _, username := range usernames {
        wg.Add(1)
        go func(u string) {
            defer wg.Done()
            sem <- struct{}{}
            defer func() { <-sem }()

            if err := s.fetchAndStore(u); err != nil {
                s.log.Error().Err(err).Str("user", u).Msg("sync failed")
                s.redis.MarkUserFail(ctx, u, err.Error())
            }
        }(username)
    }
    wg.Wait()
}
```

### 8.3 懒加载异步抓取

接口触发的异步抓取（首次访问无数据用户）：

```go
func (s *Scheduler) AsyncFetch(username string) {
    go func() {
        // 尝试获取锁
        ok, _ := s.redis.TryLock(ctx, username)
        if !ok {
            return // 已有抓取进行中
        }
        defer s.redis.Unlock(ctx, username)

        s.log.Info().Str("user", username).Msg("lazy fetch started")
        if err := s.fetchAndStore(username); err != nil {
            s.log.Error().Err(err).Str("user", username).Msg("lazy fetch failed")
            s.redis.MarkUserFail(ctx, username, err.Error())
        }
    }()
}
```

---

## 9. SVG/HTML 模板设计

### 9.1 模板加载

使用 Go 1.16+ `embed` 将模板嵌入二进制：

```go
package svc

import (
    "embed"
    "html/template"
    "text/template" // SVG 使用 text/template，避免 HTML 转义
)

//go:embed tmpl/svg/*.svg
var svgFS embed.FS

//go:embed tmpl/html/*.html
var htmlFS embed.FS

type Renderer struct {
    svgs  map[string]*texttemplate.Template
    pages *htmltemplate.Template    // 详情页 (html/template 自动转义)
}
```

### 9.2 SVG 模板 (`tmpl/svg/default.svg`)

```svg
<svg xmlns="http://www.w3.org/2000/svg" width="400" height="120">
  <defs>
    <linearGradient id="bg" x1="0%" y1="0%" x2="100%" y2="0%">
      <stop offset="0%" style="stop-color:#2d3748"/>
      <stop offset="100%" style="stop-color:#4a5568"/>
    </linearGradient>
  </defs>

  <rect width="400" height="120" rx="12" fill="url(#bg)"/>

  <!-- PR 图标 -->
  <text x="30" y="55" font-size="28">🐙</text>

  <!-- 用户名 -->
  <text x="70" y="40" fill="#a0aec0" font-size="14" font-family="Arial,sans-serif">
    {{.Username}}'s Pull Requests
  </text>

  <!-- 统计数据 -->
  <text x="70" y="75" fill="#ffffff" font-size="28" font-weight="bold"
        font-family="Arial,sans-serif">
    {{.TotalPRs}} PRs
  </text>
  <text x="70" y="100" fill="#68d391" font-size="13" font-family="Arial,sans-serif">
    across {{.TotalRepos}} repositories
  </text>
</svg>
```

模板数据：
```go
type SVGBadgeData struct {
    Username   string
    TotalPRs   int
    TotalRepos int
    Style      string
}
```

### 9.3 HTML 详情页 (`tmpl/html/pr_list.html`)

```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Username}} - PR Contributions</title>
    <style>
        body { font-family: -apple-system, system-ui, sans-serif;
               max-width: 800px; margin: 40px auto; padding: 0 20px; }
        .pr-item { border: 1px solid #e2e8f0; border-radius: 8px;
                   padding: 16px; margin: 12px 0; }
        .pr-repo { color: #0366d6; font-weight: 600; }
        .pr-state { display: inline-block; padding: 2px 8px; border-radius: 12px;
                    font-size: 12px; }
        .pr-state-MERGED { background: #dcffe4; color: #22863a; }
        .pr-state-OPEN { background: #dbedff; color: #0366d6; }
        .pr-state-CLOSED { background: #ffeef0; color: #cb2431; }
    </style>
</head>
<body>
    <h1>🐙 {{.Username}}'s Pull Requests</h1>
    <p>{{.TotalPRs}} PRs across {{.TotalRepos}} repos</p>

    <form action="/refresh" method="post">
        <input type="hidden" name="username" value="{{.Username}}">
        <button type="submit">🔄 手动刷新</button>
    </form>

    {{range .PRs}}
    <div class="pr-item">
        <div class="pr-repo">{{.Repo}}</div>
        <a href="{{.URL}}" target="_blank">{{.Title}}</a>
        <span class="pr-state pr-state-{{.State}}">{{.State}}</span>
        <div style="color:#666; font-size:13px">
            Created: {{.CreatedAt}} {{if .MergedAt}} | Merged: {{.MergedAt}}{{end}}
        </div>
    </div>
    {{end}}
</body>
</html>
```

---

## 10. Caddy 配置

### 10.1 Caddyfile

```caddyfile
shturl.cc {
    # 自动 HTTPS
    tls admin@shturl.cc

    # /card 接口：强缓存 + 限流
    @card {
        path /card
    }
    route @card {
        header Cache-Control "public, max-age=86400, immutable"
        rate_limit {
            zone dynamic {
                key {remote_host}
                events 10
                window 1s
            }
        }
        reverse_proxy localhost:8080
    }

    # /pr 接口：限流 + 反向代理
    @pr {
        path /pr /refresh
    }
    route @pr {
        rate_limit {
            zone dynamic {
                key {remote_host}
                events 5
                window 1s
            }
        }
        reverse_proxy localhost:8080
    }

    # 默认
    reverse_proxy localhost:8080
}
```

---

## 11. systemd 部署

### 11.1 Unit 文件 (`deploy/pr-collector.service`)

```ini
[Unit]
Description=PR Collector - GitHub PR Badge Service
After=network.target redis.service

[Service]
Type=simple
User=www
Group=www
WorkingDirectory=/opt/pr-collector
ExecStart=/opt/pr-collector/pr-collector
Restart=always
RestartSec=5
StandardOutput=append:/opt/pr-collector/logs/stdout.log
StandardError=append:/opt/pr-collector/logs/stderr.log
LimitNOFILE=65536

# 安全加固
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/opt/pr-collector/logs

[Install]
WantedBy=multi-user.target
```

### 11.2 一键部署脚本 (`deploy/install.sh`)

```bash
#!/bin/bash
set -e

APP_DIR="/opt/pr-collector"
BINARY="pr-collector"

# 1. 编译
echo "[1/5] Building..."
CGO_ENABLED=0 go build -ldflags="-s -w" -o $BINARY .

# 2. 创建目录
echo "[2/5] Creating directories..."
sudo mkdir -p $APP_DIR/logs

# 3. 复制文件
echo "[3/5] Copying files..."
sudo cp $BINARY $APP_DIR/
sudo cp config.yaml $APP_DIR/
sudo cp deploy/Caddyfile $APP_DIR/

# 4. 安装 systemd 服务
echo "[4/5] Installing systemd service..."
sudo cp deploy/pr-collector.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable pr-collector

# 5. 启动
echo "[5/5] Starting service..."
sudo systemctl restart pr-collector
sudo systemctl status pr-collector

echo "Done! Check logs: journalctl -u pr-collector -f"
```

---

## 12. 稳定性 & 资源适配

### 12.1 Gin 生产模式

```go
// main.go
gin.SetMode(gin.ReleaseMode)
gin.DefaultWriter = io.Discard  // 关闭默认日志，统一走 zerolog
```

### 12.2 Redis 内存策略

```bash
# redis.conf
maxmemory 128mb
maxmemory-policy allkeys-lru   # LRU 淘汰，保证不 OOM
```

### 12.3 并发控制

| 场景 | 控制方式 | 限制 |
|------|---------|------|
| 全量同步 | 信号量 channel | max 5 goroutines |
| 懒加载抓取 | Redis 分布式锁 | 同用户 1 个 |
| HTTP 请求 | Caddy rate_limit | 10 req/s (/card) |

### 12.4 zerolog 初始化

```go
package main

import (
    "io"
    "os"
    "github.com/rs/zerolog"
    "gopkg.in/natefinch/lumberjack.v2"
)

func initLogger(cfg config.LogConfig) zerolog.Logger {
    fileWriter := &lumberjack.Logger{
        Filename:   cfg.File,
        MaxSize:    cfg.MaxSize,
        MaxBackups: cfg.MaxBackups,
        MaxAge:     cfg.MaxAge,
        Compress:   true,
    }

    // 同时输出到文件和标准输出（systemd 日志）
    multi := io.MultiWriter(os.Stdout, fileWriter)
    return zerolog.New(multi).With().Timestamp().Logger().Level(parseLevel(cfg.Level))
}
```

### 12.5 优雅关闭

```go
func main() {
    // ...
    srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Server.Port), Handler: router}

    go func() {
        if err := srv.ListenAndServe(); err != http.ErrServerClosed {
            log.Fatal().Err(err).Msg("server error")
        }
    }()

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    log.Info().Msg("shutting down...")
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    cronScheduler.Stop()
    redisClient.Close()
    srv.Shutdown(ctx)
    log.Info().Msg("server exited")
}
```

---

## 13. 开发路线图

### Phase 1 — 项目骨架 (Day 1)

- [x] 初始化 Go module：`go mod init pr-collector`
- [ ] 配置文件设计 + config 包
- [ ] zerolog 日志初始化
- [ ] main.go 骨架：加载配置、初始化日志
- [ ] .gitignore + config.example.yaml

### Phase 2 — GitHub 客户端 (Day 1-2)

- [ ] GraphQL 客户端封装（参考 `pr-collector-py/main.py`）
- [ ] 分页逻辑
- [ ] 限流重试
- [ ] 单元测试 (mock HTTP)

### Phase 3 — Redis 存储层 (Day 2)

- [ ] Redis 连接初始化
- [ ] 用户集操作
- [ ] PR 列表读写
- [ ] SVG 缓存读写
- [ ] 分布式锁
- [ ] 访问计数

### Phase 4 — 模板渲染 (Day 2-3)

- [ ] SVG 模板 (default/dark/compact)
- [ ] HTML 详情页模板
- [ ] embed 加载 + Renderer 封装

### Phase 5 — HTTP 接口 (Day 3)

- [ ] GET /card 处理器
- [ ] GET /pr 处理器
- [ ] POST /refresh 处理器
- [ ] 中间件：CORS、请求日志

### Phase 6 — 定时任务 (Day 3-4)

- [ ] Cron 注册
- [ ] 全量同步逻辑
- [ ] 懒加载异步抓取
- [ ] 并发控制

### Phase 7 — 部署 & 联调 (Day 4)

- [ ] Caddyfile 配置
- [ ] systemd unit 文件
- [ ] 一键部署脚本
- [ ] 端到端测试

### Phase 8 — 优化 & 可选功能 (Day 5+)

- [ ] 简单限流中间件
- [ ] 连续失败重试机制
- [ ] 错误页面友好提示
- [ ] 更多 SVG 主题
- [ ] 性能压测

---

## 附录

### A. .gitignore

```
# 二进制
pr-collector
*.exe

# 配置
config.yaml

# 日志
logs/

# IDE
.idea/
.vscode/
*.swp
*.swo

# Go
vendor/

# 临时文件
*.tmp
.DS_Store
```

### B. Go 依赖清单

```
github.com/gin-gonic/gin          # Web 框架
github.com/redis/go-redis/v9      # Redis 客户端
github.com/robfig/cron/v3         # Cron 调度
github.com/rs/zerolog             # 结构化日志
gopkg.in/yaml.v3                  # YAML 解析
gopkg.in/natefinch/lumberjack.v2  # 日志轮转
```

### C. 关键决策记录

| 决策 | 理由 |
|------|------|
| Redis 而非 MySQL | 数据结构简单（Set/Hash/List/String），无需复杂查询；内存快，适合高频读取 |
| GraphQL 而非 REST | 批量获取 PR 信息，减少 API 调用次数；REST 需要多次请求不同端点 |
| text/template 渲染 SVG | 标准库零依赖，SVG 不需要 HTML 转义（用 text/template，非 html/template） |
| embed 而非外部文件 | 部署时无需附带模板文件，单二进制部署 |
| Caddy 而非 Nginx | 自动 HTTPS 零配置；Caddyfile 简洁；内存占用低 |
