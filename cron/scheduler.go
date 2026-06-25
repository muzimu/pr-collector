package cron

import (
	"sync"

	"pr-collector/svc"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
)

// Scheduler 定时任务调度器
type Scheduler struct {
	cron       *cron.Cron
	fetcher    *svc.Fetcher
	log        zerolog.Logger
	maxWorkers int

	mu     sync.Mutex
	running bool // 防止重入
}

// NewScheduler 创建并配置 cron 调度器
func NewScheduler(cronExpr string, fetcher *svc.Fetcher, maxWorkers int, log zerolog.Logger) *Scheduler {
	s := &Scheduler{
		cron:       cron.New(cron.WithSeconds()),
		fetcher:    fetcher,
		log:        log,
		maxWorkers: maxWorkers,
	}

	s.cron.AddFunc(cronExpr, func() {
		s.log.Info().Msg("cron: full sync started")
		s.mu.Lock()
		if s.running {
			s.mu.Unlock()
			s.log.Warn().Msg("cron: previous sync still running, skipped")
			return
		}
		s.running = true
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()

		success, fail := s.fetcher.FetchAllUsers(s.maxWorkers)
		s.log.Info().
			Int("success", success).
			Int("fail", fail).
			Msg("cron: full sync completed")
	})

	return s
}

// Start 启动定时任务
func (s *Scheduler) Start() {
	s.cron.Start()
	s.log.Info().Msg("cron scheduler started")
}

// Stop 优雅停止定时任务（等待当前任务完成）
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	s.log.Info().Msg("cron scheduler stopped")
}
