package svc

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"pr-collector/github"
	"pr-collector/redis/cache"
)

// PRProvider 封装 PR 数据获取逻辑：优先缓存，缓存未命中则同步抓取
type PRProvider struct {
	store        *cache.Store
	fetcher      *Fetcher
	fetchTimeout time.Duration
	log          zerolog.Logger
}

// NewPRProvider 创建 PR 数据提供者
func NewPRProvider(store *cache.Store, fetcher *Fetcher, fetchTimeout time.Duration, log zerolog.Logger) *PRProvider {
	return &PRProvider{
		store:        store,
		fetcher:      fetcher,
		fetchTimeout: fetchTimeout,
		log:          log,
	}
}

// GetOrFetch 获取用户 PR 列表：优先从缓存读取，缓存未命中则同步抓取并缓存
// 返回的 PR 列表已过滤，仅包含已合并的 PR
func (p *PRProvider) GetOrFetch(ctx context.Context, username string) ([]github.PRInfo, error) {
	// 1. 尝试从缓存读取
	prs, err := p.store.GetPRList(ctx, username)
	if err == nil && len(prs) > 0 {
		return filterMerged(prs), nil
	}
	if err != nil && err != redis.Nil {
		p.log.Error().Err(err).Str("user", username).Msg("redis error getting pr list")
	}

	// 2. 缓存未命中：同步抓取
	_ = p.store.AddUser(ctx, username)

	fetchCtx, cancel := context.WithTimeout(ctx, p.fetchTimeout)
	defer cancel()

	prs, err = p.fetcher.FetchUserSync(fetchCtx, username)
	if err != nil {
		p.log.Error().Err(err).Str("user", username).Msg("sync fetch failed")
		return nil, err
	}

	return filterMerged(prs), nil
}

// filterMerged 过滤仅保留已合并的 PR
func filterMerged(prs []github.PRInfo) []github.PRInfo {
	out := make([]github.PRInfo, 0, len(prs))
	for _, pr := range prs {
		if pr.State == "MERGED" {
			out = append(out, pr)
		}
	}
	return out
}
