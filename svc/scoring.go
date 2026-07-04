package svc

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"pr-collector/github"
	"pr-collector/redis/cache"
)

// ScoringService 评分计算服务
type ScoringService struct {
	store     *cache.Store
	log       zerolog.Logger
	maxRaw    float64
	refreshMu sync.Mutex
}

// NewScoringService 创建评分服务
func NewScoringService(store *cache.Store, maxRaw int, log zerolog.Logger) *ScoringService {
	if maxRaw <= 0 {
		maxRaw = 100000
	}
	return &ScoringService{
		store:  store,
		log:    log,
		maxRaw: float64(maxRaw),
	}
}

// rawScore 单用户原始分
type rawScore struct {
	username  string
	raw       float64
	prCount   int
	repoCount int
}

// CalculateScore 将对数压缩 raw 分映射到 [50, 100]
// score = 50 + 50 * log2(raw + 1) / log2(maxRaw + 1)
func (s *ScoringService) CalculateScore(raw float64) float64 {
	if raw <= 0 {
		return 50.0
	}
	if raw >= s.maxRaw {
		return 100.0
	}
	score := 50.0 + 50.0*math.Log2(raw+1)/math.Log2(s.maxRaw+1)
	return math.Round(score*10) / 10
}

// calculateRawScore 计算单个用户的原始分
func (s *ScoringService) calculateRawScore(prs []github.PRInfo) rawScore {
	// 注意：传入的 prs 应当已经是 merged 过滤后的结果
	repoMap := make(map[string]struct {
		stars   int
		prCount int
	})
	for _, pr := range prs {
		r := repoMap[pr.Repo]
		r.stars = pr.RepoStars
		r.prCount++
		repoMap[pr.Repo] = r
	}

	raw := 0.0
	for _, r := range repoMap {
		raw += float64(r.prCount) * math.Log2(float64(r.stars)+1)
	}

	return rawScore{
		raw:       raw,
		prCount:   len(prs),
		repoCount: len(repoMap),
	}
}

// CalculateAllScores 计算所有用户原始分
func (s *ScoringService) CalculateAllScores(ctx context.Context) ([]rawScore, error) {
	usernames, err := s.store.GetAllUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all users: %w", err)
	}

	scores := make([]rawScore, 0, len(usernames))
	for _, username := range usernames {
		prs, err := s.store.GetPRList(ctx, username)
		if err != nil {
			if err == redis.Nil {
				// 用户无 PR 数据：按 0 PR 处理，仍可入榜（50 分）
				scores = append(scores, rawScore{username: username})
				continue
			}
			s.log.Warn().Err(err).Str("user", username).Msg("skip user: get pr list failed")
			continue
		}

		merged := filterMerged(prs)
		score := s.calculateRawScore(merged)
		score.username = username
		if score.prCount == 0 {
			// 有缓存但无 merged PR，仍保留在榜中（50 分）
		}
		scores = append(scores, score)
	}

	return scores, nil
}

// CalculateUserScore 计算指定用户的排行榜条目
func (s *ScoringService) CalculateUserScore(ctx context.Context, username string) (cache.LeaderboardUser, error) {
	prs, err := s.store.GetPRList(ctx, username)
	if err != nil {
		if err == redis.Nil {
			// 缓存不存在：无法计算，应等待抓取完成后再调用
			return cache.LeaderboardUser{}, fmt.Errorf("pr list not cached for %s", username)
		}
		return cache.LeaderboardUser{}, fmt.Errorf("get pr list: %w", err)
	}

	merged := filterMerged(prs)
	raw := s.calculateRawScore(merged)
	return cache.LeaderboardUser{
		Username:  username,
		Score:     s.CalculateScore(raw.raw),
		PRCount:   raw.prCount,
		RepoCount: raw.repoCount,
	}, nil
}

// RefreshLeaderboardCache 刷新排行榜缓存（全量）
func (s *ScoringService) RefreshLeaderboardCache(ctx context.Context) error {
	s.log.Info().Msg("leaderboard refresh started")

	raw, err := s.CalculateAllScores(ctx)
	if err != nil {
		return fmt.Errorf("calculate scores: %w", err)
	}

	users := make([]cache.LeaderboardUser, len(raw))
	totalPRs := 0
	totalRepos := 0
	for i, r := range raw {
		users[i] = cache.LeaderboardUser{
			Username:  r.username,
			Score:     s.CalculateScore(r.raw),
			PRCount:   r.prCount,
			RepoCount: r.repoCount,
		}
		totalPRs += r.prCount
		totalRepos += r.repoCount
	}

	if err := s.store.SetLeaderboardCache(ctx, users); err != nil {
		return fmt.Errorf("set leaderboard cache: %w", err)
	}

	meta := cache.LeaderboardMeta{
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		TotalUsers: len(users),
		TotalPRs:   totalPRs,
		TotalRepos: totalRepos,
	}
	if err := s.store.SetLeaderboardMeta(ctx, meta); err != nil {
		return fmt.Errorf("set leaderboard meta: %w", err)
	}

	s.log.Info().
		Int("users", len(users)).
		Int("total_prs", totalPRs).
		Int("total_repos", totalRepos).
		Msg("leaderboard refresh completed")

	return nil
}

// RefreshLeaderboardCacheAsync 异步刷新排行榜缓存（全量）
// 如果已有刷新任务在执行，则跳过本次调用
func (s *ScoringService) RefreshLeaderboardCacheAsync() {
	go func() {
		if !s.refreshMu.TryLock() {
			s.log.Debug().Msg("leaderboard async refresh skipped: already running")
			return
		}
		defer s.refreshMu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		if err := s.RefreshLeaderboardCache(ctx); err != nil {
			s.log.Error().Err(err).Msg("leaderboard async refresh failed")
		}
	}()
}

// UpdateUserScore 增量更新单个用户的排行榜分数
func (s *ScoringService) UpdateUserScore(ctx context.Context, username string) error {
	user, err := s.CalculateUserScore(ctx, username)
	if err != nil {
		return err
	}

	if err := s.store.UpsertLeaderboardUser(ctx, user); err != nil {
		return fmt.Errorf("upsert leaderboard user: %w", err)
	}

	s.log.Debug().
		Str("user", username).
		Float64("score", user.Score).
		Int("prs", user.PRCount).
		Int("repos", user.RepoCount).
		Msg("leaderboard user score updated")
	return nil
}

// UpdateUserScoreAsync 异步增量更新单个用户分数
func (s *ScoringService) UpdateUserScoreAsync(username string) {
	go func() {
		// 使用独立 context，避免请求结束导致 leaderboard 更新被中断
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.UpdateUserScore(ctx, username); err != nil {
			s.log.Warn().Err(err).Str("user", username).Msg("leaderboard user score update failed")
		}
	}()
}

// GetLeaderboard 读取排行榜（带排名）
func (s *ScoringService) GetLeaderboard(ctx context.Context, offset, limit int) ([]cache.LeaderboardUser, int64, error) {
	users, err := s.store.GetLeaderboardRange(ctx, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("get leaderboard range: %w", err)
	}
	total, err := s.store.GetLeaderboardCount(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get leaderboard count: %w", err)
	}
	return users, total, nil
}

// GetStats 读取统计信息
func (s *ScoringService) GetStats(ctx context.Context) (cache.LeaderboardMeta, error) {
	meta, err := s.store.GetLeaderboardMeta(ctx)
	if err != nil {
		return cache.LeaderboardMeta{}, fmt.Errorf("get leaderboard meta: %w", err)
	}
	return meta, nil
}

