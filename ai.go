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
// items of ONE category ("vocabulary" or "expression"), avoiding the items
// listed in `avoid` (a sample of what's already in the pool, so refills add
// genuinely new content instead of re-generating classics). Returns
// (nil, nil) — not an error — when no API key is configured, so callers can
// treat "no AI" as a normal, expected state rather than a failure.
func generateBusinessEnglishBatch(ctx context.Context, category string, count int, avoid []string) ([]generatedPhrase, error) {
	key := anthropicAPIKey(ctx)
	if key == "" {
		return nil, nil
	}

	client := anthropic.NewClient(option.WithAPIKey(key))

	kind := `"expression": full workplace sentences (e.g. "Let's circle back on this next week.")`
	if category == "vocabulary" {
		kind = `"vocabulary": single business terms or short collocations (e.g. "stakeholder", "touch base")`
	}
	avoidClause := ""
	if len(avoid) > 0 {
		avoidClause = "\n\nDo NOT generate any of these (already in the app):\n- " + strings.Join(avoid, "\n- ")
	}

	prompt := fmt.Sprintf(`Generate exactly %d NEW items for a workplace/business English learning app aimed at Korean professionals.

Every item must be of this one kind — %s. Every item needs a natural, accurate Korean translation.
The Korean translation must be a REAL Korean gloss, never a transliteration of the English word
(e.g. "synergy" must be translated as 상승 효과, NOT 시너지 — a transliterated answer makes the quiz trivial).
Prefer varied, less-common but genuinely useful real workplace situations: meetings, email, negotiation,
project status, hiring, finance, sales, presentations, small talk with colleagues.%s

Respond with ONLY a JSON array, no prose, no markdown code fences, in exactly this shape:
[{"english": "...", "korean": "...", "category": %q}]`, count, kind, avoidClause, category)

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

// ensureFreshContent generates a new batch for one category when this user
// has fewer unseen phrases left in it than `need` — the moment repeats would
// otherwise start appearing in their sessions. Runs synchronously inside
// quiz-session creation (the frontend shows "Preparing your quiz..." during
// the ~5-10s a generation takes) so the very session being started already
// benefits. Best-effort: any failure just means this session pads with
// repeats, exactly like before.
func ensureFreshContent(ctx context.Context, email, category string, need int) {
	var unseen int
	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM phraseup.phrases p
		WHERE p.category = $1 AND NOT EXISTS (
			SELECT 1 FROM phraseup.quiz_questions q WHERE q.email = $2 AND q.phrase_id = p.id
		)
	`, category, email).Scan(&unseen); err != nil {
		log.Printf("ai: count unseen: %v", err)
		return
	}
	if unseen >= need {
		return
	}

	// Give the model a sample of existing items so it steers away from them.
	avoid := []string{}
	rows, err := db.Query(ctx, `
		SELECT english_text FROM phraseup.phrases WHERE category = $1 ORDER BY id DESC LIMIT 40
	`, category)
	if err == nil {
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err == nil {
				avoid = append(avoid, text)
			}
		}
		rows.Close()
	}

	items, err := generateBusinessEnglishBatch(ctx, category, aiGenerationBatchSize, avoid)
	if err != nil {
		log.Printf("ai: generate batch: %v", err)
		return
	}
	if len(items) == 0 {
		return // no API key configured — fine, sessions pad with repeats
	}

	inserted := 0
	for _, item := range items {
		english := strings.TrimSpace(item.English)
		korean := strings.TrimSpace(item.Korean)
		if english == "" || korean == "" {
			continue
		}
		tag, err := db.Exec(ctx, `
			INSERT INTO phraseup.phrases (english_text, korean_text, category, source)
			VALUES ($1, $2, $3, 'ai')
			ON CONFLICT (english_text) DO NOTHING
		`, english, korean, category)
		if err != nil {
			log.Printf("ai: insert generated phrase: %v", err)
			continue
		}
		inserted += int(tag.RowsAffected())
	}
	log.Printf("ai: generated %d new %s items (user had %d unseen, needed %d)", inserted, category, unseen, need)
}
