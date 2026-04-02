package brain2

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	httpClient = &http.Client{Timeout: 15 * time.Second}
	userAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	tagStripper = regexp.MustCompile(`<[^>]*>`)
	wsCollapser = regexp.MustCompile(`\s+`)
)

// SearchGoogle performs a direct Google scrape without any API key.
// Returns a formatted list of search results with titles, URLs, and snippets.
func SearchGoogle(query string, numResults int) (string, error) {
	if numResults <= 0 {
		numResults = 5
	}
	if numResults > 10 {
		numResults = 10
	}

	searchURL := fmt.Sprintf("https://www.google.com/search?q=%s&num=%d&hl=en", url.QueryEscape(query), numResults)

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("google request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500_000))
	if err != nil {
		return "", err
	}
	html := string(body)

	// Extract search results from Google's HTML
	results := extractGoogleResults(html, numResults)
	if len(results) == 0 {
		return "", fmt.Errorf("no results found (Google may have blocked the request)")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Google search results for \"%s\":\n\n", query))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   %s\n   %s\n\n", i+1, r.title, r.url, r.snippet))
	}
	return sb.String(), nil
}

type searchResult struct {
	title   string
	url     string
	snippet string
}

func extractGoogleResults(html string, max int) []searchResult {
	var results []searchResult

	// Google wraps each result in <div class="tF2Cxc"> or similar.
	// We look for <a href="/url?q=..." patterns which are the actual result links.
	// Then extract nearby text for title and snippet.

	// Strategy: find all href="/url?q=" links (Google's redirect format)
	linkPattern := regexp.MustCompile(`href="/url\?q=([^&"]+)`)
	matches := linkPattern.FindAllStringSubmatch(html, max*3)

	seen := make(map[string]bool)
	for _, m := range matches {
		if len(results) >= max {
			break
		}
		rawURL, err := url.QueryUnescape(m[1])
		if err != nil {
			continue
		}
		// Skip Google's own URLs
		if strings.Contains(rawURL, "google.com") || strings.Contains(rawURL, "youtube.com/results") {
			continue
		}
		if seen[rawURL] {
			continue
		}
		seen[rawURL] = true

		results = append(results, searchResult{
			title:   extractDomain(rawURL),
			url:     rawURL,
			snippet: "",
		})
	}

	// If redirect-style links not found, try direct href patterns
	if len(results) == 0 {
		directPattern := regexp.MustCompile(`<a href="(https?://[^"]+)"[^>]*>([^<]+)</a>`)
		matches := directPattern.FindAllStringSubmatch(html, max*5)
		for _, m := range matches {
			if len(results) >= max {
				break
			}
			link := m[1]
			title := strings.TrimSpace(m[2])
			if strings.Contains(link, "google.com") || title == "" {
				continue
			}
			if seen[link] {
				continue
			}
			seen[link] = true
			results = append(results, searchResult{
				title:   title,
				url:     link,
				snippet: "",
			})
		}
	}

	return results
}

// FetchAndExtract fetches a URL and extracts readable text content.
// Uses a simple paragraph + heading extraction approach (similar to Hermes).
func FetchAndExtract(targetURL string, maxChars int) (string, error) {
	if maxChars <= 0 {
		maxChars = 10000
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, targetURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1_000_000))
	if err != nil {
		return "", err
	}
	html := string(body)

	// Extract title
	title := extractBetween(html, "<title", "</title>")

	// Extract text from <p>, <h1>-<h6>, <li>, <td> tags
	text := extractReadableText(html)

	if len(text) > maxChars {
		text = text[:maxChars] + "\n[...truncated]"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Content from %s", targetURL))
	if title != "" {
		sb.WriteString(fmt.Sprintf(" — %s", title))
	}
	sb.WriteString(":\n\n")
	sb.WriteString(text)
	return sb.String(), nil
}

func extractReadableText(html string) string {
	// Remove script, style, nav, footer, header tags and their contents
	for _, tag := range []string{"script", "style", "nav", "footer", "header", "noscript", "svg", "form"} {
		pattern := regexp.MustCompile(`(?is)<` + tag + `[^>]*>.*?</` + tag + `>`)
		html = pattern.ReplaceAllString(html, " ")
	}

	// Extract text from content tags
	var parts []string
	contentPattern := regexp.MustCompile(`(?is)<(p|h[1-6]|li|td|th|blockquote|figcaption|summary|dt|dd)[^>]*>(.*?)</\1>`)
	matches := contentPattern.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		text := tagStripper.ReplaceAllString(m[2], " ")
		text = wsCollapser.ReplaceAllString(strings.TrimSpace(text), " ")
		if len(text) > 20 { // skip tiny fragments
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n")
}

func extractBetween(html, startTag, endTag string) string {
	lower := strings.ToLower(html)
	start := strings.Index(lower, strings.ToLower(startTag))
	if start == -1 {
		return ""
	}
	// Find the end of the opening tag
	tagEnd := strings.Index(html[start:], ">")
	if tagEnd == -1 {
		return ""
	}
	contentStart := start + tagEnd + 1
	end := strings.Index(lower[contentStart:], strings.ToLower(endTag))
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(html[contentStart : contentStart+end])
}

func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}
