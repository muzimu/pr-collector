package svc

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"sort"
	texttemplate "text/template"

	"pr-collector/github"
)

//go:embed tmpl/svg/*.svg
var svgFS embed.FS

//go:embed tmpl/html/*.html
var htmlFS embed.FS

// RepoInfo 单仓库摘要（用于 SVG 徽章展示 Top N）
type RepoInfo struct {
	Name    string
	Stars   int
	PRCount int
}

// SVGBadgeData SVG 模板渲染数据
type SVGBadgeData struct {
	Username   string
	TotalPRs   int
	TotalRepos int
	TopRepos   []RepoInfo // 按 Stars 降序的 Top N 仓库，空则不展示仓库列表
}

// Renderer SVG 和 HTML 模板渲染器
type Renderer struct {
	svgTemplates  map[string]*texttemplate.Template
	htmlTemplates *htmltemplate.Template
}

// NewRenderer 加载并编译全部模板
func NewRenderer() (*Renderer, error) {
	// 自定义模板函数
	funcMap := texttemplate.FuncMap{
		"add": func(a, b int) int { return a + b },
		"mul": func(a, b int) int { return a * b },
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

	// 加载 HTML 模板（带自定义函数 add）
	htmlFuncMap := htmltemplate.FuncMap{
		"add": func(a, b int) int { return a + b },
	}
	htmlTemplates, err := htmltemplate.New("").Funcs(htmlFuncMap).ParseFS(htmlFS, "tmpl/html/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse html templates: %w", err)
	}

	return &Renderer{
		svgTemplates:  svgTemplates,
		htmlTemplates: htmlTemplates,
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
	type repoAgg struct {
		stars   int
		prCount int
	}
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

		limit := topN
		if limit > len(sorted) {
			limit = len(sorted)
		}
		data.TopRepos = make([]RepoInfo, limit)
		for i := 0; i < limit; i++ {
			data.TopRepos[i] = RepoInfo{
				Name:    sorted[i].name,
				Stars:   sorted[i].stars,
				PRCount: sorted[i].count,
			}
		}
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
