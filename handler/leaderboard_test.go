package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"pr-collector/redis/cache"
	"pr-collector/svc"
)

type fakeLeaderboardScorer struct {
	users []cache.LeaderboardUser
	total int64
	stats cache.LeaderboardMeta

	leaderboardErr error
	statsErr       error
	refreshErr     error
	refreshCalls   int
}

func (f *fakeLeaderboardScorer) GetLeaderboard(_ context.Context, offset, limit int) ([]cache.LeaderboardUser, int64, error) {
	if f.leaderboardErr != nil {
		return nil, 0, f.leaderboardErr
	}
	if offset >= len(f.users) {
		return []cache.LeaderboardUser{}, f.total, nil
	}
	end := min(offset+limit, len(f.users))
	users := append([]cache.LeaderboardUser(nil), f.users[offset:end]...)
	return users, f.total, nil
}

func (f *fakeLeaderboardScorer) GetStats(context.Context) (cache.LeaderboardMeta, error) {
	return f.stats, f.statsErr
}

func (f *fakeLeaderboardScorer) RefreshLeaderboardCache(context.Context) error {
	f.refreshCalls++
	return f.refreshErr
}

func TestAddRanks(t *testing.T) {
	h := &LeaderboardHandler{}
	users := []cache.LeaderboardUser{
		{Username: "a", Score: 100},
		{Username: "b", Score: 90},
		{Username: "c", Score: 80},
	}

	h.addRanks(users, 0)
	if users[0].Rank != 1 || users[1].Rank != 2 || users[2].Rank != 3 {
		t.Errorf("ranks = %v, want [1 2 3]", []int{users[0].Rank, users[1].Rank, users[2].Rank})
	}

	h.addRanks(users, 50)
	if users[0].Rank != 51 || users[1].Rank != 52 || users[2].Rank != 53 {
		t.Errorf("ranks = %v, want [51 52 53]", []int{users[0].Rank, users[1].Rank, users[2].Rank})
	}
}

func TestLeaderboardResponseVariesByHXRequest(t *testing.T) {
	router := newLeaderboardTestRouter(t)

	t.Run("normal request gets a complete page", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/leaderboard?limit=1&offset=0", nil)

		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
		}
		body := recorder.Body.String()
		if !strings.Contains(body, "<!DOCTYPE html>") {
			t.Fatal("normal request did not render a complete HTML page")
		}
		if !strings.Contains(body, "/static/htmx-2.0.10.min.js") {
			t.Fatal("complete page does not load the embedded htmx asset")
		}
		if got := recorder.Header().Get("Vary"); got != htmxRequestHeader {
			t.Fatalf("Vary = %q, want %q", got, htmxRequestHeader)
		}
	})

	t.Run("htmx request gets rows and pagination fragments", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/leaderboard?limit=1&offset=0", nil)
		request.Header.Set(htmxRequestHeader, "true")

		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
		}
		body := recorder.Body.String()
		if strings.Contains(body, "<!DOCTYPE html>") {
			t.Fatal("htmx request unexpectedly rendered a complete page")
		}
		if !strings.Contains(body, `data-username="alice"`) {
			t.Fatal("htmx response does not contain the requested leaderboard row")
		}
		if !strings.Contains(body, `hx-swap-oob="outerHTML"`) {
			t.Fatal("htmx response does not update pagination out of band")
		}
		if got := recorder.Header().Get("Vary"); got != htmxRequestHeader {
			t.Fatalf("Vary = %q, want %q", got, htmxRequestHeader)
		}
	})
}

func TestCardPreviewResponseVariesByHXRequest(t *testing.T) {
	router := newLeaderboardTestRouter(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/card/preview?username=alice", nil)
	request.Header.Set(htmxRequestHeader, "true")
	router.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatal("htmx card preview unexpectedly rendered a complete page")
	}
	if !strings.Contains(body, `id="card-preview-result"`) || !strings.Contains(body, "username=alice") {
		t.Fatal("htmx card preview response is incomplete")
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/card/preview?username=alice", nil)
	router.ServeHTTP(recorder, request)
	if !strings.Contains(recorder.Body.String(), "<!DOCTYPE html>") {
		t.Fatal("normal card preview request did not render a complete page")
	}
}

func TestStatsResponseVariesByHXRequest(t *testing.T) {
	router := newLeaderboardTestRouter(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/leaderboard/stats", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "<!DOCTYPE html>") {
		t.Fatal("normal stats request did not render a complete page")
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/leaderboard/stats", nil)
	request.Header.Set(htmxRequestHeader, "true")
	router.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, `id="leaderboard-stats"`) || !strings.Contains(body, `data-target="20"`) {
		t.Fatal("htmx stats response does not contain the expected fragment")
	}
}

func TestLeaderboardRefreshUsesPRGForNormalRequests(t *testing.T) {
	scorer := newFakeLeaderboardScorer()
	router := newLeaderboardTestRouterWithScorer(t, scorer)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/refresh/leaderboard", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusSeeOther)
	}
	if location := recorder.Header().Get("Location"); location != "/?notice=leaderboard-refreshed" {
		t.Fatalf("Location = %q, want leaderboard notice URL", location)
	}

	pageRecorder := httptest.NewRecorder()
	pageRequest := httptest.NewRequest(http.MethodGet, recorder.Header().Get("Location"), nil)
	router.ServeHTTP(pageRecorder, pageRequest)
	if pageRecorder.Code != http.StatusOK || !strings.Contains(pageRecorder.Body.String(), "排行榜刷新任务已完成") {
		t.Fatal("redirect target did not render the leaderboard refresh notice")
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/refresh/leaderboard", nil)
	request.Header.Set(htmxRequestHeader, "true")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "<!DOCTYPE html>") || !strings.Contains(recorder.Body.String(), "refresh-success") {
		t.Fatal("htmx leaderboard refresh did not render a success fragment")
	}
	if scorer.refreshCalls != 2 {
		t.Fatalf("refresh calls = %d, want 2", scorer.refreshCalls)
	}
}

func TestLeaderboardErrorsVaryByHXRequest(t *testing.T) {
	scorer := newFakeLeaderboardScorer()
	scorer.leaderboardErr = errors.New("redis unavailable")
	router := newLeaderboardTestRouterWithScorer(t, scorer)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/leaderboard", nil)
	router.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, "/static/feedback.css") {
		t.Fatal("normal error response is not a styled complete page")
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/leaderboard", nil)
	request.Header.Set(htmxRequestHeader, "true")
	router.ServeHTTP(recorder, request)
	body = recorder.Body.String()
	if recorder.Code != http.StatusInternalServerError || strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, "feedback-card") {
		t.Fatal("htmx error response is not a styled body fragment")
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
}

func TestStatsErrorRendersHTMXErrorFragment(t *testing.T) {
	scorer := newFakeLeaderboardScorer()
	scorer.statsErr = errors.New("redis unavailable")
	router := newLeaderboardTestRouterWithScorer(t, scorer)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/leaderboard/stats", nil)
	request.Header.Set(htmxRequestHeader, "true")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "<!DOCTYPE html>") || !strings.Contains(recorder.Body.String(), "统计数据加载失败") {
		t.Fatal("stats failure did not render the htmx error fragment")
	}
}

func TestHTMXRequestHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if isHTMXRequest(context) {
		t.Fatal("request without HX-Request was detected as htmx")
	}
	context.Request.Header.Set(htmxRequestHeader, "TRUE")
	if !isHTMXRequest(context) {
		t.Fatal("HX-Request: TRUE was not detected as htmx")
	}
}

func newLeaderboardTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	return newLeaderboardTestRouterWithScorer(t, newFakeLeaderboardScorer())
}

func newFakeLeaderboardScorer() *fakeLeaderboardScorer {
	return &fakeLeaderboardScorer{
		users: []cache.LeaderboardUser{
			{Username: "alice", Score: 95.5, PRCount: 12, RepoCount: 3},
			{Username: "bob", Score: 82, PRCount: 8, RepoCount: 2},
		},
		total: 2,
		stats: cache.LeaderboardMeta{TotalUsers: 2, TotalPRs: 20, TotalRepos: 5},
	}
}

func newLeaderboardTestRouterWithScorer(t *testing.T, scorer *fakeLeaderboardScorer) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	renderer, err := svc.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	handler := NewLeaderboardHandler(nil, scorer, zerolog.Nop())
	router := gin.New()
	router.SetHTMLTemplate(renderer.HTMLTemplate())
	router.GET("/", handler.HandleIndex)
	router.GET("/api/leaderboard", handler.HandleLeaderboard)
	router.GET("/api/leaderboard/stats", handler.HandleStats)
	router.POST("/refresh/leaderboard", handler.HandleRefresh)
	router.GET("/card/preview", handler.HandleCardPreview)
	return router
}
