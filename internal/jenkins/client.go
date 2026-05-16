// Package jenkins is a small REST client for Jenkins that only talks to the
// single host configured at startup.
package jenkins

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultTimeout is the HTTP timeout used when Config.Timeout is zero.
const DefaultTimeout = 90 * time.Second

// debugEnabled is sampled once at process start. When set, debugf emits a
// single stderr line for each outbound Jenkins request, cache hit, and cache
// write. Stderr only — stdout is reserved for MCP protocol frames.
var debugEnabled = os.Getenv("JENKINS_MCP_DEBUG") != ""

// debugf prints a single stderr line when JENKINS_MCP_DEBUG is set. Callers
// must never pass response bodies or request headers — the Authorization
// header carries the Jenkins API token, and bodies can be megabytes.
func debugf(format string, args ...any) {
	if !debugEnabled {
		return
	}
	log.Printf("jenkins: "+format, args...)
}

// Client is a Jenkins REST client with HTTP Basic auth.
//
// All requests target a single base URL. There is no host switching at request
// time — an MCP process talks to exactly one Jenkins.
type Client struct {
	baseURL string
	user    string
	token   string
	http    *http.Client
}

// Config carries the inputs needed to construct a Client.
type Config struct {
	BaseURL string
	User    string
	Token   string
	Timeout time.Duration
}

// NewClient validates the configuration and returns a ready-to-use Client.
//
// BaseURL trailing slashes are trimmed so callers can join paths starting with
// "/". A zero Timeout falls back to DefaultTimeout.
func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" || cfg.User == "" || cfg.Token == "" {
		return nil, fmt.Errorf("jenkins: BaseURL, User, and Token are all required")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		user:    cfg.User,
		token:   cfg.Token,
		http:    &http.Client{Timeout: timeout},
	}, nil
}

// Get issues an authenticated GET to baseURL+path with optional query params
// and returns the response body. Non-2xx responses are returned as errors with
// a short body snippet for context.
func (c *Client) Get(ctx context.Context, path string, query map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		q := req.URL.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	req.SetBasicAuth(c.user, c.token)
	debugf("req  GET %s", req.URL.Path)
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		debugf("err  GET %s after %s: %v", req.URL.Path, time.Since(start), err)
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		debugf("err  GET %s read body after %s: %v", req.URL.Path, time.Since(start), err)
		return nil, err
	}
	debugf("resp %d GET %s in %s (%d bytes)", resp.StatusCode, req.URL.Path, time.Since(start), len(body))
	if resp.StatusCode/100 != 2 {
		snippet := string(body)
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		return nil, fmt.Errorf("jenkins %s returned HTTP %d: %s", req.URL.Path, resp.StatusCode, snippet)
	}
	return body, nil
}

// JobAPIPath converts a slash-separated job path like
// "folder/subfolder/job-name" into Jenkins' nested form
// "/job/folder/job/subfolder/job/job-name".
func JobAPIPath(jobPath string) string {
	var sb strings.Builder
	for _, p := range strings.Split(strings.Trim(jobPath, "/"), "/") {
		if p == "" {
			continue
		}
		sb.WriteString("/job/")
		sb.WriteString(p)
	}
	return sb.String()
}

// BuildRef returns "lastBuild" for n<=0, otherwise the decimal build number.
func BuildRef(n int64) string {
	if n <= 0 {
		return "lastBuild"
	}
	return fmt.Sprintf("%d", n)
}
