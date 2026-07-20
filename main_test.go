package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"pr-collector/svc"
)

func TestUsernameValidationNegotiatesHTMLAndPreservesCardJSON(t *testing.T) {
	router := newMainMiddlewareTestRouter(t, false)

	tests := []struct {
		name        string
		path        string
		htmx        bool
		contentType string
		doctype     bool
	}{
		{name: "normal html", path: "/pr?username=invalid--name", contentType: "text/html", doctype: true},
		{name: "htmx html", path: "/pr?username=invalid--name", htmx: true, contentType: "text/html", doctype: false},
		{name: "card json", path: "/card?username=invalid--name", contentType: "application/json", doctype: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.htmx {
				request.Header.Set("HX-Request", "true")
			}
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, test.contentType) {
				t.Fatalf("Content-Type = %q, want prefix %q", got, test.contentType)
			}
			if strings.Contains(recorder.Body.String(), "<!DOCTYPE html>") != test.doctype {
				t.Fatalf("DOCTYPE presence does not match complete-page expectation: %q", recorder.Body.String())
			}
		})
	}
}

func TestRecoveryNegotiatesHTMLAndPreservesCardJSON(t *testing.T) {
	router := newMainMiddlewareTestRouter(t, true)

	tests := []struct {
		name        string
		path        string
		htmx        bool
		contentType string
		doctype     bool
	}{
		{name: "normal html", path: "/panic", contentType: "text/html", doctype: true},
		{name: "htmx html", path: "/panic", htmx: true, contentType: "text/html", doctype: false},
		{name: "card json", path: "/card", contentType: "application/json", doctype: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.htmx {
				request.Header.Set("HX-Request", "true")
			}
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
			}
			if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, test.contentType) {
				t.Fatalf("Content-Type = %q, want prefix %q", got, test.contentType)
			}
			if strings.Contains(recorder.Body.String(), "<!DOCTYPE html>") != test.doctype {
				t.Fatalf("DOCTYPE presence does not match complete-page expectation: %q", recorder.Body.String())
			}
		})
	}
}

func newMainMiddlewareTestRouter(t *testing.T, withRecovery bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	renderer, err := svc.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	router := gin.New()
	if withRecovery {
		router.Use(gin.CustomRecoveryWithWriter(io.Discard, recoverRequest))
	}
	router.SetHTMLTemplate(renderer.HTMLTemplate())
	if withRecovery {
		panicHandler := func(*gin.Context) { panic("test panic") }
		router.GET("/panic", panicHandler)
		router.GET("/card", panicHandler)
		return router
	}
	router.GET("/pr", usernameValidate, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/card", usernameValidate, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return router
}
