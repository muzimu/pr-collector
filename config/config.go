package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 全局配置结构体
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	GitHub    GitHubConfig    `yaml:"github"`
	Redis     RedisConfig     `yaml:"redis"`
	Cron      CronConfig      `yaml:"cron"`
	Log       LogConfig       `yaml:"log"`
	RateLimit RateLimitConfig `yaml:"ratelimit"`
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"`
}

// GitHubConfig GitHub API 配置
type GitHubConfig struct {
	Token string `yaml:"token"`
}

// RedisConfig Redis 连接配置
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	PoolSize int    `yaml:"pool_size"`
}

// CronConfig 定时任务配置
type CronConfig struct {
	FullSync        string        `yaml:"full_sync"`
	LeaderboardSync string        `yaml:"leaderboard_sync"`
	SVGCacheTTL     time.Duration `yaml:"svg_cache_ttl"`
	FetchLockTTL    time.Duration `yaml:"fetch_lock_ttl"`
	MaxWorkers      int           `yaml:"max_workers"`
	// LeaderboardMaxRaw 对数压缩公式中的 raw 上限，raw 等于该值时 score=100
	LeaderboardMaxRaw int `yaml:"leaderboard_max_raw"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level      string `yaml:"level"`
	File       string `yaml:"file"`
	MaxSize    int    `yaml:"max_size"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAge     int    `yaml:"max_age"`
}

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	CardRPS int `yaml:"card_rps"`
	PRRPS   int `yaml:"pr_rps"`
}

// Load 从 YAML 文件加载配置并填充默认值
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// 填充默认值
	cfg.applyDefaults()

	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.Mode == "" {
		c.Server.Mode = "release"
	}
	if c.Redis.PoolSize == 0 {
		c.Redis.PoolSize = 20
	}
	if c.Cron.MaxWorkers == 0 {
		c.Cron.MaxWorkers = 5
	}
	if c.Cron.FullSync == "" {
		c.Cron.FullSync = "0 0 3 * * *"
	}
	if c.Cron.LeaderboardSync == "" {
		c.Cron.LeaderboardSync = "0 0 4 * * *"
	}
	if c.Cron.LeaderboardMaxRaw == 0 {
		c.Cron.LeaderboardMaxRaw = 100000
	}
	if c.Cron.SVGCacheTTL == 0 {
		c.Cron.SVGCacheTTL = 24 * time.Hour
	}
	if c.Cron.FetchLockTTL == 0 {
		c.Cron.FetchLockTTL = 60 * time.Second
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.File == "" {
		c.Log.File = "logs/app.log"
	}
	if c.RateLimit.CardRPS == 0 {
		c.RateLimit.CardRPS = 10
	}
	if c.RateLimit.PRRPS == 0 {
		c.RateLimit.PRRPS = 5
	}
}
