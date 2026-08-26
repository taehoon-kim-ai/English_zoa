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
	"time"

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

// Table DDL for translation_cache lives in migrations.go.

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

// translateDailyCap bounds each user's Claude calls per (Seoul) day —
// team-wide cache hits are free and don't count. Postgres-backed counter
// (phraseup.translate_usage) so the cap holds across Cloud Run instances.
const translateDailyCap = 50

var errTranslateCapped = errors.New("daily translation limit reached — try again tomorrow")

// bumpTranslateUsage counts one API-bound call and reports whether the user
// is still under the cap. Counting before the call (not after success) means
// a failing API can't be hammered either.
func bumpTranslateUsage(ctx context.Context, email string) error {
	var calls int
	err := db.QueryRow(ctx, `
		INSERT INTO phraseup.translate_usage (email, usage_date, calls) VALUES ($1, $2, 1)
		ON CONFLICT (email, usage_date) DO UPDATE SET calls = translate_usage.calls + 1
		RETURNING calls
	`, email, time.Now().In(seoulTZ).Format("2006-01-02")).Scan(&calls)
	if err != nil {
		return err
	}
	if calls > translateDailyCap {
		return errTranslateCapped
	}
	return nil
}

func translateText(ctx context.Context, email, text string) (translateResult, error) {
	hash := translateInputHash(text)
	if cached, ok := lookupTranslationCache(ctx, hash); ok {
		return cached, nil
	}
	if err := bumpTranslateUsage(ctx, email); err != nil {
		return translateResult{}, err
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

	// The fast/cheap model, deliberately — see the cost notes at the top of
	// this file.
	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     aiFastModel,
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
		email, ok := requireEmail(c)
		if !ok {
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

		result, err := translateText(c.Request.Context(), email, text)
		if err != nil {
			if errors.Is(err, errTranslateCapped) {
				c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
				return
			}
			log.Printf("translateText: %v", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})
}
