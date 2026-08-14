package svc

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"pr-collector/github"
)

func TestRendererEmbedsHTMXAndParsesFragments(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}

	asset, err := fs.ReadFile(renderer.StaticFS(), "htmx-2.0.10.min.js")
	if err != nil {
		t.Fatalf("read embedded htmx asset: %v", err)
	}
	if len(asset) < 10000 || !bytes.Contains(asset, []byte("htmx")) {
		t.Fatal("embedded htmx asset is missing or incomplete")
	}
	feedbackCSS, err := fs.ReadFile(renderer.StaticFS(), "feedback.css")
	if err != nil {
		t.Fatalf("read embedded feedback stylesheet: %v", err)
	}
	if !bytes.Contains(feedbackCSS, []byte(".feedback-card")) {
		t.Fatal("embedded feedback stylesheet is missing error fragment styles")
	}

	for _, name := range []string{
		"htmx_head",
		"leaderboard_rows",
		"leaderboard_page",
		"leaderboard_stats",
		"card_preview",
		"refresh_status",
		"error_fragment",
	} {
		if renderer.HTMLTemplate().Lookup(name) == nil {
			t.Fatalf("template %q was not parsed", name)
		}
	}

	if got := string(asset[:min(len(asset), 100)]); strings.TrimSpace(got) == "" {
		t.Fatal("embedded htmx asset starts with empty content")
	}

	var rendered bytes.Buffer
	err = renderer.HTMLTemplate().ExecuteTemplate(&rendered, "status.html", map[string]any{
		"Success":   true,
		"Title":     "完成",
		"Message":   "操作成功",
		"ReturnURL": "/",
	})
	if err != nil {
		t.Fatalf("render status page: %v", err)
	}
	if strings.Contains(rendered.String(), "ZgotmplZ") {
		t.Fatal("status page contains a template escaping error")
	}
}

func TestRendererUsesLeaderboardScore(t *testing.T) {
	renderer, err := NewRendererWithScoreMaxRaw(100000)
	if err != nil {
		t.Fatalf("NewRendererWithScoreMaxRaw() error = %v", err)
	}
	prs := []github.PRInfo{
		{Repo: "a/b", RepoStars: 1023},
		{Repo: "a/b", RepoStars: 1023},
		{Repo: "c/d", RepoStars: 1},
	}

	svg := renderer.RenderSVG("alice", "default", prs, 0)
	want := calculateScore(calculateRawScore(prs).raw, 100000)
	if !strings.Contains(svg, fmt.Sprintf(">%.1f</text>", want)) {
		t.Fatalf("rendered SVG does not contain leaderboard score %.1f", want)
	}
}
