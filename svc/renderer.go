package svc

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"math"
	"pr-collector/github"
	"sort"
	"strconv"
	texttemplate "text/template"
)

//go:embed tmpl/svg/*.svg
var svgFS embed.FS

//go:embed tmpl/html/*.html
var htmlFS embed.FS

//go:embed static/*
var embeddedStaticFS embed.FS

// RepoInfo 单仓库摘要（用于 SVG 徽章展示 Top N）
type RepoInfo struct {
	Name      string
	Stars     int
	StarsText string // 格式化后的 star 数量（如 1.2k）
	StarsX    int    // star 文本在 chip 内的 x 坐标
	PRCount   int
	Width     int // chip 估算宽度
	Offset    int // chip 在当前行中的 x 偏移
	Row       int // chip 所在行号（从 0 开始，每行最多 3 个）
}

// SVGBadgeData SVG 模板渲染数据
type SVGBadgeData struct {
	Username   string
	TotalPRs   int
	TotalRepos int
	TopRepos   []RepoInfo // 按 Stars 降序的 Top N 仓库，空则不展示仓库列表
	Score      float64    // Trend Score
	Height     int        // SVG 高度，根据 TopRepos 数量自动计算
}

// Renderer SVG 和 HTML 模板渲染器
type Renderer struct {
	svgTemplates  map[string]*texttemplate.Template
	htmlTemplates *htmltemplate.Template
	staticFiles   fs.FS
}

// repoAgg 仓库聚合（用于 RenderSVG 内部分组统计）
type repoAgg struct {
	stars   int
	prCount int
}

const badgeScoreMaxRaw = 100000.0

// truncateName 截断过长的仓库名，避免单个 chip 超出卡片宽度
func truncateName(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	if maxLen <= 3 {
		return name[:maxLen]
	}
	return name[:maxLen-3] + "..."
}

// formatStars 将 star 数量格式化为紧凑可读字符串
func formatStars(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	case n < 100000000:
		return fmt.Sprintf("%.1fm", float64(n)/1000000)
	default:
		return fmt.Sprintf("%.0fm", float64(n)/1000000)
	}
}

// calculateBadgeScore 根据仓库 star 分布计算 Trend Score（映射到 [50, 100]）
func calculateBadgeScore(repoMap map[string]*repoAgg) float64 {
	raw := 0.0
	for _, ra := range repoMap {
		raw += float64(ra.prCount) * math.Log2(float64(ra.stars)+1)
	}
	score := 50.0 + 50.0*math.Log10(raw+1)/math.Log10(badgeScoreMaxRaw+1)
	if score > 100 {
		score = 100
	}
	return score
}

// NewRenderer 加载并编译全部模板
func NewRenderer() (*Renderer, error) {
	// 自定义模板函数
	funcMap := texttemplate.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
		"repoColor": func(i int) string {
			colors := []string{"#ff6b6b", "#4ecdc4", "#45b7d1", "#96ceb4", "#ffeaa7", "#dfe6e9"}
			return colors[i%len(colors)]
		},
	}

	// 加载 SVG 模板
	svgTemplates := make(map[string]*texttemplate.Template)
	entries, err := svgFS.ReadDir("tmpl/svg")
	if err != nil {
		return nil, fmt.Errorf("read svg template dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		tmpl, err := texttemplate.New(e.Name()).Funcs(funcMap).ParseFS(svgFS, "tmpl/svg/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("parse svg template %s: %w", e.Name(), err)
		}
		// 提取 style 名称 (去掉 .svg 后缀)
		style := e.Name()[:len(e.Name())-4]
		svgTemplates[style] = tmpl
	}

	// 加载 HTML 模板（带自定义函数 add、sub、formatStars）
	htmlFuncMap := htmltemplate.FuncMap{
		"add":         func(a, b int) int { return a + b },
		"sub":         func(a, b int) int { return a - b },
		"formatStars": formatStars,
	}
	htmlTemplates, err := htmltemplate.New("").Funcs(htmlFuncMap).ParseFS(htmlFS, "tmpl/html/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse html templates: %w", err)
	}
	staticFiles, err := fs.Sub(embeddedStaticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded static files: %w", err)
	}

	return &Renderer{
		svgTemplates:  svgTemplates,
		htmlTemplates: htmlTemplates,
		staticFiles:   staticFiles,
	}, nil
}

// RenderSVG 根据风格渲染 SVG 徽章
// topN: 展示前 N 个仓库（按 Stars 降序），0 表示不展示仓库列表
func (r *Renderer) RenderSVG(username, style string, prs []github.PRInfo, topN int) string {
	data := SVGBadgeData{
		Username:   username,
		TotalPRs:   len(prs),
		TotalRepos: 0,
	}

	// 按仓库分组，统计 Stars 和 PR 数
	repoMap := make(map[string]*repoAgg)
	for _, pr := range prs {
		ra, ok := repoMap[pr.Repo]
		if !ok {
			ra = &repoAgg{stars: pr.RepoStars}
			repoMap[pr.Repo] = ra
		}
		ra.prCount++
	}
	data.TotalRepos = len(repoMap)

	// Trend Score：复用排行榜对数压缩公式，默认 raw 上限 100000
	data.Score = calculateBadgeScore(repoMap)

	// Top N：按 Stars 降序排列
	if topN > 0 && len(repoMap) > 0 {
		type entry struct {
			name  string
			stars int
			count int
		}
		sorted := make([]entry, 0, len(repoMap))
		for name, ra := range repoMap {
			sorted = append(sorted, entry{name, ra.stars, ra.prCount})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].stars > sorted[j].stars
		})

		// 按 topN 展示仓库，根据 chip 宽度自动换行
		const maxRowWidth = 660 // chip 行可用宽度上限（避开右侧 trend score 列）
		chipLimit := min(topN, len(sorted))
		data.TopRepos = make([]RepoInfo, chipLimit)
		row := 0
		offset := 0
		for i := range chipLimit {
			displayName := truncateName(sorted[i].name, 25)
			nameWidth := int(float64(len(displayName)) * 7.5)
			starsText := formatStars(sorted[i].stars)
			// star 区域包含 "★ " 图标与数字，按 2 个额外字符估算宽度
			starsWidth := int(float64(len(starsText)+2) * 7.5)
			starsX := 22 + nameWidth + 8
			width := starsX + starsWidth + 8

			// 当前行放不下时换行（第一个 chip 除外）
			if i > 0 && offset+width > maxRowWidth {
				row++
				offset = 0
			}

			data.TopRepos[i] = RepoInfo{
				Name:      displayName,
				Stars:     sorted[i].stars,
				StarsText: starsText,
				StarsX:    starsX,
				PRCount:   sorted[i].count,
				Width:     width,
				Offset:    offset,
				Row:       row,
			}
			offset += width + 8
		}
	}

	// 自适应高度：基础 158px，每多一行 chip +28px
	if len(data.TopRepos) > 0 {
		maxRow := 0
		for _, r := range data.TopRepos {
			if r.Row > maxRow {
				maxRow = r.Row
			}
		}
		data.Height = 158 + (maxRow+1)*28
	} else {
		data.Height = 158
	}

	tmpl, ok := r.svgTemplates[style]
	if !ok {
		tmpl = r.svgTemplates["default"]
		if tmpl == nil {
			return r.RenderError("unknown_style")
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return r.RenderError("render_failed")
	}
	return buf.String()
}

// RenderPlaceholder 渲染占位 SVG（加载中/错误状态）
func (r *Renderer) RenderPlaceholder(state string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="120">
  <rect width="400" height="120" rx="12" fill="#e2e8f0"/>
  <text x="200" y="65" text-anchor="middle" fill="#718096" font-size="16" font-family="Arial,sans-serif">
    %s
  </text>
</svg>`, placeholderText(state))
}

// RenderError 渲染错误 SVG
func (r *Renderer) RenderError(reason string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="120">
  <rect width="400" height="120" rx="12" fill="#fed7d7"/>
  <text x="200" y="65" text-anchor="middle" fill="#c53030" font-size="16" font-family="Arial,sans-serif">
    Error: %s
  </text>
</svg>`, reason)
}

// HTMLTemplate 返回编译好的 HTML 模板实例
func (r *Renderer) HTMLTemplate() *htmltemplate.Template {
	return r.htmlTemplates
}

// StaticFS returns versioned frontend assets embedded in the application.
func (r *Renderer) StaticFS() fs.FS {
	return r.staticFiles
}

func placeholderText(state string) string {
	switch state {
	case "loading":
		return "Loading PR data..."
	case "error":
		return "Data fetch failed, retry later"
	case "empty":
		return "No pull requests found"
	default:
		return "..."
	}
}
