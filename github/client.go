package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

const graphqlURL = "https://api.github.com/graphql"

// PRInfo 单条 PR 的结构化信息
type PRInfo struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
	MergedAt  string `json:"merged_at"`
	Repo      string `json:"repo"`
	RepoStars int    `json:"repo_stars"`
}

// Client GitHub GraphQL API 客户端
type Client struct {
	token      string
	httpClient *http.Client
	log        zerolog.Logger
}

// NewClient 创建 GitHub GraphQL 客户端
func NewClient(token string, log zerolog.Logger) *Client {
	return &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: log,
	}
}

// FetchAllPRs 获取用户全部 PR（处理分页和限流重试）
// 参考 pr-collector-py/main.py 的 _graphql_request 重试逻辑
func (c *Client) FetchAllPRs(username string) ([]PRInfo, error) {
	const query = `
query($queryString: String!, $cursor: String) {
  search(query: $queryString, type: ISSUE, first: 100, after: $cursor) {
    issueCount
    pageInfo {
      hasNextPage
      endCursor
    }
    edges {
      node {
        ... on PullRequest {
          number
          title
          state
          url
          createdAt
          mergedAt
          repository {
            nameWithOwner
            stargazerCount
          }
        }
      }
    }
  }
  rateLimit {
    remaining
    resetAt
  }
}`

	var allPRs []PRInfo
	var cursor string

	for {
		variables := map[string]interface{}{
			"queryString": fmt.Sprintf(
				"is:pr author:%s archived:false is:merged is:public sort:created-desc",
				username,
			),
		}
		if cursor != "" {
			variables["cursor"] = cursor
		}

		data, err := c.doRequest(query, variables)
		if err != nil {
			return nil, fmt.Errorf("graphql request failed: %w", err)
		}

		search, ok := data["search"].(map[string]interface{})
		if !ok || search == nil {
			return nil, fmt.Errorf("unexpected response: missing search field")
		}

		edges, ok := search["edges"].([]interface{})
		if !ok {
			c.log.Warn().Str("user", username).Msg("search edges is not an array, skipping page")
			break
		}

		for _, edge := range edges {
			edgeMap, ok := edge.(map[string]interface{})
			if !ok || edgeMap == nil {
				continue
			}

			node, ok := edgeMap["node"].(map[string]interface{})
			if !ok || node == nil {
				continue
			}

			repo, ok := node["repository"].(map[string]interface{})
			if !ok || repo == nil {
				continue
			}

			pr, ok := c.parsePRNode(node, repo)
			if !ok {
				c.log.Warn().
					Str("user", username).
					Interface("node", node).
					Msg("failed to parse PR node, skipping")
				continue
			}
			allPRs = append(allPRs, pr)
		}

		pageInfo, ok := search["pageInfo"].(map[string]interface{})
		if !ok || pageInfo == nil {
			break
		}

		hasNext, ok := pageInfo["hasNextPage"].(bool)
		if !ok || !hasNext {
			break
		}

		endCursor, ok := pageInfo["endCursor"].(string)
		if !ok || endCursor == "" {
			break
		}
		cursor = endCursor
	}

	c.log.Info().
		Str("user", username).
		Int("count", len(allPRs)).
		Msg("fetch completed")

	return allPRs, nil
}

// parsePRNode 安全解析单条 PR 节点，返回解析结果和是否成功
func (c *Client) parsePRNode(node, repo map[string]interface{}) (PRInfo, bool) {
	pr := PRInfo{}

	// number (JSON float64 → int)
	if v, ok := node["number"].(float64); ok {
		pr.Number = int(v)
	} else {
		return PRInfo{}, false
	}

	// title
	if v, ok := node["title"].(string); ok {
		pr.Title = v
	} else {
		return PRInfo{}, false
	}

	// state
	if v, ok := node["state"].(string); ok {
		pr.State = v
	}

	// url
	if v, ok := node["url"].(string); ok {
		pr.URL = v
	}

	// createdAt
	if v, ok := node["createdAt"].(string); ok {
		pr.CreatedAt = v
	}

	// mergedAt (nullable)
	if v, ok := node["mergedAt"]; ok && v != nil {
		if s, ok := v.(string); ok {
			pr.MergedAt = s
		}
	}

	// repo nameWithOwner
	if v, ok := repo["nameWithOwner"].(string); ok {
		pr.Repo = v
	}

	// repo stargazerCount
	if v, ok := repo["stargazerCount"].(float64); ok {
		pr.RepoStars = int(v)
	}

	return pr, true
}

const (
	maxWaitSeconds = 60
	maxRetries     = 10
)

func (c *Client) doRequest(query string, variables map[string]interface{}) (map[string]interface{}, error) {
	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodPost, graphqlURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "bearer "+c.token)
		req.Header.Set("User-Agent", "pr-collector/0.1.0")
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.log.Warn().Err(err).Int("attempt", attempt+1).Msg("http request error")
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			c.log.Warn().Err(readErr).Int("attempt", attempt+1).Msg("read response body error")
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		var result map[string]interface{}
		if err := json.Unmarshal(respBody, &result); err != nil {
			c.log.Warn().
				Err(err).
				Int("attempt", attempt+1).
				Str("body", string(respBody[:min(len(respBody), 200)])).
				Msg("unmarshal response error")
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			// 检查 GraphQL errors
			errorsRaw, hasErrors := result["errors"]
			if !hasErrors {
				data, _ := result["data"].(map[string]interface{})
				if data == nil {
					return nil, fmt.Errorf("unexpected response: missing data field")
				}
				return data, nil
			}

			// 安全解析 errors 并检测 rate limit
			errMsg := c.formatGraphQLErrors(errorsRaw)

			waitSeconds, exceeded := c.calcRateLimitWait(result)
			if exceeded {
				return nil, fmt.Errorf("rate limit wait too long: %s", errMsg)
			}

			c.log.Warn().
				Int("attempt", attempt+1).
				Float64("wait_seconds", waitSeconds).
				Str("error", errMsg).
				Msg("graphql error, retrying")
			time.Sleep(time.Duration(waitSeconds+1) * time.Second)
			continue
		}

		// 403 / 429
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			waitSeconds := c.parseRateLimitReset(resp)
			c.log.Warn().
				Int("status", resp.StatusCode).
				Int("attempt", attempt+1).
				Float64("wait_seconds", waitSeconds).
				Msg("rate limited, retrying")
			if waitSeconds > maxWaitSeconds {
				return nil, fmt.Errorf("rate limit wait %ds exceeds max %ds", int(waitSeconds), maxWaitSeconds)
			}
			time.Sleep(time.Duration(waitSeconds+1) * time.Second)
			continue
		}

		return nil, fmt.Errorf("unexpected HTTP %d: %s", resp.StatusCode, string(respBody[:min(len(respBody), 500)]))
	}

	return nil, fmt.Errorf("max retries (%d) exceeded", maxRetries)
}

// formatGraphQLErrors 安全解析 GraphQL errors 字段，拼接错误消息
func (c *Client) formatGraphQLErrors(errorsRaw interface{}) string {
	errors, ok := errorsRaw.([]interface{})
	if !ok {
		// 可能是单个对象或字符串
		return fmt.Sprintf("%v", errorsRaw)
	}

	parts := make([]string, 0, len(errors))
	for _, e := range errors {
		if errMap, ok := e.(map[string]interface{}); ok {
			if msg, ok := errMap["message"].(string); ok {
				parts = append(parts, msg)
			}
		}
	}
	if len(parts) == 0 {
		return "unknown graphql error"
	}
	return fmt.Sprintf("%s", joinStrings(parts, "; "))
}

// parseRateLimitReset 解析 HTTP 429/403 响应中的 x-ratelimit-reset 头
// 参考 pr-collector-py 的限流处理逻辑
func (c *Client) parseRateLimitReset(resp *http.Response) float64 {
	resetTS := resp.Header.Get("x-ratelimit-reset")
	if resetTS == "" {
		return 60 // 默认等待 60s
	}

	ts, err := strconv.ParseInt(resetTS, 10, 64)
	if err != nil {
		return 60
	}

	wait := time.Until(time.Unix(ts, 0)).Seconds()
	if wait < 0 {
		return 0
	}
	return wait
}

func (c *Client) calcRateLimitWait(data map[string]interface{}) (float64, bool) {
	rateLimit, ok := data["rateLimit"].(map[string]interface{})
	if !ok {
		return 60, false
	}
	resetAt, _ := rateLimit["resetAt"].(string)
	if resetAt == "" {
		return 60, false
	}

	resetTime, err := time.Parse(time.RFC3339, resetAt)
	if err != nil {
		return 60, false
	}

	waitSeconds := time.Until(resetTime).Seconds()
	if waitSeconds < 0 {
		waitSeconds = 0
	}
	if waitSeconds > maxWaitSeconds {
		return waitSeconds, true
	}
	return waitSeconds, false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}
