package svc

import (
	"math"
	"testing"

	"github.com/rs/zerolog"

	"pr-collector/github"
)

func TestCalculateScore(t *testing.T) {
	svc := NewScoringService(nil, 100000, zerolog.Logger{})

	tests := []struct {
		raw  float64
		want float64
	}{
		{0, 50.0},
		{100000, 100.0},
	}

	for _, tt := range tests {
		got := svc.CalculateScore(tt.raw)
		if math.Abs(got-tt.want) > 1e-6 {
			t.Errorf("CalculateScore(%v) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestCalculateRawScore(t *testing.T) {
	svc := NewScoringService(nil, 100000, zerolog.Logger{})
	prs := []github.PRInfo{
		{Repo: "a/b", RepoStars: 1023, State: "MERGED"}, // log2(1024)=10
		{Repo: "a/b", RepoStars: 1023, State: "MERGED"},
		{Repo: "c/d", RepoStars: 1, State: "MERGED"},    // log2(2)=1
	}

	raw := svc.calculateRawScore(prs)
	wantRaw := 2.0*10.0 + 1.0*1.0 // 21
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

func TestCalculateRawScoreCountsAllGiven(t *testing.T) {
	// calculateRawScore 由调用方负责先 filterMerged，本函数仅统计传入的 PR
	svc := NewScoringService(nil, 100000, zerolog.Logger{})
	prs := []github.PRInfo{
		{Repo: "a/b", RepoStars: 1023, State: "MERGED"},
		{Repo: "a/b", RepoStars: 1023, State: "OPEN"},
	}

	raw := svc.calculateRawScore(prs)
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
