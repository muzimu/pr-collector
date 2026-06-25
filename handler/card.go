package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"pr-collector/redis/cache"
	"pr-collector/svc"
)

// CardHandler GET /card 处理器 — SVG 徽章接口
type CardHandler struct {
	store        *cache.Store
	renderer     *svc.Renderer
	fetcher      *svc.Fetcher
	svgCacheTTL  time.Duration
	fetchTimeout time.Duration
	log          zerolog.Logger
}

// NewCardHandler 创建 /card 处理器
func NewCardHandler(store *cache.Store, renderer *svc.Renderer, fetcher *svc.Fetcher, svgCacheTTL time.Duration, log zerolog.Logger) *CardHandler {
	return &CardHandler{
		store:        store,
		renderer:     renderer,
		fetcher:      fetcher,
		svgCacheTTL:  svgCacheTTL,
		fetchTimeout: 20 * time.Second, // 同步抓取最长等待时间
		log:          log,
	}
}

// Handle 处理 GET /card
// 参数: username, style(default/dark/compact), top(展示前N个仓库，默认0不展示)
func (h *CardHandler) Handle(c *gin.Context) {
	username := c.Query("username")
	style := c.DefaultQuery("style", "default")
	topN, _ := strconv.Atoi(c.DefaultQuery("top", "0"))

	if username == "" {
		h.writeSVG(c, h.renderer.RenderError("missing_username"))
		return
	}

	ctx := c.Request.Context()
	h.store.IncrCardVisits(ctx, username)

	// 1. SVG 缓存命中 → 直接返回（缓存键包含 top 参数）
	svg, err := h.store.GetSVGBySuffix(ctx, svgCacheSuffix(username, style, topN))
	if err == nil && svg != "" {
		h.writeSVG(c, svg)
		return
	}

	// 2. 尝试读取已有 PR 数据
	prs, err := h.store.GetPRList(ctx, username)
	if err == nil && len(prs) > 0 {
		prs = filterMerged(prs)
		svg = h.renderer.RenderSVG(username, style, prs, topN)
		_ = h.store.SetSVGBySuffix(ctx, svgCacheSuffix(username, style, topN), svg, h.svgCacheTTL)
		h.writeSVG(c, svg)
		return
	}
	if err != nil && err != redis.Nil {
		h.log.Error().Err(err).Str("user", username).Msg("redis error getting pr list")
	}

	// 3. 无数据：同步抓取
	_ = h.store.AddUser(ctx, username)

	fetchCtx, cancel := context.WithTimeout(ctx, h.fetchTimeout)
	defer cancel()

	prs, err = h.fetcher.FetchUserSync(fetchCtx, username)
	if err != nil {
		h.log.Error().Err(err).Str("user", username).Msg("sync fetch failed")
		h.writeSVG(c, h.renderer.RenderPlaceholder("error"))
		return
	}

	prs = filterMerged(prs)
	if len(prs) == 0 {
		h.writeSVG(c, h.renderer.RenderPlaceholder("empty"))
		return
	}

	svg = h.renderer.RenderSVG(username, style, prs, topN)
	_ = h.store.SetSVGBySuffix(ctx, svgCacheSuffix(username, style, topN), svg, h.svgCacheTTL)
	h.writeSVG(c, svg)
}

// svgCacheSuffix 构建 SVG 缓存键后缀: username:style:top
func svgCacheSuffix(username, style string, topN int) string {
	return fmt.Sprintf("%s:%s:%d", username, style, topN)
}

func (h *CardHandler) writeSVG(c *gin.Context, svg string) {
	c.Header("Content-Type", "image/svg+xml")
	c.Header("Cache-Control", "public, max-age=86400, immutable")
	c.String(http.StatusOK, svg)
}
