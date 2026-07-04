package svc

import (
	"context"
	"time"

	"github.com/jimmicro/singleflight"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"pr-collector/github"
	"pr-collector/redis/cache"
)

// prFetchGroup 屏蔽 singleflight 具体类型，便于单元测试替换
type prFetchGroup interface {
	Do(key string, fn func() ([]github.PRInfo, error)) ([]github.PRInfo, error)
}

// PRProvider 封装 PR 数据获取逻辑：优先缓存，缓存未命中则同步抓取
// 使用 singleflight 合并同用户名并发请求，避免重复调用 GitHub API
type PRProvider struct {
	onFetched    func(ctx context.Context, username string)
	store        *cache.Store
	fetcher      *Fetcher
	fetchTimeout time.Duration
	sf           prFetchGroup
	log          zerolog.Logger
}

// NewPRProvider 创建 PR 数据提供者
func NewPRProvider(store *cache.Store, fetcher *Fetcher, fetchTimeout time.Duration, log zerolog.Logger) *PRProvider {
	return &PRProvider{
		store:        store,
		fetcher:      fetcher,
		fetchTimeout: fetchTimeout,
		sf:           singleflight.New[[]github.PRInfo](),
		log:          log,
	}
}

// OnFetched 设置 PR 数据成功抓取并缓存后的回调
func (p *PRProvider) OnFetched(fn func(ctx context.Context, username string)) {
	p.onFetched = fn
}

// GetOrFetch 获取用户 PR 列表：优先从缓存读取，缓存未命中则同步抓取并缓存
// 返回的 PR 列表已过滤，仅包含已合并的 PR
func (p *PRProvider) GetOrFetch(ctx context.Context, username string) ([]github.PRInfo, error) {
	// 1. 尝试从缓存读取
	prs, err := p.store.GetPRList(ctx, username)
	if err == nil {
		return filterMerged(prs), nil
	}
	if err != redis.Nil {
		p.log.Error().Err(err).Str("user", username).Msg("redis error getting pr list")
	}

	// 2. 缓存未命中：singleflight 合并同用户并发请求
	v, err := p.sf.Do(username, func() ([]github.PRInfo, error) {
		return p.fetchAndStore(ctx, username)
	})
	if err != nil {
		return nil, err
	}
	return filterMerged(v), nil
}

// fetchAndStore 同步抓取并缓存用户 PR 数据；singleflight 保证同一时刻只执行一次
func (p *PRProvider) fetchAndStore(ctx context.Context, username string) ([]github.PRInfo, error) {
	_ = p.store.AddUser(ctx, username)

	fetchCtx, cancel := context.WithTimeout(ctx, p.fetchTimeout)
	defer cancel()

	prs, err := p.fetcher.FetchUserSync(fetchCtx, username)
	if err != nil {
		p.log.Error().Err(err).Str("user", username).Msg("sync fetch failed")
		return nil, err
	}

	if p.onFetched != nil {
		p.onFetched(ctx, username)
	}

	return prs, nil
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
