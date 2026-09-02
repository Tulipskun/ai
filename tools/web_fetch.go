package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	defaultWebFetchResponseBytes = 2 << 20
	defaultWebFetchTextChars     = 200000
	defaultWebFetchTimeout       = 30 * time.Second
	defaultWebFetchMaxRedirects  = 5
)

type webFetchArgs struct {
	URL string `json:"url"`
}

type webFetchResult struct {
	URL         string `json:"url"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Title       string `json:"title,omitempty"`
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
}

func newWebFetchTool(policy *NetworkPolicy) handler {
	return newWebFetchToolWithLimits(policy, defaultWebFetchResponseBytes, defaultWebFetchTextChars, defaultWebFetchTimeout, defaultWebFetchMaxRedirects)
}

func newWebFetchToolWithLimits(policy *NetworkPolicy, maxBytes, maxTextChars int64, timeout time.Duration, maxRedirects int) handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args webFetchArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		u, err := normalizeURL(args.URL)
		if err != nil {
			return "", err
		}
		if err := policy.ValidateURL(ctx, u); err != nil {
			return "", err
		}
		if maxBytes <= 0 {
			maxBytes = defaultWebFetchResponseBytes
		}
		if maxTextChars <= 0 {
			maxTextChars = defaultWebFetchTextChars
		}
		if timeout <= 0 {
			timeout = defaultWebFetchTimeout
		}
		if maxRedirects < 0 {
			maxRedirects = defaultWebFetchMaxRedirects
		}

		client := &http.Client{Timeout: timeout}
		client = policy.ValidateAndRoundTrip(ctx, client)
		redirects := 0
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			redirects++
			if redirects > maxRedirects {
				return errors.New("maximum redirect count exceeded")
			}
			if err := policy.ValidateURL(req.Context(), req.URL); err != nil {
				return err
			}
			return nil
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "Tulipskun-ai-web-fetch/1.0")
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("fetch %s: %w", u.String(), err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		if err != nil {
			return "", err
		}
		truncated := int64(len(body)) > maxBytes
		if truncated {
			body = body[:maxBytes]
		}

		contentType := resp.Header.Get("Content-Type")
		text, title := extractWebText(string(body), contentType)
		if int64(len(text)) > maxTextChars {
			text = text[:maxTextChars]
			truncated = true
		}
		result := webFetchResult{
			URL:         resp.Request.URL.String(),
			Status:      resp.StatusCode,
			ContentType: contentType,
			Title:       title,
			Text:        text,
			Truncated:   truncated,
		}
		data, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

var (
	scriptStyleRE = regexp.MustCompile(`(?is)<(script|style|noscript|template)[^>]*>.*?</\1>`)
	commentRE     = regexp.MustCompile(`(?s)<!--.*?-->`)
	tagRE         = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRE       = regexp.MustCompile(`[\t\r\n ]+`)
	titleRE       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

func extractWebText(body, contentType string) (string, string) {
	if strings.Contains(strings.ToLower(contentType), "html") {
		title := ""
		if match := titleRE.FindStringSubmatch(body); len(match) == 2 {
			title = strings.TrimSpace(html.UnescapeString(tagRE.ReplaceAllString(match[1], " ")))
		}
		text := scriptStyleRE.ReplaceAllString(body, " ")
		text = commentRE.ReplaceAllString(text, " ")
		text = tagRE.ReplaceAllString(text, " ")
		text = html.UnescapeString(text)
		text = spaceRE.ReplaceAllString(text, " ")
		return strings.TrimSpace(text), title
	}
	return strings.TrimSpace(body), ""
}

func webFetchURL(raw string) (*url.URL, error) {
	return normalizeURL(raw)
}
