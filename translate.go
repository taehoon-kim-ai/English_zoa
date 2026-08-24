package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: translator — 3rd main nav tab. Detects Korean vs English,
// translates to the other language, and separately rewrites the phrase into
// polished, professional business English. Uses the same Anthropic key as
// ai.go's content generation.
//
// Token-cost controls (this endpoint is the app's only per-user-action AI
// call, so it's the one worth being careful with):
//   - Model is Haiku, not Opus — short-phrase translation doesn't need a
//     frontier model, and Haiku is ~5x cheaper per token.
//   - Results are cached server-side by normalized-input hash, so repeat
//     translations of the same text (by anyone on the team) cost zero API
//     calls. Owns phraseup.translation_cache.
//   - The frontend only calls on an explicit action (button/Enter), never
//     automatically while typing (translate.jsx).

var translateSchemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS phraseup.translation_cache (
		input_hash       TEXT PRIMARY KEY,
		input_text       TEXT NOT NULL,
		detected_lang    TEXT NOT NULL,
		translation      TEXT NOT NULL,
		business_version TEXT NOT NULL,
		created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
}

type translateResult struct {
	DetectedLang    string `json:"detected_lang"`
	Translation     string `json:"translation"`
	BusinessVersion string `json:"business_version"`
	Cached          bool   `json:"cached"`
}

var reJSONObject = regexp.MustCompile(`(?s)\{.*\}`)

// normalizeTranslateInput folds trivial differences (case, whitespace runs)
// so near-identical inputs share a cache entry.
func normalizeTranslateInput(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func translateInputHash(text string) string {
	sum := sha256.Sum256([]byte(normalizeTranslateInput(text)))
	return fmt.Sprintf("%x", sum)
}

func lookupTranslationCache(ctx context.Context, hash string) (translateResult, bool) {
	var r translateResult
	err := db.QueryRow(ctx, `
		SELECT detected_lang, translation, business_version
		FROM phraseup.translation_cache WHERE input_hash = $1
	`, hash).Scan(&r.DetectedLang, &r.Translation, &r.BusinessVersion)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("translation cache lookup: %v", err)
		}
		return translateResult{}, false
	}
	r.Cached = true
	return r, true
}

func saveTranslationCache(ctx context.Context, hash, text string, r translateResult) {
	if _, err := db.Exec(ctx, `
		INSERT INTO phraseup.translation_cache (input_hash, input_text, detected_lang, translation, business_version)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (input_hash) DO NOTHING
	`, hash, text, r.DetectedLang, r.Translation, r.BusinessVersion); err != nil {
		log.Printf("translation cache save: %v", err)
	}
}

func translateText(ctx context.Context, text string) (translateResult, error) {
	hash := translateInputHash(text)
	if cached, ok := lookupTranslationCache(ctx, hash); ok {
		return cached, nil
	}

	key := anthropicAPIKey(ctx)
	if key == "" {
		return translateResult{}, fmt.Errorf("translator unavailable — no ANTHROPIC_API_KEY configured")
	}

	client := anthropic.NewClient(option.WithAPIKey(key))

	prompt := fmt.Sprintf(`You are a Korean<->English business translator.

Input text: %q

1. Detect whether the input is primarily Korean or English.
2. If Korean, translate it to natural English. If English, translate it to natural Korean.
3. Separately, write a "business_version": a polished, professional BUSINESS ENGLISH rendering
   of the phrase — not a literal translation, an upgrade. If the input was already English,
   rephrase it to sound more professional/workplace-appropriate. If the input was Korean, base
   the business_version on its English meaning, phrased professionally.

Respond with ONLY a JSON object, no prose, no markdown fences, in exactly this shape:
{"detected_lang": "ko", "translation": "...", "business_version": "..."}`, text)

	// Haiku, deliberately — see the cost notes at the top of this file.
	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     "claude-haiku-4-5",
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return translateResult{}, fmt.Errorf("anthropic request: %w", err)
	}

	var raw strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			raw.WriteString(tb.Text)
		}
	}
	match := reJSONObject.FindString(raw.String())
	if match == "" {
		return translateResult{}, fmt.Errorf("no JSON object in response: %s", raw.String())
	}

	var result translateResult
	if err := json.Unmarshal([]byte(match), &result); err != nil {
		return translateResult{}, fmt.Errorf("parse translation: %w", err)
	}
	saveTranslationCache(ctx, hash, text, result)
	return result, nil
}

func registerTranslateRoutes(r *gin.Engine) {
	r.POST("/api/translate", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		var body struct {
			Text string `json:"text"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		text := strings.TrimSpace(body.Text)
		if text == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "text is required"})
			return
		}
		if r := []rune(text); len(r) > 500 {
			text = string(r[:500])
		}

		result, err := translateText(c.Request.Context(), text)
		if err != nil {
			log.Printf("translateText: %v", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})
}
