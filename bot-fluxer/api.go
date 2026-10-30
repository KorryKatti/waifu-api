package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type apiError struct {
	Status int
	Detail string
}

func (e *apiError) Error() string {
	return e.Detail
}

type apiClient struct {
	cfg    *config
	state  *appState
	client *http.Client
}

func newAPIClient(cfg *config, state *appState) *apiClient {
	return &apiClient{
		cfg:    cfg,
		state:  state,
		client: &http.Client{},
	}
}

func buildURL(base, path string, params url.Values) string {
	target := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	return target
}

func (c *apiClient) do(ctx context.Context, method, target string, payload any, headers map[string]string) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

func (c *apiClient) getJSON(base, path string, params url.Values, timeout time.Duration) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	started := time.Now()
	status, data, err := c.do(ctx, http.MethodGet, buildURL(base, path, params), nil, nil)
	if err != nil {
		c.state.noteError()
		return nil, fmt.Errorf("could not reach the waifu API: %w", err)
	}
	if status >= 400 {
		c.state.noteError()
		return nil, &apiError{Status: status, Detail: apiProblemDetail(status, data)}
	}
	c.state.noteOK(time.Since(started))

	var body map[string]any
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("the API answered with invalid JSON: %w", err)
	}
	return body, nil
}

func (c *apiClient) ping() (time.Duration, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	defer cancel()

	status, data, err := c.do(ctx, http.MethodGet, buildURL(c.cfg.apiBase, "ping", nil), nil, nil)
	if err != nil {
		c.state.noteError()
		return 0, err
	}
	if status >= 400 {
		c.state.noteError()
		return 0, &apiError{Status: status, Detail: apiProblemDetail(status, data)}
	}
	latency := time.Since(start)
	c.state.noteOK(latency)
	return latency, nil
}

func (c *apiClient) registerInstance(rawURL, name, userID string) (int, map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), registerTimeout)
	defer cancel()

	payload := map[string]any{
		"url":           rawURL,
		"name":          name,
		"discordUserId": userID,
	}
	headers := map[string]string{"X-Api-Key": c.cfg.adminKey}
	started := time.Now()
	status, data, err := c.do(ctx, http.MethodPost, buildURL(c.cfg.registerBase, "instances", nil), payload, headers)
	if err != nil {
		c.state.noteError()
		return 0, nil, err
	}

	var body map[string]any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &body)
	}
	if status >= 400 {
		c.state.noteError()
		return status, body, &apiError{Status: status, Detail: apiProblemDetail(status, data)}
	}
	c.state.noteOK(time.Since(started))
	return status, body, nil
}

func apiProblemDetail(status int, data []byte) string {
	var problem struct {
		Detail  string `json:"detail"`
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &problem); err == nil {
		for _, text := range []string{problem.Detail, problem.Title, problem.Message} {
			if strings.TrimSpace(text) != "" {
				return text
			}
		}
	}

	text := strings.TrimSpace(string(data))
	if text != "" && len(text) < 300 && !strings.HasPrefix(text, "{") {
		return text
	}
	return fmt.Sprintf("The waifu API returned HTTP %d.", status)
}
