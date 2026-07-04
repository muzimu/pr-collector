package handler

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"pr-collector/github"
	"pr-collector/redis/cache"
	"pr-collector/svc"
)

// RepoGroup 按仓库聚合的 PR 展示结构
type RepoGroup struct {
	Repo     string
	RepoURL  string
	Stars    int
	PRs      []github.PRInfo
}

// PRHandler GET /pr + POST /refresh 处理器 — PR 详情页
type PRHandler struct {
	store    *cache.Store
	renderer *svc.Renderer
	provider *svc.PRProvider
	fetcher  *svc.Fetcher
	log      zerolog.Logger
}

// NewPRHandler 创建 PR 页面处理器
func NewPRHandler(store *cache.Store, renderer *svc.Renderer, provider *svc.PRProvider, fetcher *svc.Fetcher, log zerolog.Logger) *PRHandler {
	return &PRHandler{
		store:    store,
		renderer: renderer,
		provider: provider,
		fetcher:  fetcher,
		log:      log,
	}
}

// HandlePRPage GET /pr — PR 详情页
func (h *PRHandler) HandlePRPage(c *gin.Context) {
	username := c.Query("username")
	if username == "" {
		h.log.Warn().Str("client_ip", c.ClientIP()).Msg("[GET /pr] missing username")
		c.HTML(http.StatusOK, "error.html", gin.H{
			"message": "缺少 username 参数",
		})
		return
	}

	ctx := c.Request.Context()
	h.store.IncrPRVisits(ctx, username)

	// 获取 PR 数据（优先缓存，缓存未命中则同步抓取）
	prs, err := h.provider.GetOrFetch(ctx, username)
	if err != nil {
		h.log.Error().Err(err).Str("username", username).Msg("[GET /pr] fetch failed")
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{
			"message": "数据抓取失败，请稍后重试",
		})
		return
	}

	if len(prs) == 0 {
		h.log.Info().Str("username", username).Msg("[GET /pr] no PRs found")
		c.HTML(http.StatusOK, "empty.html", gin.H{
			"Username": username,
		})
		return
	}

	groups := groupByRepo(prs)
	h.log.Info().Str("username", username).Int("total_prs", len(prs)).Int("total_repos", len(groups)).Msg("[GET /pr] ok")
	c.HTML(http.StatusOK, "pr_list.html", gin.H{
		"Username":   username,
		"Groups":     groups,
		"TotalPRs":   len(prs),
		"TotalRepos": len(groups),
	})
}

// HandleRefresh POST /refresh — 手动刷新 PR 数据
func (h *PRHandler) HandleRefresh(c *gin.Context) {
	username := c.PostForm("username")
	if username == "" {
		h.log.Warn().Str("client_ip", c.ClientIP()).Msg("[POST /refresh] missing username")
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "缺少 username",
		})
		return
	}

	submitted := h.fetcher.SubmitFetch(username)
	if submitted {
		h.log.Info().Str("username", username).Msg("[POST /refresh] submitted")
	} else {
		h.log.Info().Str("username", username).Msg("[POST /refresh] rejected (queue full)")
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":        submitted,
		"message":   "刷新任务已提交，请稍后刷新页面",
		"submitted": submitted,
	})
}

// groupByRepo 将 PR 列表按仓库分组，按 Stars 降序排列（参考 pr-collector-py 的输出格式）
func groupByRepo(prs []github.PRInfo) []RepoGroup {
	repoMap := make(map[string]*RepoGroup)

	for _, pr := range prs {
		g, ok := repoMap[pr.Repo]
		if !ok {
			g = &RepoGroup{
				Repo:    pr.Repo,
				RepoURL: "https://github.com/" + pr.Repo,
				Stars:   pr.RepoStars,
			}
			repoMap[pr.Repo] = g
		}
		g.PRs = append(g.PRs, pr)
	}

	groups := make([]RepoGroup, 0, len(repoMap))
	for _, g := range repoMap {
		groups = append(groups, *g)
	}

	// 按 Star 数降序
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Stars > groups[j].Stars
	})

	return groups
}
