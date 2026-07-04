package cron

import (
	"context"
	"sync"

	"pr-collector/svc"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
)

// Scheduler 定时任务调度器
type Scheduler struct {
	cron               *cron.Cron
	fetcher            *svc.Fetcher
	scoring            *svc.ScoringService
	log                zerolog.Logger
	maxWorkers         int

	mu                  sync.Mutex
	fullSyncRunning     bool
	leaderboardRunning  bool
}

// NewScheduler 创建并配置 cron 调度器
func NewScheduler(fullSyncExpr, leaderboardExpr string, fetcher *svc.Fetcher, scoring *svc.ScoringService, maxWorkers int, log zerolog.Logger) *Scheduler {
	s := &Scheduler{
		cron:       cron.New(cron.WithSeconds()),
		fetcher:    fetcher,
		scoring:    scoring,
		log:        log,
		maxWorkers: maxWorkers,
	}

	s.cron.AddFunc(fullSyncExpr, func() {
		s.runFullSync()
	})

	s.cron.AddFunc(leaderboardExpr, func() {
		s.runLeaderboardRefresh()
	})

	return s
}

func (s *Scheduler) runFullSync() {
	s.log.Info().Msg("cron: full sync started")
	s.mu.Lock()
	if s.fullSyncRunning {
		s.mu.Unlock()
		s.log.Warn().Msg("cron: previous full sync still running, skipped")
		return
	}
	s.fullSyncRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.fullSyncRunning = false
		s.mu.Unlock()
	}()

	success, fail := s.fetcher.FetchAllUsers(s.maxWorkers)
	s.log.Info().
		Int("success", success).
		Int("fail", fail).
		Msg("cron: full sync completed")
}

func (s *Scheduler) runLeaderboardRefresh() {
	s.log.Info().Msg("cron: leaderboard refresh started")
	s.mu.Lock()
	if s.leaderboardRunning {
		s.mu.Unlock()
		s.log.Warn().Msg("cron: previous leaderboard refresh still running, skipped")
		return
	}
	s.leaderboardRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.leaderboardRunning = false
		s.mu.Unlock()
	}()

	ctx := context.Background()
	if err := s.scoring.RefreshLeaderboardCache(ctx); err != nil {
		s.log.Error().Err(err).Msg("cron: leaderboard refresh failed")
		return
	}

	s.log.Info().Msg("cron: leaderboard refresh completed")
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
