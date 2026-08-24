package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// Data API wiring mirrors the MADANG dashboard's (dataapi.experimental.apps.applied.dev,
// metadata-server IAM token in prod, forwarder SOCKS tunnel locally) since both
// apps run on the same Apps Platform v2. UNCONFIRMED for this app until the
// platform team grants access: the channel ID, the seed user, and the
// user_scopes needed to read #learning-english-with-ai (see README "Slack 연동").
const dataAPIURL = "https://dataapi.experimental.apps.applied.dev"

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func slackChannelID() string     { return strings.TrimSpace(os.Getenv("SLACK_CHANNEL_ID")) }
func slackSeedUserEmail() string { return envOr("SLACK_SEED_USER_EMAIL", "taehoon.kim@applied.co") }

func dataAPIClient() *http.Client {
	socksPort := os.Getenv("SOCKS_PORT")
	if socksPort == "" {
		return &http.Client{Timeout: 15 * time.Second}
	}
	dialer, err := proxy.SOCKS5("tcp", "localhost:"+socksPort, nil, proxy.Direct)
	if err != nil {
		return &http.Client{Timeout: 15 * time.Second}
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		},
		Timeout: 15 * time.Second,
	}
}

// fetchMetadataToken gets an IAM identity token from the GCP metadata server.
func fetchMetadataToken(audience string) (string, error) {
	url := "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity?audience=" + audience + "&format=full"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("metadata token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("metadata token read: %w", err)
	}
	return strings.TrimSpace(string(body)), nil
}

var (
	asyncTokenMu     sync.Mutex
	cachedAsyncToken string
	asyncTokenExpiry time.Time
)

// getAsyncToken mints a scoped Data API token seeded by slackSeedUserEmail(),
// cached for 9 of its 10-minute TTL. A scale-to-zero/cold service has no live
// browser session to mint a request token from, so production always goes
// through this path (see dataAPIGetSlack).
func getAsyncToken() (string, error) {
	asyncTokenMu.Lock()
	defer asyncTokenMu.Unlock()
	if cachedAsyncToken != "" && time.Now().Before(asyncTokenExpiry) {
		return cachedAsyncToken, nil
	}
	iamToken, err := fetchMetadataToken(dataAPIURL)
	if err != nil {
		return "", fmt.Errorf("async-token IAM: %w", err)
	}
	body, _ := json.Marshal(map[string]any{
		"emails":      []string{slackSeedUserEmail()},
		"user_scopes": []string{"channels:history", "channels:read"},
	})
	req, _ := http.NewRequest("POST", dataAPIURL+"/api/async-tokens", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+iamToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("async-token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("async-token: HTTP %d", resp.StatusCode)
	}
	var result struct {
		Tokens []struct {
			RequestToken string `json:"request_token"`
		} `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Tokens) == 0 {
		return "", fmt.Errorf("async-token: empty response")
	}
	cachedAsyncToken = result.Tokens[0].RequestToken
	asyncTokenExpiry = time.Now().Add(9 * time.Minute)
	return cachedAsyncToken, nil
}

func dataAPIGetSlack(path string) (*http.Response, error) {
	if isCloudRun() {
		iamToken, err := fetchMetadataToken(dataAPIURL)
		if err != nil {
			return nil, fmt.Errorf("production auth: %w", err)
		}
		requestToken, err := getAsyncToken()
		if err != nil {
			return nil, fmt.Errorf("production async-token: %w", err)
		}
		req, err := http.NewRequest("GET", dataAPIURL+"/api/data/slack"+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+iamToken)
		req.Header.Set("X-Request-Token", requestToken)
		return (&http.Client{Timeout: 15 * time.Second}).Do(req)
	}

	base := os.Getenv("DATA_API_URL")
	if base == "" {
		return nil, fmt.Errorf("DATA_API_URL not set — run: apps-platform app forwarder --service phraseup")
	}
	req, err := http.NewRequest("GET", base+"/api/data/slack"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("DATA_API_AUTH_TOKEN"))
	req.Header.Set("X-Request-Token", os.Getenv("X_REQUEST_TOKEN"))
	return dataAPIClient().Do(req)
}

func dataAPIReady() bool {
	return isCloudRun() || (os.Getenv("DATA_API_URL") != "" && os.Getenv("X_REQUEST_TOKEN") != "")
}

// fetchPhraseFromSlack looks at today's messages in SLACK_CHANNEL_ID and
// returns the first one that parses into an English/Korean pair. Returns ""
// english when the channel isn't configured, the Data API isn't reachable, or
// nothing today parses — callers fall back to a built-in phrase (db.go).
func fetchPhraseFromSlack(ctx context.Context) (english, korean, slackTS string) {
	channelID := slackChannelID()
	if channelID == "" || !dataAPIReady() {
		return "", "", ""
	}
	oldest := fmt.Sprintf("%d", time.Now().In(seoulTZ).Truncate(24*time.Hour).Unix())
	resp, err := dataAPIGetSlack(fmt.Sprintf("/conversations.history?channel=%s&oldest=%s&limit=20", channelID, oldest))
	if err != nil {
		log.Printf("slack phrase fetch: %v", err)
		return "", "", ""
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", ""
	}
	var hist struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error"`
		Messages []struct {
			Ts   string `json:"ts"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &hist); err != nil || !hist.OK {
		log.Printf("slack phrase fetch: ok=%v err=%v body=%s", hist.OK, err, string(raw))
		return "", "", ""
	}
	// conversations.history returns newest-first; walk oldest-of-today first so
	// the first post of the day wins when there's more than one.
	for i := len(hist.Messages) - 1; i >= 0; i-- {
		m := hist.Messages[i]
		if en, ko, ok := parsePhraseText(m.Text); ok {
			return en, ko, m.Ts
		}
	}
	return "", "", ""
}

var (
	reSlackLinkText = regexp.MustCompile(`<(https?://[^|>]+)\|([^>]+)>`)
	reSlackLink     = regexp.MustCompile(`<(https?://[^>]+)>`)
	reSlackMention  = regexp.MustCompile(`<@[A-Z0-9]+>`)
	reLabelEnglish  = regexp.MustCompile(`(?i)^(?:en(?:glish)?)\s*[:\-]\s*(.+)$`)
	reLabelKorean   = regexp.MustCompile(`(?i)^(?:kr|ko(?:rean)?|한국어|한글)\s*[:\-]\s*(.+)$`)
	reParenTail     = regexp.MustCompile(`^(.+?)\s*[(（]([^()（）]+)[)）]\s*$`)
)

func cleanSlackText(text string) string {
	text = reSlackLinkText.ReplaceAllString(text, "$2")
	text = reSlackLink.ReplaceAllString(text, "") // bare links aren't rendered here — drop rather than leak the raw URL into the phrase text
	text = reSlackMention.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// parsePhraseText tries a few common "English phrase + Korean translation"
// message shapes. The real #learning-english-with-ai format is unconfirmed —
// this couldn't be inspected before the app is registered with Slack access,
// so adjust this once a real message sample is available (see README).
func parsePhraseText(raw string) (english, korean string, ok bool) {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line = cleanSlackText(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "", "", false
	}

	// Labeled lines ("EN: ..." / "KR: ..."), in either order.
	var en, ko string
	for _, line := range lines {
		if m := reLabelEnglish.FindStringSubmatch(line); m != nil {
			en = strings.TrimSpace(m[1])
		}
		if m := reLabelKorean.FindStringSubmatch(line); m != nil {
			ko = strings.TrimSpace(m[1])
		}
	}
	if en != "" && ko != "" {
		return en, ko, true
	}

	// Two plain lines: English then Korean.
	if len(lines) >= 2 {
		return lines[0], lines[1], true
	}

	// Single line "English (한국어)".
	if m := reParenTail.FindStringSubmatch(lines[0]); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), true
	}

	return "", "", false
}
