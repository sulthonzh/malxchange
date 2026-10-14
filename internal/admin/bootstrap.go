// The installer's bootstrap RPC (ADR-0007/ADR-0012): compose-network
// only (the boot harness keeps /internal off Caddy; no tokens — the
// network boundary is the auth), creates the first super-admin from
// one-time credentials the installer generates, and refuses forever
// once any Employee exists. The `-bootstrap-first-employee` flag mode
// is the installer's client: it reads the credentials from stdin (not
// argv — no ps leak) and POSTs to its own internal listener.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/syaikhipin/malxchange/internal/config"
)

type bootstrapRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RegisterInternal mounts the internal RPC (prefix-free pattern; the
// boot harness serves it at /internal/bootstrap/employee) plus the
// member content read RPC (#63, /internal/content/*).
func (s *Server) RegisterInternal(mux *http.ServeMux) {
	mux.HandleFunc("POST /bootstrap/employee", s.audited(ActionCreateEmployee, s.handleBootstrapEmployee))
	s.RegisterContentRPC(mux)
}

// handleBootstrapEmployee creates the first super-admin. The audit
// envelope is written by the wrapping middleware; this handler only
// fills it in (system actor — the installer has no session).
func (s *Server) handleBootstrapEmployee(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry(r)
	var req bootstrapRequest
	if !decodeBody(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(w, req.Email)
	if !ok {
		return
	}
	if len(req.Password) < 8 {
		writeInternalErr(w, http.StatusBadRequest, "the one-time password must be at least 8 characters")
		return
	}
	emp, err := s.CreateFirstEmployee(r.Context(), email, req.Password)
	if errors.Is(err, ErrAlreadyBootstrapped) {
		writeInternalErr(w, http.StatusConflict, "an Employee already exists — bootstrap refused")
		return
	}
	if err != nil {
		s.log.Error("admin: bootstrap create", "err", err)
		writeInternalErr(w, http.StatusInternalServerError, "bootstrap failed")
		return
	}
	if entry != nil {
		entry.Actor = systemActor
		entry.Target = emp.ID
		entry.After = mustJSON(auditView(emp))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          emp.ID,
		"email":       emp.Email,
		"role":        "super-admin",
		"must_rotate": true,
	})
}

// BootstrapRequested reports whether the binary was invoked as the
// installer's bootstrap client (-bootstrap-first-employee). Parsing is
// tolerant: in serve mode other flags (-healthcheck) simply make this
// false, and service.Main parses them properly afterwards.
func BootstrapRequested() bool {
	fs := flag.NewFlagSet("admin-api bootstrap", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	boot := fs.Bool("bootstrap-first-employee", false,
		"create the first super-admin from stdin {email,password} via the internal RPC, then exit")
	_ = fs.Parse(os.Args[1:])
	return *boot
}

// RunBootstrapClient is the installer-side half: read the one-time
// credentials from stdin, call the internal RPC on this container's
// own listener, and print a machine-checkable verdict. Installer
// contract: stdout starting with "bootstrapped" = created, "exists" =
// already has an Employee (rerun, not an error); any other outcome is
// a failure (nonzero exit).
func RunBootstrapClient(ctx context.Context) int {
	var req bootstrapRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap: read credentials from stdin:", err)
		return 2
	}
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		fmt.Fprintln(os.Stderr, "bootstrap: stdin must be {\"email\":...,\"password\":...}")
		return 2
	}
	payload, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		return 2
	}
	addr := config.EnvDefault("MAL_HTTP_ADDR", ":8080")
	req2, err := http.NewRequestWithContext(ctx, "POST",
		"http://127.0.0.1"+addr+"/internal/bootstrap/employee", strings.NewReader(string(payload)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		return 2
	}
	req2.Header.Set("Content-Type", "application/json")
	var resp *http.Response
	for i := range 60 {
		var err error
		resp, err = http.DefaultClient.Do(req2.Clone(ctx))
		if err == nil {
			break
		}
		if i == 59 {
			fmt.Fprintln(os.Stderr, "bootstrap: admin-api internal RPC unreachable:", err)
			return 1
		}
		time.Sleep(time.Second)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var out struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			fmt.Fprintln(os.Stderr, "bootstrap:", err)
			return 1
		}
		fmt.Println("bootstrapped " + out.ID)
		return 0
	case http.StatusConflict:
		fmt.Println("exists — first Employee already created; bootstrap skipped")
		return 0
	default:
		raw, _ := io.ReadAll(resp.Body)
		fmt.Printf("bootstrap-refused %d: %s\n", resp.StatusCode, raw)
		return 1
	}
}
