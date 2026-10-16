package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/internal/oauthreturn"
)

func TestBindReturnTo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name   string
		body   string
		page   oauthreturn.Page
		status int
	}{
		{"no body", "", oauthreturn.Settings, 0},
		{"settings", `{"return_to":"settings"}`, oauthreturn.Settings, 0},
		{"onboarding", `{"return_to":"onboarding"}`, oauthreturn.Onboarding, 0},
		{"another page", `{"return_to":"/welcome"}`, "", http.StatusBadRequest},
		{"unknown field", `{"return_to":"onboarding","next":"/"}`, "", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/install", strings.NewReader(test.body))
			page, ok := bindReturnTo(c)
			if test.status != 0 {
				if ok || recorder.Code != test.status {
					t.Fatalf("bindReturnTo = %q, %v, status %d; want refusal %d", page, ok, recorder.Code, test.status)
				}
				return
			}
			if !ok || page != test.page {
				t.Fatalf("bindReturnTo = %q, %v; want %q", page, ok, test.page)
			}
		})
	}
}
