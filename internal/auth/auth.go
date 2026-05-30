// Package auth verifies Subsonic/OpenSubsonic credentials by forwarding a ping
// request to the configured Navidrome server.
//
// # Design rationale
//
// The sidecar never stores, hashes, or validates passwords itself. Instead, it
// delegates all credential checking to Navidrome by forwarding the same u/t/s
// parameters the client sent. This means:
//
//   - The sidecar cannot be used to bypass Navidrome's access controls.
//   - If Navidrome changes its password hashing scheme the sidecar continues to
//     work without modification.
//   - The sidecar never sees plain-text passwords; it only passes the MD5 token
//     and salt that the client already computed.
//
// # OpenSubsonic token auth
//
// The client computes: t = md5(password + salt)
// where salt is a random string (minimum 6 characters) generated per-request.
// Both t and s are sent as query parameters alongside u (username).
// Reference: https://opensubsonic.netlify.app/docs/api-reference/
package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// pingTimeout is the maximum time to wait for Navidrome to respond to an auth
// ping. Kept short because auth checks block every incoming request, and a
// slow Navidrome startup is better surfaced quickly than masked by a long wait.
const pingTimeout = 5 * time.Second

// client is the package-level HTTP client used for all auth pings. A shared
// client reuses TCP connections across requests, which matters when the sidecar
// handles concurrent requests from the Naviamp client.
var client = &http.Client{Timeout: pingTimeout}

// pingResponse is the minimal subset of the Subsonic JSON response needed to
// determine whether credentials are valid. We only unmarshal what we need to
// avoid coupling to the full Navidrome response schema.
type pingResponse struct {
	SubsonicResponse struct {
		Status string `json:"status"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"subsonic-response"`
}

// Verify confirms that the given Subsonic credentials are accepted by the
// Navidrome instance at navidromeURL. It returns nil if authentication
// succeeds, or a descriptive error otherwise.
//
// Parameters:
//   - navidromeURL: base URL of the Navidrome server, e.g. "http://localhost:4533".
//     Must not have a trailing slash.
//   - u: Navidrome username.
//   - t: MD5 token = md5(password + salt), hex-encoded lowercase.
//   - s: random salt string (min 6 chars) that was used to compute t.
//
// The sidecar passes these parameters verbatim — it never inspects or modifies
// the credentials beyond forwarding them.
func Verify(navidromeURL, u, t, s string) error {
	pingURL := fmt.Sprintf(
		"%s/rest/ping.view?u=%s&t=%s&s=%s&v=1.16.1&c=naviamp&f=json",
		navidromeURL, u, t, s,
	)

	resp, err := client.Get(pingURL)
	if err != nil {
		// Network error reaching Navidrome — could be startup race or misconfiguration.
		// Return a generic error; don't leak the internal URL to the client.
		return fmt.Errorf("could not reach Navidrome for auth verification: %w", err)
	}
	defer resp.Body.Close()

	var pr pingResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return fmt.Errorf("invalid response from Navidrome during auth check: %w", err)
	}

	if pr.SubsonicResponse.Status != "ok" {
		// Navidrome rejected the credentials. Surface the error code and message
		// so the sidecar can construct a matching Subsonic error envelope for the
		// client.
		if pr.SubsonicResponse.Error != nil {
			return &VerifyError{
				Code:    pr.SubsonicResponse.Error.Code,
				Message: pr.SubsonicResponse.Error.Message,
			}
		}
		return &VerifyError{Code: 40, Message: "Wrong username or password."}
	}

	return nil
}

// VerifyError is returned by Verify when Navidrome rejects the credentials.
// It carries the Subsonic error code and message so the handler can include
// them verbatim in the error envelope it returns to the client.
type VerifyError struct {
	// Code is a Subsonic error code. Common values:
	//   40  Wrong username or password
	//   10  Required parameter missing
	//   50  User not authorised
	Code    int
	Message string
}

func (e *VerifyError) Error() string {
	return fmt.Sprintf("subsonic auth error %d: %s", e.Code, e.Message)
}
