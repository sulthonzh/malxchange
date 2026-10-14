// The api upstream client (ticket #58): admin-api fronts api's
// internal member RPCs (search/detail/lock, KYC queue/evidence/review,
// Authenticator reset) the same way api fronts wallet's — a typed
// client over the internal mux (funds.go's pattern). The compose
// network is the auth boundary; every mutating call carries
// X-Mal-Actor so api's audit rows name the acting Employee.
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// errAPIDown marks an unreachable or slow api; Employees get one
// honest 503 sentence, operators get the cause in the log.
var errAPIDown = errors.New("api rpc unreachable")

// apiUpstreamError is one api error answer: its HTTP status and the
// message from the {"error": …} body (or plain text).
type apiUpstreamError struct {
	Status  int
	Message string
}

func (e *apiUpstreamError) Error() string {
	return fmt.Sprintf("api %d: %s", e.Status, e.Message)
}

// apiClient is the typed client for api's member internal RPC. Paths
// are prefix-free (the boot harness strips /internal); the base URL
// carries the /internal suffix.
type apiClient struct {
	base string
	http *http.Client
}

func newAPIClient(env *AdminEnv) *apiClient {
	return &apiClient{
		base: strings.TrimRight(env.ApiBaseURL, "/"),
		http: &http.Client{Timeout: env.ApiTimeout},
	}
}

// defaultCallCap bounds one proxied body (lists, reviews). The
// evidence RPC trades document images and gets its own, larger cap —
// the member side accepted up to 5 MiB per image, base64 inflates 4/3.
const (
	defaultCallCap = 1 << 20
	evidenceCap    = 16 << 20
)

// call performs one JSON call as the acting Employee ("" = no
// attribution header — reads). Non-2xx answers come back as
// *apiUpstreamError, transport failures as errAPIDown.
func (c *apiClient) call(ctx context.Context, method, path string, in, out any, maxBytes int64, actor string) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return errAPIDown
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if actor != "" {
		req.Header.Set("X-Mal-Actor", actor)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errAPIDown
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return errAPIDown
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		var env struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Error != "" {
			msg = env.Error
		}
		return &apiUpstreamError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
