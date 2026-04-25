package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type WebFetch struct{}

func (WebFetch) Name() string        { return "web_fetch" }
func (WebFetch) Description() string { return "Fetch a URL and return its text content." }
func (WebFetch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "URL to fetch"}
		},
		"required": ["url"]
	}`)
}

func (WebFetch) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, params.URL, nil)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; deus-ex-machina/1.0)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, params.URL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return "", err
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		return htmlToText(string(body)), nil
	}
	return string(body), nil
}

var (
	reBlockTag = regexp.MustCompile(`(?i)<(br|p|div|h[1-6]|li|tr|td|th)[^>]*>`)
	reAnyTag   = regexp.MustCompile(`<[^>]*>`)
	reSpaces   = regexp.MustCompile(`[ \t]{2,}`)
	reBlank3   = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(html string) string {
	for _, tag := range []string{"script", "style", "head", "nav", "footer"} {
		re := regexp.MustCompile(`(?is)<` + tag + `[^>]*>.*?</` + tag + `>`)
		html = re.ReplaceAllString(html, "")
	}
	html = reBlockTag.ReplaceAllString(html, "\n")
	html = reAnyTag.ReplaceAllString(html, "")
	for old, new := range map[string]string{
		"&amp;": "&", "&lt;": "<", "&gt;": ">",
		"&nbsp;": " ", "&#39;": "'", "&quot;": `"`,
	} {
		html = strings.ReplaceAll(html, old, new)
	}
	html = reSpaces.ReplaceAllString(html, " ")
	html = reBlank3.ReplaceAllString(html, "\n\n")
	return strings.TrimSpace(html)
}
