package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The api client's error mapping (issue #58): api's {"error": …}
// envelope becomes a typed upstream error the handlers re-emit with
// the original status and sentence; transport failures collapse to
// errAPIDown. Hermetic — no database, no integration gate.
func TestAPIClientErrorMapping(t *testing.T) {
	t.Run("upstream error envelope passes status and message through", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"authenticator reset requires a KYC-verified member"}`))
		}))
		defer stub.Close()

		c := &apiClient{base: stub.URL, http: stub.Client()}
		err := c.call(context.Background(), http.MethodPost, "/members/m-1/authenticator/reset",
			map[string]string{"actor": "c@it.test"}, nil, defaultCallCap, "c@it.test")
		var ue *apiUpstreamError
		if !errors.As(err, &ue) {
			t.Fatalf("want *apiUpstreamError, got %v", err)
		}
		if ue.Status != http.StatusConflict || ue.Message != "authenticator reset requires a KYC-verified member" {
			t.Fatalf("upstream error = %+v", ue)
		}
	})

	t.Run("plain-text upstream errors keep their body", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer stub.Close()

		c := &apiClient{base: stub.URL, http: stub.Client()}
		err := c.call(context.Background(), http.MethodGet, "/members", nil, nil, defaultCallCap, "")
		var ue *apiUpstreamError
		if !errors.As(err, &ue) || ue.Status != http.StatusInternalServerError || ue.Message == "" {
			t.Fatalf("upstream error = %v", err)
		}
	})

	t.Run("transport failure collapses to errAPIDown", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		base := stub.URL
		stub.Close() // nothing listens anymore

		c := &apiClient{base: base, http: &http.Client{}}
		err := c.call(context.Background(), http.MethodGet, "/members", nil, nil, defaultCallCap, "")
		if !errors.Is(err, errAPIDown) {
			t.Fatalf("want errAPIDown, got %v", err)
		}
	})

	t.Run("success decodes the answer and carries the actor header", func(t *testing.T) {
		var actor string
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor = r.Header.Get("X-Mal-Actor")
			_, _ = w.Write([]byte(`{"id":"m-1","status":"locked"}`))
		}))
		defer stub.Close()

		c := &apiClient{base: stub.URL, http: stub.Client()}
		var out struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := c.call(context.Background(), http.MethodPost, "/members/m-1/lock",
			map[string]any{}, &out, defaultCallCap, "s@it.test"); err != nil {
			t.Fatal(err)
		}
		if actor != "s@it.test" || out.ID != "m-1" || out.Status != "locked" {
			t.Fatalf("actor %q out %+v", actor, out)
		}
	})
}

// LoadAdminEnv's api knob defaults are the compose contract: api's
// internal RPC behind the /internal suffix, 5 s ceiling.
func TestLoadAdminEnvAPIDefaults(t *testing.T) {
	env := loadAdminEnv()
	if env.ApiBaseURL != "http://api:8080/internal" {
		t.Fatalf("ApiBaseURL default = %q", env.ApiBaseURL)
	}
	if env.ApiTimeout != 5*time.Second {
		t.Fatalf("ApiTimeout default = %v", env.ApiTimeout)
	}
}
