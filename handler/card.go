package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"pr-collector/redis/cache"
	"pr-collector/svc"
)

// CardHandler GET /card 处理器 — SVG 徽章接口
type CardHandler struct {
	store       *cache.Store
	renderer    *svc.Renderer
	provider    *svc.PRProvider
	svgCacheTTL time.Duration
	log         zerolog.Logger
}

// NewCardHandler 创建 /card 处理器
func NewCardHandler(store *cache.Store, renderer *svc.Renderer, provider *svc.PRProvider, svgCacheTTL time.Duration, log zerolog.Logger) *CardHandler {
	return &CardHandler{
		store:       store,
		renderer:    renderer,
		provider:    provider,
		svgCacheTTL: svgCacheTTL,
		log:         log,
	}
}

// Handle 处理 GET /card
// 参数: username, style(default/dark/compact), top(展示前N个仓库，默认0不展示)
func (h *CardHandler) Handle(c *gin.Context) {
	username := c.Query("username")
	style := c.DefaultQuery("style", "default")
	topN, _ := strconv.Atoi(c.DefaultQuery("top", "0"))

	if username == "" {
		h.log.Warn().Str("client_ip", c.ClientIP()).Msg("[GET /card] missing username")
		h.writeSVG(c, h.renderer.RenderError("missing_username"))
		return
	}

	h.log.Info().Str("username", username).Str("style", style).Int("top", topN).Str("client_ip", c.ClientIP()).Msg("[GET /card] request")

	ctx := c.Request.Context()
	h.store.IncrCardVisits(ctx, username)

	// 1. SVG 缓存命中 → 直接返回（缓存键包含 top 参数）
	svg, err := h.store.GetSVGBySuffix(ctx, svgCacheSuffix(username, style, topN))
	if err == nil && svg != "" {
		h.log.Debug().Str("username", username).Str("style", style).Msg("[GET /card] cache hit")
		h.writeSVG(c, svg)
		return
	}

	// 2. 获取 PR 数据（优先缓存，缓存未命中则同步抓取）
	prs, err := h.provider.GetOrFetch(ctx, username)
	if err != nil {
		h.log.Error().Err(err).Str("username", username).Msg("[GET /card] fetch failed")
		h.writeSVG(c, h.renderer.RenderPlaceholder("error"))
		return
	}

	if len(prs) == 0 {
		h.log.Info().Str("username", username).Msg("[GET /card] no PRs found")
		h.writeSVG(c, h.renderer.RenderPlaceholder("empty"))
		return
	}

	// 3. 渲染并缓存 SVG
	svg = h.renderer.RenderSVG(username, style, prs, topN)
	_ = h.store.SetSVGBySuffix(ctx, svgCacheSuffix(username, style, topN), svg, h.svgCacheTTL)
	h.log.Info().Str("username", username).Str("style", style).Int("total_prs", len(prs)).Int("top", topN).Msg("[GET /card] ok")
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
