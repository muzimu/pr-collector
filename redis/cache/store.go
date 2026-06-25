package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"pr-collector/github"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// Store Redis 存储层，封装 PR 数据、SVG 缓存、用户集合、分布式锁操作
type Store struct {
	rdb *redis.Client
	log zerolog.Logger
}

// NewStore 创建 Redis 存储实例
func NewStore(rdb *redis.Client, log zerolog.Logger) *Store {
	return &Store{rdb: rdb, log: log}
}

// ── 用户集合 ──────────────────────────────────────────────

// AddUser 将用户名加入全局用户集合
func (s *Store) AddUser(ctx context.Context, username string) error {
	return s.rdb.SAdd(ctx, "users:all", username).Err()
}

// GetAllUsers 获取所有已注册用户名
func (s *Store) GetAllUsers(ctx context.Context) ([]string, error) {
	return s.rdb.SMembers(ctx, "users:all").Result()
}

// UserExists 检查用户是否存在于集合中
func (s *Store) UserExists(ctx context.Context, username string) (bool, error) {
	return s.rdb.SIsMember(ctx, "users:all", username).Result()
}

// ── 用户元信息 ────────────────────────────────────────────

// UserMeta 用户元信息
type UserMeta struct {
	LastUpdate string `json:"last_update"`
	Status     string `json:"status"` // normal / fail
	FailReason string `json:"fail_reason"`
	FailCount  int    `json:"fail_count"`
}

func userKey(username string) string { return fmt.Sprintf("user:%s", username) }

// SetUserStatus 设置用户状态
func (s *Store) SetUserStatus(ctx context.Context, username, status, reason string) error {
	fields := map[string]interface{}{
		"last_update": time.Now().UTC().Format(time.RFC3339),
		"status":      status,
		"fail_reason": reason,
	}
	if status == "fail" {
		// 原子递增失败计数
		s.rdb.HIncrBy(ctx, userKey(username), "fail_count", 1)
	} else {
		fields["fail_count"] = 0
	}
	return s.rdb.HSet(ctx, userKey(username), fields).Err()
}

// GetUserMeta 获取用户元信息
func (s *Store) GetUserMeta(ctx context.Context, username string) (*UserMeta, error) {
	data, err := s.rdb.HGetAll(ctx, userKey(username)).Result()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, redis.Nil
	}
	return &UserMeta{
		LastUpdate: data["last_update"],
		Status:     data["status"],
		FailReason: data["fail_reason"],
	}, nil
}

// ── PR 列表 ───────────────────────────────────────────────

func prListKey(username string) string { return fmt.Sprintf("pr:%s", username) }

// SetPRList 全量替换用户 PR 列表（先删后写，事务）
func (s *Store) SetPRList(ctx context.Context, username string, prs []github.PRInfo) error {
	pipe := s.rdb.Pipeline()
	key := prListKey(username)
	pipe.Del(ctx, key)

	if len(prs) > 0 {
		values := make([]interface{}, len(prs))
		for i, pr := range prs {
			data, err := json.Marshal(pr)
			if err != nil {
				return fmt.Errorf("marshal pr: %w", err)
			}
			values[i] = string(data)
		}
		pipe.RPush(ctx, key, values...)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// GetPRList 获取用户全部 PR 列表（JSON 反序列化）
func (s *Store) GetPRList(ctx context.Context, username string) ([]github.PRInfo, error) {
	data, err := s.rdb.LRange(ctx, prListKey(username), 0, -1).Result()
	if err != nil || len(data) == 0 {
		return nil, redis.Nil
	}

	prs := make([]github.PRInfo, len(data))
	for i, item := range data {
		if err := json.Unmarshal([]byte(item), &prs[i]); err != nil {
			return nil, fmt.Errorf("unmarshal pr: %w", err)
		}
	}
	return prs, nil
}

// ── SVG 缓存 ──────────────────────────────────────────────

func svgCacheKey(username, style string) string {
	return fmt.Sprintf("svg:%s:%s", username, style)
}

// SetSVG 写入 SVG 渲染缓存，TTL 默认 24h
func (s *Store) SetSVG(ctx context.Context, username, style, svg string, ttl time.Duration) error {
	return s.rdb.Set(ctx, svgCacheKey(username, style), svg, ttl).Err()
}

// GetSVG 读取 SVG 缓存
func (s *Store) GetSVG(ctx context.Context, username, style string) (string, error) {
	return s.rdb.Get(ctx, svgCacheKey(username, style)).Result()
}

// SetSVGBySuffix 写入 SVG 缓存，key = "svg:" + suffix（suffix 由调用方自由组合）
func (s *Store) SetSVGBySuffix(ctx context.Context, suffix, svg string, ttl time.Duration) error {
	return s.rdb.Set(ctx, "svg:"+suffix, svg, ttl).Err()
}

// GetSVGBySuffix 读取 SVG 缓存，key = "svg:" + suffix
func (s *Store) GetSVGBySuffix(ctx context.Context, suffix string) (string, error) {
	return s.rdb.Get(ctx, "svg:"+suffix).Result()
}

// ClearUserSVG 清理指定用户所有风格 SVG 缓存
func (s *Store) ClearUserSVG(ctx context.Context, username string) error {
	iter := s.rdb.Scan(ctx, 0, fmt.Sprintf("svg:%s:*", username), 0).Iterator()
	for iter.Next(ctx) {
		s.rdb.Del(ctx, iter.Val())
	}
	return iter.Err()
}

// ── 分布式锁 ──────────────────────────────────────────────

func lockKey(username string) string {
	return fmt.Sprintf("lock:fetch:%s", username)
}

// TryLock 尝试获取用户抓取锁
func (s *Store) TryLock(ctx context.Context, username string, ttl time.Duration) (bool, error) {
	return s.rdb.SetNX(ctx, lockKey(username), "1", ttl).Result()
}

// Unlock 释放用户抓取锁
func (s *Store) Unlock(ctx context.Context, username string) error {
	return s.rdb.Del(ctx, lockKey(username)).Err()
}

// ── 访问计数 ──────────────────────────────────────────────

// IncrCardVisits 递增 SVG 卡片访问计数
func (s *Store) IncrCardVisits(ctx context.Context, username string) (int64, error) {
	return s.rdb.Incr(ctx, "stats:card_visits").Result()
}

// IncrPRVisits 递增详情页访问计数
func (s *Store) IncrPRVisits(ctx context.Context, username string) (int64, error) {
	return s.rdb.Incr(ctx, "stats:pr_visits").Result()
}
