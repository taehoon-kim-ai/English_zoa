package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "google.golang.org/genproto/googleapis/cloud/secretmanager/v1"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ── AI-generated business English content ──────────────────────────────────
// Tops up the phrase pool (phrase.go) with a batch of AI-generated business
// English vocabulary + expression items whenever it's running low, so the
// quiz never runs dry even without a steady Slack feed. Key lookup mirrors
// MADANG's Secret Manager pattern (env var locally, Secret Manager in prod).
// Best-effort throughout: any failure here just means quiz.go falls back to
// the static fallbackPhrases list (phrase.go) — nothing depends on this file
// succeeding.

const (
	anthropicAPIKeySecretName  = "phraseup-anthropic-api-key"
	anthropicAPIKeyCacheMaxAge = 10 * time.Minute
	aiGenerationBatchSize      = 20
	aiPoolLowWatermark         = 15 // top up whenever fewer than this many phrases exist
)

var (
	anthropicAPIKeyMu       sync.Mutex
	cachedAnthropicAPIKey   string
	cachedAnthropicAPIKeyAt time.Time
)

// anthropicAPIKey returns the API key from env (local dev), else Secret
// Manager (prod), caching the secret lookup for anthropicAPIKeyCacheMaxAge.
func anthropicAPIKey(ctx context.Context) string {
	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		return key
	}
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		return ""
	}

	anthropicAPIKeyMu.Lock()
	defer anthropicAPIKeyMu.Unlock()
	if cachedAnthropicAPIKey != "" && time.Since(cachedAnthropicAPIKeyAt) < anthropicAPIKeyCacheMaxAge {
		return cachedAnthropicAPIKey
	}

	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		log.Printf("ai: secret manager client unavailable: %v", err)
		return ""
	}
	defer client.Close()

	secretPath := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, anthropicAPIKeySecretName)
	res, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: secretPath})
	if err != nil {
		log.Printf("ai: anthropic key secret unavailable: %v", err)
		return ""
	}
	key := strings.TrimSpace(string(res.Payload.Data))
	if key == "" {
		return ""
	}
	cachedAnthropicAPIKey = key
	cachedAnthropicAPIKeyAt = time.Now()
	return key
}

type generatedPhrase struct {
	English  string `json:"english"`
	Korean   string `json:"korean"`
	Category string `json:"category"` // "vocabulary" | "expression"
}

var reJSONArray = regexp.MustCompile(`(?s)\[.*\]`)

// generateBusinessEnglishBatch asks Claude for a batch of business-English
// vocabulary + expression items. Returns (nil, nil) — not an error — when no
// API key is configured, so callers can treat "no AI" as a normal, expected
// state rather than a failure.
func generateBusinessEnglishBatch(ctx context.Context, count int) ([]generatedPhrase, error) {
	key := anthropicAPIKey(ctx)
	if key == "" {
		return nil, nil
	}

	client := anthropic.NewClient(option.WithAPIKey(key))

	prompt := fmt.Sprintf(`Generate exactly %d items for a workplace/business English learning app aimed at Korean professionals.

Mix roughly half "vocabulary" (a single business term or short collocation, e.g. "deadline", "touch base") and half
"expression" (a full workplace sentence, e.g. "Let's circle back on this next week."). Every item needs a natural,
accurate Korean translation. Avoid repeating extremely common items like "deadline", "circle back", or "touch base".
Prefer varied real workplace situations: meetings, email, negotiation, project status, hiring, finance, sales.

Respond with ONLY a JSON array, no prose, no markdown code fences, in exactly this shape:
[{"english": "...", "korean": "...", "category": "vocabulary"}, {"english": "...", "korean": "...", "category": "expression"}]`, count)

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     "claude-opus-5",
		MaxTokens: 4096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}

	var raw strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			raw.WriteString(tb.Text)
		}
	}
	match := reJSONArray.FindString(raw.String())
	if match == "" {
		return nil, fmt.Errorf("no JSON array in response: %s", raw.String())
	}

	var items []generatedPhrase
	if err := json.Unmarshal([]byte(match), &items); err != nil {
		return nil, fmt.Errorf("parse generated items: %w", err)
	}
	return items, nil
}

// topUpPhrasePoolIfLow generates a fresh AI batch when the pool is running
// low. Best-effort: logs and returns on any failure so callers (the daily
// quiz build) never block on it.
func topUpPhrasePoolIfLow(ctx context.Context) {
	var total int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM phraseup.phrases`).Scan(&total); err != nil {
		log.Printf("ai: count phrases: %v", err)
		return
	}
	if total >= aiPoolLowWatermark {
		return
	}

	items, err := generateBusinessEnglishBatch(ctx, aiGenerationBatchSize)
	if err != nil {
		log.Printf("ai: generate batch: %v", err)
		return
	}
	if len(items) == 0 {
		return // no API key configured — fine, fallbackPhrases covers it
	}

	inserted := 0
	for _, item := range items {
		english := strings.TrimSpace(item.English)
		korean := strings.TrimSpace(item.Korean)
		if english == "" || korean == "" {
			continue
		}
		category := item.Category
		if category != "vocabulary" && category != "expression" {
			category = "expression"
		}
		tag, err := db.Exec(ctx, `
			INSERT INTO phraseup.phrases (english_text, korean_text, category)
			VALUES ($1, $2, $3)
			ON CONFLICT (english_text) DO NOTHING
		`, english, korean, category)
		if err != nil {
			log.Printf("ai: insert generated phrase: %v", err)
			continue
		}
		inserted += int(tag.RowsAffected())
	}
	log.Printf("ai: topped up phrase pool with %d generated items", inserted)
}
