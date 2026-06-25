package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"

	"pr-collector/config"
	"pr-collector/cron"
	"pr-collector/github"
	"pr-collector/handler"
	"pr-collector/middleware"
	"pr-collector/redis/cache"
	"pr-collector/svc"
)

// GitHub 用户名基础字符集：字母数字连字符，1-39 字符
var githubUsernameRE = regexp.MustCompile(`^[a-zA-Z\d][a-zA-Z\d-]{0,38}$`)

func main() {
	// ── 加载配置 ──────────────────────────────────
	cfgPath := "config.yaml"
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		cfgPath = p
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// ── 初始化日志 ────────────────────────────────
	log := initLogger(cfg.Log)
	log.Info().Str("config", cfgPath).Msg("config loaded")

	// ── Gin 生产模式 ──────────────────────────────
	gin.SetMode(cfg.Server.Mode)
	if cfg.Server.Mode == "release" {
		gin.DefaultWriter = os.Stderr
	}

	// ── 初始化 Redis ─────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})
	{
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := rdb.Ping(pingCtx).Err(); err != nil {
			pingCancel()
			log.Fatal().Err(err).Msg("redis connection failed")
		}
		pingCancel()
	}
	log.Info().Str("addr", cfg.Redis.Addr).Msg("redis connected")

	// ── 初始化各层 ───────────────────────────────
	store := cache.NewStore(rdb, log)

	ghClient := github.NewClient(cfg.GitHub.Token, log)

	// 应用级 context：优雅关闭时 cancel，终止所有进行中的抓取
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	fetcher := svc.NewFetcherWithContext(appCtx, store, ghClient, cfg.Cron.FetchLockTTL, log)

	renderer, err := svc.NewRenderer()
	if err != nil {
		log.Fatal().Err(err).Msg("renderer init failed")
	}

	// ── 初始化 Cron ──────────────────────────────
	scheduler := cron.NewScheduler(
		cfg.Cron.FullSync,
		fetcher,
		cfg.Cron.MaxWorkers,
		log,
	)
	scheduler.Start()

	// ── 注册路由 ─────────────────────────────────
	router := gin.New()
	router.Use(gin.Recovery())

	// 限流中间件
	cardLimiter := middleware.NewRateLimiter(cfg.RateLimit.CardRPS, log)
	prLimiter := middleware.NewRateLimiter(cfg.RateLimit.PRRPS, log)

	// 处理器
	cardHandler := handler.NewCardHandler(store, renderer, fetcher, cfg.Cron.SVGCacheTTL, log)
	prHandler := handler.NewPRHandler(store, renderer, fetcher, log)

	// HTML 模板
	router.SetHTMLTemplate(renderer.HTMLTemplate())

	// 路由
	router.GET("/card",
		cardLimiter.Handler(func(c *gin.Context) string { return c.ClientIP() }),
		usernameValidate,
		cardHandler.Handle,
	)

	prGroup := router.Group("/pr")
	prGroup.Use(prLimiter.Handler(func(c *gin.Context) string { return c.ClientIP() }))
	prGroup.Use(usernameValidate)
	{
		prGroup.GET("", prHandler.HandlePRPage)
	}
	router.POST("/refresh",
		prLimiter.Handler(func(c *gin.Context) string { return c.ClientIP() }),
		usernameValidate,
		prHandler.HandleRefresh,
	)

	// 健康检查
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// ── 启动 HTTP 服务 ────────────────────────────
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second, // 需大于 /card 同步抓取超时(20s)
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info().Int("port", cfg.Server.Port).Msg("server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	// ── 优雅关闭 ─────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info().Str("signal", sig.String()).Msg("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	// 1. 停止 cron
	scheduler.Stop()
	// 2. 取消应用 context → 终止进行中的抓取
	appCancel()
	// 3. 等待抓取 workers 退出
	fetcher.Shutdown(5 * time.Second)
	// 4. 关闭 HTTP 服务
	srv.Shutdown(shutdownCtx)
	// 5. 关闭 Redis
	rdb.Close()
	log.Info().Msg("server exited")
}

// usernameValidate 校验 GitHub 用户名合法性
// RE2 不支持前瞻断言，边界条件由代码检查：不能以连字符结尾、不能有连续连字符
func usernameValidate(c *gin.Context) {
	username := c.Query("username")
	if username == "" {
		username = c.PostForm("username")
	}
	if username == "" {
		return // 由各 handler 自行处理空参数
	}
	if !githubUsernameRE.MatchString(username) ||
		strings.HasSuffix(username, "-") ||
		strings.Contains(username, "--") {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "invalid github username",
		})
		return
	}
	c.Next()
}

// initLogger 初始化 zerolog：同时输出文件和控制台
func initLogger(cfg config.LogConfig) zerolog.Logger {
	fileWriter := &lumberjack.Logger{
		Filename:   cfg.File,
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   true,
	}

	multi := zerolog.MultiLevelWriter(os.Stdout, fileWriter)

	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}

	return zerolog.New(multi).With().Timestamp().Logger().Level(level)
}
