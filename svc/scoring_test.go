package svc

import (
	"math"
	"testing"

	"github.com/rs/zerolog"

	"pr-collector/github"
)

func TestCalculateScore(t *testing.T) {
	svc := NewScoringService(nil, 15, zerolog.Logger{})

	tests := []struct {
		raw  float64
		want float64
	}{
		{0, 60.0},
		{3, 80.0},
		{15, 100.0},
	}

	for _, tt := range tests {
		got := svc.CalculateScore(tt.raw)
		if math.Abs(got-tt.want) > 1e-6 {
			t.Errorf("CalculateScore(%v) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestCalculateRawScore(t *testing.T) {
	prs := []github.PRInfo{
		{Repo: "a/b", RepoStars: 1023, State: "MERGED"}, // log2(1024)=10
		{Repo: "a/b", RepoStars: 1023, State: "MERGED"},
		{Repo: "c/d", RepoStars: 1, State: "MERGED"},
	}

	raw := calculateRawScore(prs)
	wantRaw := 10.0 * math.Log2(3) // 1-star 仓库低于 16-star 门槛，不计分。
	if math.Abs(raw.raw-wantRaw) > 1e-6 {
		t.Errorf("raw = %v, want %v", raw.raw, wantRaw)
	}
	if raw.prCount != 3 {
		t.Errorf("prCount = %d, want 3", raw.prCount)
	}
	if raw.repoCount != 2 {
		t.Errorf("repoCount = %d, want 2", raw.repoCount)
	}
}

func TestCalculateRawScoreFiltersBelowStarThreshold(t *testing.T) {
	prs := []github.PRInfo{
		{Repo: "a/low", RepoStars: 15, State: "MERGED"},
		{Repo: "b/threshold", RepoStars: 16, State: "MERGED"},
		{Repo: "c/high", RepoStars: 256, State: "MERGED"},
	}

	raw := calculateRawScore(prs)
	wantRaw := math.Log2(17) + math.Log2(257)
	if math.Abs(raw.raw-wantRaw) > 1e-6 {
		t.Errorf("raw = %v, want %v", raw.raw, wantRaw)
	}
}

func TestRepoStarThreshold(t *testing.T) {
	tests := []struct {
		stars []int
		want  int
	}{
		{nil, 16},
		{[]int{1, 15, 16}, 16},
		{[]int{20, 40, 100, 200, 400}, 40},
		{[]int{500, 600, 700, 800}, 200},
	}

	for _, tt := range tests {
		if got := repoStarThreshold(tt.stars); got != tt.want {
			t.Errorf("repoStarThreshold(%v) = %d, want %d", tt.stars, got, tt.want)
		}
	}
}

func TestCalculateRawScoreCountsAllGiven(t *testing.T) {
	// calculateRawScore 由调用方负责先 filterMerged，本函数仅统计传入的 PR
	prs := []github.PRInfo{
		{Repo: "a/b", RepoStars: 1023, State: "MERGED"},
		{Repo: "a/b", RepoStars: 1023, State: "OPEN"},
	}

	raw := calculateRawScore(prs)
	if raw.prCount != 2 {
		t.Errorf("prCount = %d, want 2", raw.prCount)
	}
}

func TestScoringServiceEmptyDeps(t *testing.T) {
	// Ensure constructor does not panic with nil store
	s := NewScoringService(nil, 0, zerolog.Logger{})
	if s == nil {
		t.Fatal("expected non-nil service")
	}
	if s.maxRaw != 100000 {
		t.Errorf("maxRaw = %v, want 100000", s.maxRaw)
	}
}
