package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"pr-collector/svc"
)

type fakeFetchSubmitter struct {
	submitted bool
	usernames []string
}

func (f *fakeFetchSubmitter) SubmitFetch(username string) bool {
	f.usernames = append(f.usernames, username)
	return f.submitted
}

func TestRefreshResponseVariesByHXRequest(t *testing.T) {
	fetcher := &fakeFetchSubmitter{submitted: true}
	router := newPRRefreshTestRouter(t, fetcher)

	t.Run("htmx request gets a status fragment", func(t *testing.T) {
		recorder := performRefreshRequest(router, true, "alice")

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
		}
		body := recorder.Body.String()
		if strings.Contains(body, "<!DOCTYPE html>") {
			t.Fatal("htmx refresh unexpectedly rendered a complete page")
		}
		if !strings.Contains(body, `class="refresh-status refresh-success"`) || !strings.Contains(body, "刷新任务已提交") {
			t.Fatal("htmx refresh response does not contain the success fragment")
		}
		if got := recorder.Header().Get("Vary"); got != htmxRequestHeader {
			t.Fatalf("Vary = %q, want %q", got, htmxRequestHeader)
		}
	})

	t.Run("normal request redirects to a GET status page", func(t *testing.T) {
		recorder := performRefreshRequest(router, false, "alice")

		if recorder.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusSeeOther)
		}
		location := recorder.Header().Get("Location")
		if location != "/refresh/status?submitted=true&username=alice" {
			t.Fatalf("Location = %q, want refresh status URL", location)
		}

		statusRecorder := httptest.NewRecorder()
		statusRequest := httptest.NewRequest(http.MethodGet, location, nil)
		router.ServeHTTP(statusRecorder, statusRequest)
		if statusRecorder.Code != http.StatusOK {
			t.Fatalf("status page status = %d, want %d", statusRecorder.Code, http.StatusOK)
		}
		body := statusRecorder.Body.String()
		if !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, "刷新任务已提交") {
			t.Fatal("redirect target did not render the complete refresh result page")
		}
	})

	if len(fetcher.usernames) != 2 || fetcher.usernames[0] != "alice" || fetcher.usernames[1] != "alice" {
		t.Fatalf("submitted usernames = %v, want [alice alice]", fetcher.usernames)
	}
}

func TestRefreshValidationErrorResponseVariesByHXRequest(t *testing.T) {
	router := newPRRefreshTestRouter(t, &fakeFetchSubmitter{submitted: true})

	recorder := performRefreshRequest(router, true, "")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("htmx status = %d, want %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	if strings.Contains(recorder.Body.String(), "<!DOCTYPE html>") || !strings.Contains(recorder.Body.String(), "缺少 username") {
		t.Fatal("htmx validation error is not a fragment")
	}

	recorder = performRefreshRequest(router, false, "")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("normal status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), "<!DOCTYPE html>") || !strings.Contains(recorder.Body.String(), "feedback-error-message") {
		t.Fatal("normal validation error is not a styled complete page")
	}
}

func newPRRefreshTestRouter(t *testing.T, fetcher fetchSubmitter) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	renderer, err := svc.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	handler := NewPRHandler(nil, renderer, nil, fetcher, zerolog.Nop())
	router := gin.New()
	router.SetHTMLTemplate(renderer.HTMLTemplate())
	router.POST("/refresh", handler.HandleRefresh)
	router.GET("/refresh/status", handler.HandleRefreshStatus)
	return router
}

func performRefreshRequest(router http.Handler, htmx bool, username string) *httptest.ResponseRecorder {
	form := url.Values{}
	if username != "" {
		form.Set("username", username)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/refresh", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		request.Header.Set(htmxRequestHeader, "true")
	}
	router.ServeHTTP(recorder, request)
	return recorder
}
