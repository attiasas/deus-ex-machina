package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type WebSearch struct{}

func (WebSearch) Name() string { return "web_search" }
func (WebSearch) Description() string {
	return "Search the web via DuckDuckGo and return results with titles, snippets, and URLs."
}
func (WebSearch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query"}
		},
		"required": ["query"]
	}`)
}

var (
	reDDGTitle   = regexp.MustCompile(`(?s)class="result__a"[^>]*>(.*?)</a>`)
	reDDGURL     = regexp.MustCompile(`class="result__url"[^>]*href="(https?://[^"]+)"`)
	reDDGSnippet = regexp.MustCompile(`(?s)class="result__snippet"[^>]*>(.*?)</a>`)
	reHTMLTag    = regexp.MustCompile(`<[^>]*>`)
)

func (WebSearch) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	body := "q=" + url.QueryEscape(params.Query)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://html.duckduckgo.com/html/", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	html := string(raw)

	titles := reDDGTitle.FindAllStringSubmatch(html, 10)
	urls := reDDGURL.FindAllStringSubmatch(html, 10)
	snippets := reDDGSnippet.FindAllStringSubmatch(html, 10)

	var sb strings.Builder
	for i := range titles {
		title := strings.TrimSpace(reHTMLTag.ReplaceAllString(titles[i][1], ""))
		snippet := ""
		if i < len(snippets) {
			snippet = strings.TrimSpace(reHTMLTag.ReplaceAllString(snippets[i][1], ""))
		}
		link := ""
		if i < len(urls) {
			link = strings.TrimSpace(urls[i][1])
		}
		if title == "" {
			continue
		}
		fmt.Fprintf(&sb, "%s\n", title)
		if snippet != "" {
			fmt.Fprintf(&sb, "%s\n", snippet)
		}
		if link != "" {
			fmt.Fprintf(&sb, "%s\n", link)
		}
		sb.WriteString("\n")
	}

	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "no results found", nil
	}
	return out, nil
}
