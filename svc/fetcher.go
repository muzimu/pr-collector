package svc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"pr-collector/github"
	"pr-collector/redis/cache"
	"github.com/rs/zerolog"
)

const (
	defaultFetchQueueSize = 100
	defaultFetchWorkers   = 3
)

// Fetcher PR 数据抓取服务：协调 GitHub GraphQL → Redis 存储
// 内置有界 worker pool，防止 goroutine 泄漏
type Fetcher struct {
	store   *cache.Store
	client  *github.Client
	log     zerolog.Logger
	lockTTL time.Duration

	// 应用级 context，用于优雅关闭时取消正在进行的抓取
	appCtx    context.Context
	appCancel context.CancelFunc

	// 有界异步抓取 worker pool
	taskCh  chan string
	wg      sync.WaitGroup
	workers int
}

// NewFetcher 创建抓取服务
// appCtx: 应用级 context，在 main.Shutdown 时 cancel，终止所有进行中的抓取
func NewFetcher(store *cache.Store, client *github.Client, lockTTL time.Duration, log zerolog.Logger) *Fetcher {
	return NewFetcherWithContext(context.Background(), store, client, lockTTL, log)
}

// NewFetcherWithContext 创建带应用级 context 的抓取服务
func NewFetcherWithContext(appCtx context.Context, store *cache.Store, client *github.Client, lockTTL time.Duration, log zerolog.Logger) *Fetcher {
	ctx, cancel := context.WithCancel(appCtx)

	f := &Fetcher{
		store:     store,
		client:    client,
		log:       log,
		lockTTL:   lockTTL,
		appCtx:    ctx,
		appCancel: cancel,
		taskCh:    make(chan string, defaultFetchQueueSize),
		workers:   defaultFetchWorkers,
	}

	// 启动 worker pool
	for i := 0; i < f.workers; i++ {
		f.wg.Add(1)
		go f.worker(i)
	}

	return f
}

// Shutdown 优雅关闭抓取服务：停止接收新任务，等待进行中任务完成（或超时取消）
func (f *Fetcher) Shutdown(timeout time.Duration) {
	f.appCancel() // 取消所有使用 appCtx 的操作
	close(f.taskCh)

	// 等待 workers 退出，带超时保护
	done := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		f.log.Info().Msg("fetcher workers exited cleanly")
	case <-time.After(timeout):
		f.log.Warn().Msg("fetcher shutdown timed out")
	}
}

// SubmitFetch 非阻塞提交异步抓取任务
// 队列满时丢弃并返回 false，防止无界堆积
func (f *Fetcher) SubmitFetch(username string) bool {
	select {
	case f.taskCh <- username:
		return true
	default:
		f.log.Warn().Str("user", username).Msg("fetch queue full, task dropped")
		return false
	}
}

// worker 从 taskCh 消费任务，调用实际抓取逻辑
func (f *Fetcher) worker(_ int) {
	defer f.wg.Done()

	for username := range f.taskCh {
		// 检查是否正在关闭
		select {
		case <-f.appCtx.Done():
			return
		default:
		}

		f.fetchUser(username)
	}
}

// FetchUser 异步抓取单个用户 PR 数据，结果通过 worker pool 处理
// 不经过 worker pool，直接用 appCtx
func (f *Fetcher) FetchUser(username string) {
	f.fetchUser(username)
}

// FetchUserSync 同步抓取用户 PR 并返回结果。
// 用于首次访问 /card 时阻塞等待真实数据，超时由调用方 context 控制。
func (f *Fetcher) FetchUserSync(ctx context.Context, username string) ([]github.PRInfo, error) {
	return f.fetchUserSync(ctx, username)
}

// fetchUser 异步版核心逻辑，丢弃返回值
func (f *Fetcher) fetchUser(username string) {
	_, _ = f.fetchUserSync(f.appCtx, username)
}

// fetchUserSync 核心抓取逻辑：加锁 → 调 API → 写 Redis → 清理缓存 → 返回 PR 列表
func (f *Fetcher) fetchUserSync(ctx context.Context, username string) ([]github.PRInfo, error) {
	// 分布式锁：防止并发重复抓取
	ok, err := f.store.TryLock(ctx, username, f.lockTTL)
	if err != nil {
		f.log.Error().Err(err).Str("user", username).Msg("try lock failed")
		return nil, fmt.Errorf("try lock: %w", err)
	}
	if !ok {
		f.log.Debug().Str("user", username).Msg("fetch skipped: locked by another process")
		return nil, fmt.Errorf("fetch already in progress for %s", username)
	}
	defer func() {
		if err := f.store.Unlock(context.Background(), username); err != nil {
			f.log.Warn().Err(err).Str("user", username).Msg("unlock failed")
		}
	}()

	f.log.Info().Str("user", username).Msg("fetch started")

	prs, err := f.client.FetchAllPRs(username)
	if err != nil {
		f.log.Error().Err(err).Str("user", username).Msg("fetch failed")
		// 标记失败时使用 Background context，避免被取消影响状态写入
		_ = f.store.SetUserStatus(context.Background(), username, "fail", err.Error())
		return nil, err
	}

	// 事务式写入 PR 列表
	if err := f.store.SetPRList(ctx, username, prs); err != nil {
		f.log.Error().Err(err).Str("user", username).Msg("store pr list failed")
		_ = f.store.SetUserStatus(context.Background(), username, "fail", err.Error())
		return nil, err
	}

	// 更新用户状态
	if err := f.store.SetUserStatus(ctx, username, "normal", ""); err != nil {
		f.log.Error().Err(err).Str("user", username).Msg("update user status failed")
	}

	// 清理 SVG 缓存，保证新数据即时生效
	if err := f.store.ClearUserSVG(ctx, username); err != nil {
		f.log.Warn().Err(err).Str("user", username).Msg("clear svg cache failed")
	}

	f.log.Info().
		Str("user", username).
		Int("count", len(prs)).
		Msg("fetch completed")

	return prs, nil
}

// FetchAllUsers 全量同步所有用户（cron 使用）
// 返回成功/失败计数
func (f *Fetcher) FetchAllUsers(maxWorkers int) (success, fail int) {
	ctx := f.appCtx

	usernames, err := f.store.GetAllUsers(ctx)
	if err != nil {
		f.log.Error().Err(err).Msg("get all users failed")
		return 0, 0
	}

	if len(usernames) == 0 {
		return 0, 0
	}

	// Worker pool 模式：只创建 maxWorkers 个 goroutine
	jobs := make(chan string, len(usernames))
	type result struct {
		user string
		err  error
	}
	results := make(chan result, len(usernames))

	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for username := range jobs {
				select {
				case <-ctx.Done():
					results <- result{user: username, err: ctx.Err()}
					continue
				default:
				}

				ok, _ := f.store.TryLock(ctx, username, f.lockTTL)
				if !ok {
					results <- result{user: username}
					continue
				}

				prs, err := f.client.FetchAllPRs(username)
				if err != nil {
					_ = f.store.SetUserStatus(context.Background(), username, "fail", err.Error())
					_ = f.store.Unlock(context.Background(), username)
					results <- result{user: username, err: err}
					continue
				}

				_ = f.store.SetPRList(ctx, username, prs)
				_ = f.store.SetUserStatus(ctx, username, "normal", "")
				_ = f.store.ClearUserSVG(ctx, username)
				_ = f.store.Unlock(context.Background(), username)
				results <- result{user: username}
			}
		}()
	}

	// 投递所有任务
	for _, u := range usernames {
		jobs <- u
	}
	close(jobs)

	// 等待所有 worker 完成
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if r.err != nil {
			fail++
			f.log.Warn().Err(r.err).Str("user", r.user).Msg("sync failed")
		} else {
			success++
		}
	}

	return success, fail
}
