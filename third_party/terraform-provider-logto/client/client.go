package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	tokenType = "Bearer"
)

var (
	errEmptyID = errors.New("identifier must be a nonempty path segment without reserved characters")
)

type Config struct {
	Endpoint          string
	Hostname          string
	Resource          string
	ApplicationID     string
	ApplicationSecret string

	Logger     zerolog.Logger
	HttpClient *http.Client
}

func DefaultConfig() *Config {

	return &Config{
		Hostname:          os.Getenv("LOGTO_HOSTNAME"),
		Resource:          os.Getenv("LOGTO_RESOURCE"),
		ApplicationID:     os.Getenv("LOGTO_APPLICATION_ID"),
		ApplicationSecret: os.Getenv("LOGTO_APPLICATION_SECRET"),
		HttpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type Client struct {
	conf *Config

	accessTokenLock    sync.Mutex
	accessToken        string
	accessTokenExpires time.Time
}

func NewClient(config *Config) (*Client, error) {
	if config == nil {
		return nil, errors.New("logto client configuration is required")
	}
	copyConfig := *config
	config = &copyConfig
	defConfig := DefaultConfig()
	if config.Hostname == "" {
		config.Hostname = defConfig.Hostname
	}
	switch {
	case config.Resource != "":
		break
	case defConfig.Resource != "":
		config.Resource = defConfig.Resource
	default:
		config.Resource = fmt.Sprintf("https://%s/api", config.Hostname)
	}
	if config.ApplicationID == "" {
		config.ApplicationID = defConfig.ApplicationID
	}
	if config.ApplicationSecret == "" {
		config.ApplicationSecret = defConfig.ApplicationSecret
	}
	if config.HttpClient == nil {
		config.HttpClient = defConfig.HttpClient
	}

	if config.Hostname == "" {
		return nil, fmt.Errorf("missing Logto hostname")
	}

	if config.Endpoint == "" {
		config.Endpoint = "https://" + config.Hostname
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return nil, errors.New("logto endpoint must be an origin without credentials, query, or path")
	}
	ip := net.ParseIP(endpoint.Hostname())
	loopback := endpoint.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && loopback) {
		return nil, errors.New("logto endpoint requires HTTPS (HTTP is allowed only on loopback for tests)")
	}
	config.Endpoint = strings.TrimRight(config.Endpoint, "/")
	httpClient := *config.HttpClient
	if httpClient.Timeout <= 0 {
		httpClient.Timeout = 60 * time.Second
	}
	// Management credentials must never follow redirects to another origin.
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	config.HttpClient = &httpClient
	return &Client{conf: config}, nil
}

type request struct {
	method                             string
	path                               string
	body                               any
	rawBody                            io.Reader
	headers                            map[string]string
	queryParameters                    map[string]string
	application_id, application_secret string
}

func (r *request) toHttpRequest(ctx context.Context, conf *Config) (*http.Request, error) {
	reqUrl := conf.Endpoint + "/" + r.path

	var body io.Reader
	if r.body != nil {
		encoded, err := json.Marshal(r.body)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	} else if r.rawBody != nil {
		body = r.rawBody
	}
	req, err := http.NewRequestWithContext(ctx, r.method, reqUrl, body)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	for key, value := range r.queryParameters {
		params.Add(key, value)
	}
	req.URL.RawQuery = params.Encode()

	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for key, value := range r.headers {
		req.Header.Set(key, value)
	}

	return req, nil
}

func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	c.accessTokenLock.Lock()
	defer c.accessTokenLock.Unlock()
	if c.accessToken != "" && time.Now().Before(c.accessTokenExpires) {
		return c.accessToken, nil
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("resource", c.conf.Resource)
	data.Set("scope", "all")

	req := &request{
		method:             "POST",
		path:               "oidc/token",
		application_id:     c.conf.ApplicationID,
		application_secret: c.conf.ApplicationSecret,
		rawBody:            strings.NewReader(data.Encode()),
		headers: map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
		},
	}

	resp, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	type Response struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
	}

	var decodedResponse Response
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&decodedResponse); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if decodedResponse.TokenType != tokenType || decodedResponse.AccessToken == "" || decodedResponse.ExpiresIn <= 0 {
		return "", errors.New("invalid token response: expected a nonempty Bearer token and positive expires_in")
	}

	c.accessToken = decodedResponse.AccessToken
	c.accessTokenExpires = time.Now().Add(time.Duration(float64(decodedResponse.ExpiresIn)*0.7) * time.Second)
	return c.accessToken, nil
}

// do retries reads only; a mutation may have committed even when its response is lost.
func (c *Client) do(ctx context.Context, r *request) (*http.Response, error) {
	refreshed := false
	readRetries := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, err := r.toHttpRequest(ctx, c.conf)
		if err != nil {
			return nil, err
		}
		token := ""
		if r.application_id != "" {
			req.SetBasicAuth(r.application_id, r.application_secret)
		} else {
			token, err = c.getAccessToken(ctx)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := c.conf.HttpClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && r.application_id == "" && !refreshed {
			resp.Body.Close()
			c.accessTokenLock.Lock()
			if c.accessToken == token {
				c.accessToken = ""
			}
			c.accessTokenLock.Unlock()
			refreshed = true
			continue
		}
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout
		if r.method != http.MethodGet || !retryable || readRetries >= 2 {
			return resp, nil
		}
		delay := readRetryDelay(resp.Header.Get("Retry-After"), readRetries, time.Now())
		resp.Body.Close()
		readRetries++
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func readRetryDelay(header string, attempt int, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
		// Clamp before multiplication so large headers cannot overflow Duration.
		return time.Duration(min(seconds, 30)) * time.Second
	}
	if date, err := http.ParseTime(header); err == nil {
		return min(max(date.Sub(now), 0), 30*time.Second)
	}
	return time.Duration(100*(1<<attempt)) * time.Millisecond
}

// HTTPError exposes status for recovery logic without including response bodies.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string { return fmt.Sprintf("Logto API returned HTTP %d", e.StatusCode) }
func IsNotFound(err error) bool {
	var e *HTTPError
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

// discard closes every success response and treats already absent deletes as success.
func (c *Client) discard(ctx context.Context, r *request, codes ...int) error {
	res, err := expect(codes...)(c.do(ctx, r))
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
	return err
}

func decode(r io.ReadCloser, out any) error {
	defer r.Close()
	dec := json.NewDecoder(r)
	if err := dec.Decode(out); err != nil {
		return err
	}
	switch model := out.(type) {
	case *ApplicationModel:
		if !validID(model.ID) {
			return errors.New("application response missing ID")
		}
	case *ApiResourceModel:
		if !validID(model.ID) {
			return errors.New("API resource response missing ID")
		}
	case *UserModel:
		if !validID(model.ID) {
			return errors.New("user response missing ID")
		}
	case *RoleModel:
		if !validID(model.ID) {
			return errors.New("role response missing ID")
		}
	case *ScopeModel:
		if !validID(model.ID) {
			return errors.New("scope response missing ID")
		}
	case *[]ScopeModel:
		if *model == nil {
			return errors.New("scope list response must be a JSON array")
		}
		for _, row := range *model {
			if !validID(row.ID) {
				return errors.New("response contains an invalid identifier")
			}
		}
	case *[]RoleModel:
		if *model == nil {
			return errors.New("role list response must be a JSON array")
		}
		for _, row := range *model {
			if !validID(row.ID) {
				return errors.New("response contains an invalid identifier")
			}
		}
	case *[]Secret:
		if *model == nil {
			return errors.New("secret list response must be a JSON array")
		}
	case *[]ApiResourceModel:
		if *model == nil {
			return errors.New("resource list response must be a JSON array")
		}
		for _, row := range *model {
			if !validID(row.ID) {
				return errors.New("response contains an invalid identifier")
			}
		}
	case *Secret:
		if model.Name == "" || model.Value == "" {
			return errors.New("secret response missing name or value")
		}
	}
	return nil
}

func expect(codes ...int) func(*http.Response, error) (*http.Response, error) {
	return func(res *http.Response, err error) (*http.Response, error) {
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, errors.New("logto API returned no response")
		}
		if !slices.Contains(codes, res.StatusCode) {
			if res.Body != nil {
				res.Body.Close()
			}
			return nil, &HTTPError{StatusCode: res.StatusCode}
		}
		return res, nil
	}
}

// Reject ambiguous path segments before callers normalize paths with path.Join.
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/\\%?#\r\n")
}
func validSecretName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\r\n")
}
