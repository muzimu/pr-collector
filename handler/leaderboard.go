package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"pr-collector/redis/cache"
	"pr-collector/svc"
)

// LeaderboardHandler 排行榜处理器
type LeaderboardHandler struct {
	store   *cache.Store
	scoring *svc.ScoringService
	log     zerolog.Logger
}

// NewLeaderboardHandler 创建排行榜处理器
func NewLeaderboardHandler(store *cache.Store, scoring *svc.ScoringService, log zerolog.Logger) *LeaderboardHandler {
	return &LeaderboardHandler{
		store:   store,
		scoring: scoring,
		log:     log,
	}
}

// HandleIndex GET / — 首页 SSR
func (h *LeaderboardHandler) HandleIndex(c *gin.Context) {
	ctx := c.Request.Context()

	users, total, err := h.scoring.GetLeaderboard(ctx, 0, 50)
	if err != nil {
		h.log.Error().Err(err).Msg("[GET /] get leaderboard failed")
	}

	meta, err := h.scoring.GetStats(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("[GET /] get stats failed")
	}

	h.addRanks(users, 0)

	baseURL := h.baseURL(c)
	initialData, _ := json.Marshal(map[string]interface{}{
		"users":   users,
		"total":   total,
		"stats":   meta,
		"baseURL": baseURL,
	})

	c.HTML(http.StatusOK, "index.html", gin.H{
		"Users":       users,
		"Total":       total,
		"Stats":       meta,
		"BaseURL":     baseURL,
		"InitialData": string(initialData),
	})
}

// HandleLeaderboard GET /api/leaderboard
func (h *LeaderboardHandler) HandleLeaderboard(c *gin.Context) {
	ctx := c.Request.Context()

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	users, total, err := h.scoring.GetLeaderboard(ctx, offset, limit)
	if err != nil {
		h.log.Error().Err(err).Msg("[GET /api/leaderboard] failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	h.addRanks(users, offset)

	c.JSON(http.StatusOK, gin.H{
		"users": users,
		"total": total,
	})
}

// HandleStats GET /api/leaderboard/stats
func (h *LeaderboardHandler) HandleStats(c *gin.Context) {
	ctx := c.Request.Context()

	meta, err := h.scoring.GetStats(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("[GET /api/leaderboard/stats] failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, meta)
}

// HandleRefresh POST /refresh/leaderboard — 手动触发排行榜缓存刷新
func (h *LeaderboardHandler) HandleRefresh(c *gin.Context) {
	ctx := c.Request.Context()

	if err := h.scoring.RefreshLeaderboardCache(ctx); err != nil {
		h.log.Error().Err(err).Msg("[POST /refresh/leaderboard] failed")
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "排行榜刷新失败",
		})
		return
	}

	h.log.Info().Msg("[POST /refresh/leaderboard] ok")
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "排行榜刷新任务已完成",
	})
}

func (h *LeaderboardHandler) addRanks(users []cache.LeaderboardUser, offset int) {
	for i := range users {
		users[i].Rank = offset + i + 1
	}
}

func (h *LeaderboardHandler) baseURL(c *gin.Context) string {
	scheme := c.Request.URL.Scheme
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return fmt.Sprintf("%s://%s", scheme, c.Request.Host)
}
