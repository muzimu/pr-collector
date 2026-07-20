package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"pr-collector/redis/cache"
)

const (
	defaultLeaderboardLimit    = 50
	leaderboardRefreshedNotice = "leaderboard-refreshed"
)

type leaderboardScorer interface {
	GetLeaderboard(ctx context.Context, offset, limit int) ([]cache.LeaderboardUser, int64, error)
	GetStats(ctx context.Context) (cache.LeaderboardMeta, error)
	RefreshLeaderboardCache(ctx context.Context) error
}

// LeaderboardHandler 排行榜处理器
type LeaderboardHandler struct {
	store   *cache.Store
	scoring leaderboardScorer
	log     zerolog.Logger
}

// NewLeaderboardHandler 创建排行榜处理器
func NewLeaderboardHandler(store *cache.Store, scoring leaderboardScorer, log zerolog.Logger) *LeaderboardHandler {
	return &LeaderboardHandler{
		store:   store,
		scoring: scoring,
		log:     log,
	}
}

// HandleIndex GET / — 首页 SSR
func (h *LeaderboardHandler) HandleIndex(c *gin.Context) {
	notice := ""
	if c.Query("notice") == leaderboardRefreshedNotice {
		notice = "排行榜刷新任务已完成"
	}
	h.renderIndex(c, 0, defaultLeaderboardLimit, notice, "")
}

// HandleLeaderboard GET /api/leaderboard returns rows for htmx and a complete
// index page for normal browser requests.
func (h *LeaderboardHandler) HandleLeaderboard(c *gin.Context) {
	varyOnHTMXRequest(c)
	limit, offset := leaderboardPagination(c)

	if !isHTMXRequest(c) {
		h.renderIndex(c, offset, limit, "", "")
		return
	}

	ctx := c.Request.Context()
	users, total, err := h.scoring.GetLeaderboard(ctx, offset, limit)
	if err != nil {
		h.log.Error().Err(err).Msg("[GET /api/leaderboard] failed")
		RenderPageError(c, http.StatusInternalServerError, "排行榜加载失败，请稍后重试")
		return
	}

	h.addRanks(users, offset)
	c.HTML(http.StatusOK, "leaderboard_page", leaderboardData(users, total, offset, limit))
}

// HandleStats GET /api/leaderboard/stats returns the stat cards for htmx and a
// complete index page when opened directly.
func (h *LeaderboardHandler) HandleStats(c *gin.Context) {
	varyOnHTMXRequest(c)
	if !isHTMXRequest(c) {
		h.renderIndex(c, 0, defaultLeaderboardLimit, "", "")
		return
	}

	ctx := c.Request.Context()
	meta, err := h.scoring.GetStats(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("[GET /api/leaderboard/stats] failed")
		RenderPageError(c, http.StatusInternalServerError, "统计数据加载失败，请稍后重试")
		return
	}

	c.HTML(http.StatusOK, "leaderboard_stats", gin.H{"Stats": meta})
}

// HandleRefresh POST /refresh/leaderboard — 手动触发排行榜缓存刷新
func (h *LeaderboardHandler) HandleRefresh(c *gin.Context) {
	varyOnHTMXRequest(c)
	ctx := c.Request.Context()

	if err := h.scoring.RefreshLeaderboardCache(ctx); err != nil {
		h.log.Error().Err(err).Msg("[POST /refresh/leaderboard] failed")
		RenderPageError(c, http.StatusInternalServerError, "排行榜刷新失败")
		return
	}

	h.log.Info().Msg("[POST /refresh/leaderboard] ok")
	if !isHTMXRequest(c) {
		c.Redirect(http.StatusSeeOther, "/?notice="+leaderboardRefreshedNotice)
		return
	}
	c.HTML(http.StatusOK, "refresh_status", gin.H{
		"Success": true,
		"Message": "排行榜刷新任务已完成",
	})
}

// HandleCardPreview GET /card/preview renders a preview fragment for htmx and
// the full homepage for normal form submissions.
func (h *LeaderboardHandler) HandleCardPreview(c *gin.Context) {
	varyOnHTMXRequest(c)
	username := c.Query("username")
	if username == "" {
		if isHTMXRequest(c) {
			c.HTML(http.StatusUnprocessableEntity, "card_preview", cardPreviewData(h.baseURL(c), "", false))
			return
		}
		h.renderIndex(c, 0, defaultLeaderboardLimit, "", "")
		return
	}

	if !isHTMXRequest(c) {
		h.renderIndex(c, 0, defaultLeaderboardLimit, "", username)
		return
	}
	c.HTML(http.StatusOK, "card_preview", cardPreviewData(h.baseURL(c), username, true))
}

func (h *LeaderboardHandler) renderIndex(c *gin.Context, offset, limit int, notice, cardUsername string) {
	ctx := c.Request.Context()
	users, total, err := h.scoring.GetLeaderboard(ctx, offset, limit)
	if err != nil {
		h.log.Error().Err(err).Msg("render index: get leaderboard failed")
		RenderPageError(c, http.StatusInternalServerError, "排行榜加载失败，请稍后重试")
		return
	}

	meta, err := h.scoring.GetStats(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("render index: get stats failed")
	}

	h.addRanks(users, offset)
	data := leaderboardData(users, total, offset, limit)
	data["Stats"] = meta
	data["Notice"] = notice
	for key, value := range cardPreviewData(h.baseURL(c), cardUsername, cardUsername != "") {
		data[key] = value
	}
	c.HTML(http.StatusOK, "index.html", data)
}

func leaderboardPagination(c *gin.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultLeaderboardLimit)))
	offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 200 {
		limit = defaultLeaderboardLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func leaderboardData(users []cache.LeaderboardUser, total int64, offset, limit int) gin.H {
	nextOffset := offset + len(users)
	return gin.H{
		"Users":      users,
		"Total":      total,
		"Offset":     offset,
		"Limit":      limit,
		"NextOffset": nextOffset,
		"HasMore":    int64(nextOffset) < total,
	}
}

func cardPreviewData(baseURL, username string, generated bool) gin.H {
	displayUsername := username
	if displayUsername == "" {
		displayUsername = "YOUR_USERNAME"
	}
	escapedUsername := url.QueryEscape(displayUsername)
	return gin.H{
		"CardUsername":  username,
		"CardGenerated": generated,
		"CardMarkdown": fmt.Sprintf(
			"[![PR Collector](%s/card?username=%s&top=3&style=default)](%s/pr?username=%s)",
			baseURL,
			escapedUsername,
			baseURL,
			escapedUsername,
		),
	}
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
