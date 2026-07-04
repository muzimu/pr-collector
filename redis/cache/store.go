package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"pr-collector/github"
)

// Store Redis 存储层，封装 PR 数据、SVG 缓存、用户集合、分布式锁操作
type Store struct {
	rdb *redis.Client
	log zerolog.Logger
}

const (
	// 全局用户集合
	usersSetKey = "users:all"

	// 用户元信息
	userKeyPrefix       = "user:%s"
	userFieldLastUpdate = "last_update"
	userFieldStatus     = "status"
	userFieldFailReason = "fail_reason"
	userFieldFailCount  = "fail_count"

	// PR 列表
	prListKeyPrefix   = "pr:%s"
	emptyPRListMarker = "__EMPTY__"

	// SVG 缓存
	svgKeyPrefix         = "svg:%s:%s"
	svgBySuffixKeyPrefix = "svg:%s"

	// 分布式锁
	lockKeyPrefix = "lock:fetch:%s"

	// 访问统计
	statsCardVisitsKey = "stats:card_visits"
	statsPRVisitsKey   = "stats:pr_visits"

	// 排行榜
	leaderboardCacheKey        = "leaderboard:cache"
	leaderboardMetaKey         = "leaderboard:meta"
	leaderboardUserKeyPrefix   = "leaderboard:user:%s"
	leaderboardUserKeyBase     = "leaderboard:user:"
	leaderboardFieldData       = "data"
	leaderboardFieldPRCount    = "pr_count"
	leaderboardFieldRepoCount  = "repo_count"
	leaderboardFieldTotalUsers = "total_users"
	leaderboardFieldTotalPRs   = "total_prs"
	leaderboardFieldTotalRepos = "total_repos"
	leaderboardFieldUpdatedAt  = "updated_at"

	leaderboardUpsertScript = `
local username = KEYS[1]
local score = tonumber(ARGV[1])
local user_json = ARGV[2]
local new_pr_count = tonumber(ARGV[3])
local new_repo_count = tonumber(ARGV[4])
local updated_at = ARGV[5]

local user_key = "%s" .. username
local old = redis.call("HGETALL", user_key)
local old_pr_count = 0
local old_repo_count = 0
for i = 1, #old, 2 do
	if old[i] == "%s" then old_pr_count = tonumber(old[i+1]) or 0 end
	if old[i] == "%s" then old_repo_count = tonumber(old[i+1]) or 0 end
end

redis.call("HSET", user_key,
	"%s", user_json,
	"%s", new_pr_count,
	"%s", new_repo_count)
redis.call("ZADD", "%s", score, username)

local total_users = redis.call("ZCARD", "%s")
local meta = redis.call("HGETALL", "%s")
local total_prs = 0
local total_repos = 0
for i = 1, #meta, 2 do
	if meta[i] == "%s" then total_prs = tonumber(meta[i+1]) or 0 end
	if meta[i] == "%s" then total_repos = tonumber(meta[i+1]) or 0 end
end

total_prs = total_prs - old_pr_count + new_pr_count
total_repos = total_repos - old_repo_count + new_repo_count
redis.call("HSET", "%s",
	"%s", total_users,
	"%s", total_prs,
	"%s", total_repos,
	"%s", updated_at)

return 1
`
)

// LeaderboardUser 排行榜单用户条目
type LeaderboardUser struct {
	Rank      int     `json:"rank"`
	Username  string  `json:"username"`
	Score     float64 `json:"score"`
	PRCount   int     `json:"pr_count"`
	RepoCount int     `json:"repo_count"`
}

// LeaderboardMeta 排行榜元信息
type LeaderboardMeta struct {
	UpdatedAt  string `json:"updated_at"`
	TotalUsers int    `json:"total_users"`
	TotalPRs   int    `json:"total_prs"`
	TotalRepos int    `json:"total_repos"`
}

// NewStore 创建 Redis 存储实例
func NewStore(rdb *redis.Client, log zerolog.Logger) *Store {
	return &Store{rdb: rdb, log: log}
}

// ── 用户集合 ──────────────────────────────────────────────

// AddUser 将用户名加入全局用户集合
func (s *Store) AddUser(ctx context.Context, username string) error {
	return s.rdb.SAdd(ctx, usersSetKey, username).Err()
}

// GetAllUsers 获取所有已注册用户名
func (s *Store) GetAllUsers(ctx context.Context) ([]string, error) {
	return s.rdb.SMembers(ctx, usersSetKey).Result()
}

// UserExists 检查用户是否存在于集合中
func (s *Store) UserExists(ctx context.Context, username string) (bool, error) {
	return s.rdb.SIsMember(ctx, usersSetKey, username).Result()
}

// ── 用户元信息 ────────────────────────────────────────────

// UserMeta 用户元信息
type UserMeta struct {
	LastUpdate string `json:"last_update"`
	Status     string `json:"status"` // normal / fail
	FailReason string `json:"fail_reason"`
	FailCount  int    `json:"fail_count"`
}

func userKey(username string) string { return fmt.Sprintf(userKeyPrefix, username) }

// SetUserStatus 设置用户状态
func (s *Store) SetUserStatus(ctx context.Context, username, status, reason string) error {
	fields := map[string]any{
		userFieldLastUpdate: time.Now().UTC().Format(time.RFC3339),
		userFieldStatus:     status,
		userFieldFailReason: reason,
	}
	if status == "fail" {
		// 原子递增失败计数
		s.rdb.HIncrBy(ctx, userKey(username), userFieldFailCount, 1)
	} else {
		fields[userFieldFailCount] = 0
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
		LastUpdate: data[userFieldLastUpdate],
		Status:     data[userFieldStatus],
		FailReason: data[userFieldFailReason],
	}, nil
}

// ── PR 列表 ───────────────────────────────────────────────

func prListKey(username string) string { return fmt.Sprintf(prListKeyPrefix, username) }

// SetPRList 全量替换用户 PR 列表（先删后写，事务）
// 若 prs 为空则写入哨兵，以区分"缓存不存在"与"已缓存但无 PR"
func (s *Store) SetPRList(ctx context.Context, username string, prs []github.PRInfo) error {
	pipe := s.rdb.Pipeline()
	key := prListKey(username)
	pipe.Del(ctx, key)

	if len(prs) > 0 {
		values := make([]any, len(prs))
		for i, pr := range prs {
			data, err := json.Marshal(pr)
			if err != nil {
				return fmt.Errorf("marshal pr: %w", err)
			}
			values[i] = string(data)
		}
		pipe.RPush(ctx, key, values...)
	} else {
		pipe.RPush(ctx, key, emptyPRListMarker)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// GetPRList 获取用户全部 PR 列表（JSON 反序列化）
func (s *Store) GetPRList(ctx context.Context, username string) ([]github.PRInfo, error) {
	data, err := s.rdb.LRange(ctx, prListKey(username), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, redis.Nil
	}
	if len(data) == 1 && data[0] == emptyPRListMarker {
		return []github.PRInfo{}, nil
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
	return fmt.Sprintf(svgKeyPrefix, username, style)
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
	return s.rdb.Set(ctx, fmt.Sprintf(svgBySuffixKeyPrefix, suffix), svg, ttl).Err()
}

// GetSVGBySuffix 读取 SVG 缓存，key = "svg:" + suffix
func (s *Store) GetSVGBySuffix(ctx context.Context, suffix string) (string, error) {
	return s.rdb.Get(ctx, fmt.Sprintf(svgBySuffixKeyPrefix, suffix)).Result()
}

// ClearUserSVG 清理指定用户所有风格 SVG 缓存
func (s *Store) ClearUserSVG(ctx context.Context, username string) error {
	iter := s.rdb.Scan(ctx, 0, fmt.Sprintf(svgKeyPrefix, username, "*"), 0).Iterator()
	for iter.Next(ctx) {
		s.rdb.Del(ctx, iter.Val())
	}
	return iter.Err()
}

// ── 分布式锁 ──────────────────────────────────────────────

func lockKey(username string) string {
	return fmt.Sprintf(lockKeyPrefix, username)
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
	return s.rdb.Incr(ctx, statsCardVisitsKey).Result()
}

// IncrPRVisits 递增详情页访问计数
func (s *Store) IncrPRVisits(ctx context.Context, username string) (int64, error) {
	return s.rdb.Incr(ctx, statsPRVisitsKey).Result()
}

// ── 排行榜缓存 ──────────────────────────────────────────────

func leaderboardUserKey(username string) string {
	return fmt.Sprintf(leaderboardUserKeyPrefix, username)
}

// SetLeaderboardCache 覆盖写入排行榜：ZSET 按 score 排序，member 为 username；
// 用户详情写入独立 hash，便于按用户名精确增量更新。
func (s *Store) SetLeaderboardCache(ctx context.Context, users []LeaderboardUser) error {
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, leaderboardCacheKey)
	for _, u := range users {
		data, err := json.Marshal(u)
		if err != nil {
			return fmt.Errorf("marshal leaderboard user: %w", err)
		}
		pipe.HSet(ctx, leaderboardUserKey(u.Username),
			leaderboardFieldData, string(data),
			leaderboardFieldPRCount, u.PRCount,
			leaderboardFieldRepoCount, u.RepoCount,
		)
		pipe.ZAdd(ctx, leaderboardCacheKey, redis.Z{Score: u.Score, Member: u.Username})
	}
	_, err := pipe.Exec(ctx)
	return err
}

// UpsertLeaderboardUser 原子地增量更新单个用户到排行榜：
// 覆盖/新增用户详情与 ZSET 分数，并基于旧计数增量更新 meta。
func (s *Store) UpsertLeaderboardUser(ctx context.Context, u LeaderboardUser) error {
	data, err := json.Marshal(u)
	if err != nil {
		return fmt.Errorf("marshal leaderboard user: %w", err)
	}

	script := redis.NewScript(fmt.Sprintf(leaderboardUpsertScript,
		leaderboardUserKeyBase,
		leaderboardFieldPRCount,
		leaderboardFieldRepoCount,
		leaderboardFieldData,
		leaderboardFieldPRCount,
		leaderboardFieldRepoCount,
		leaderboardCacheKey,
		leaderboardCacheKey,
		leaderboardMetaKey,
		leaderboardFieldTotalPRs,
		leaderboardFieldTotalRepos,
		leaderboardMetaKey,
		leaderboardFieldTotalUsers,
		leaderboardFieldTotalPRs,
		leaderboardFieldTotalRepos,
		leaderboardFieldUpdatedAt,
	))

	_, err = script.Run(ctx, s.rdb, []string{u.Username},
		u.Score, string(data), u.PRCount, u.RepoCount,
		time.Now().UTC().Format(time.RFC3339),
	).Result()
	return err
}

// SetLeaderboardMeta 写入排行榜元信息
func (s *Store) SetLeaderboardMeta(ctx context.Context, meta LeaderboardMeta) error {
	data := map[string]any{
		leaderboardFieldUpdatedAt:  meta.UpdatedAt,
		leaderboardFieldTotalUsers: meta.TotalUsers,
		leaderboardFieldTotalPRs:   meta.TotalPRs,
		leaderboardFieldTotalRepos: meta.TotalRepos,
	}
	return s.rdb.HSet(ctx, leaderboardMetaKey, data).Err()
}

// GetLeaderboardRange 从 ZSET 读取排名区间（按分数降序），再通过 pipeline 取用户详情
func (s *Store) GetLeaderboardRange(ctx context.Context, offset, limit int) ([]LeaderboardUser, error) {
	usernames, err := s.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:   leaderboardCacheKey,
		Start: int64(offset),
		Stop:  int64(offset + limit - 1),
		Rev:   true,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(usernames) == 0 {
		return []LeaderboardUser{}, nil
	}

	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.StringCmd, len(usernames))
	for i, username := range usernames {
		cmds[i] = pipe.HGet(ctx, leaderboardUserKey(username), leaderboardFieldData)
	}
	// pipeline 中单个 HGet 的 redis.Nil 不应导致整体失败，
	// 逐个 cmd.Result() 处理即可。
	_, _ = pipe.Exec(ctx)

	users := make([]LeaderboardUser, 0, len(usernames))
	for i, cmd := range cmds {
		data, err := cmd.Result()
		if err != nil {
			s.log.Warn().Err(err).Str("user", usernames[i]).Msg("leaderboard user data missing")
			continue
		}
		var u LeaderboardUser
		if err := json.Unmarshal([]byte(data), &u); err != nil {
			s.log.Warn().Err(err).Str("user", usernames[i]).Msg("unmarshal leaderboard user failed")
			continue
		}
		users = append(users, u)
	}
	return users, nil
}

// GetLeaderboardCount 获取排行榜总人数
func (s *Store) GetLeaderboardCount(ctx context.Context) (int64, error) {
	return s.rdb.ZCard(ctx, leaderboardCacheKey).Result()
}

// GetLeaderboardMeta 读取排行榜元信息
func (s *Store) GetLeaderboardMeta(ctx context.Context) (LeaderboardMeta, error) {
	data, err := s.rdb.HGetAll(ctx, leaderboardMetaKey).Result()
	if err != nil {
		return LeaderboardMeta{}, err
	}
	if len(data) == 0 {
		return LeaderboardMeta{}, nil
	}

	meta := LeaderboardMeta{}
	meta.UpdatedAt = data[leaderboardFieldUpdatedAt]
	meta.TotalUsers, _ = strconv.Atoi(data[leaderboardFieldTotalUsers])
	meta.TotalPRs, _ = strconv.Atoi(data[leaderboardFieldTotalPRs])
	meta.TotalRepos, _ = strconv.Atoi(data[leaderboardFieldTotalRepos])
	return meta, nil
}
