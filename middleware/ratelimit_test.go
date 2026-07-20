package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"pr-collector/handler"
	"pr-collector/middleware"
	"pr-collector/svc"
)

func TestRateLimitHTMLResponseVariesByHXRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	renderer, err := svc.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	limiter := middleware.NewRateLimiter(0, zerolog.Nop())
	router := gin.New()
	router.SetHTMLTemplate(renderer.HTMLTemplate())
	router.GET("/pr",
		limiter.HandlerWithResponder(
			func(*gin.Context) string { return "client" },
			func(c *gin.Context) {
				handler.RenderPageError(c, http.StatusTooManyRequests, "请求过于频繁，请稍后重试")
			},
		),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	tests := []struct {
		name     string
		htmx     bool
		doctype  bool
		contains string
	}{
		{name: "normal", doctype: true, contains: "/static/feedback.css"},
		{name: "htmx", htmx: true, doctype: false, contains: "feedback-card"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/pr?username=alice", nil)
			if test.htmx {
				request.Header.Set("HX-Request", "true")
			}
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
			}
			body := recorder.Body.String()
			if strings.Contains(body, "<!DOCTYPE html>") != test.doctype || !strings.Contains(body, test.contains) {
				t.Fatalf("unexpected rate limit response body: %q", body)
			}
			if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Fatalf("Content-Type = %q, want text/html", got)
			}
			if got := recorder.Header().Get("Vary"); got != "HX-Request" {
				t.Fatalf("Vary = %q, want HX-Request", got)
			}
		})
	}
}

func TestDefaultRateLimitResponseRemainsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := middleware.NewRateLimiter(0, zerolog.Nop())
	router := gin.New()
	router.GET("/card",
		limiter.Handler(func(*gin.Context) string { return "client" }),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/card?username=alice", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("default response = %d %q, want 429 JSON", recorder.Code, recorder.Header().Get("Content-Type"))
	}
}
