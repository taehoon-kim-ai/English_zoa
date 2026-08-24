package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/gin-gonic/gin"
)

// ── section: translator — 3rd main nav tab. Detects Korean vs English,
// translates to the other language, and separately rewrites the phrase into
// polished, professional business English (not a literal translation — an
// upgrade even when the input is already English). Uses the same Anthropic
// client/key as ai.go's content generation.

type translateResult struct {
	DetectedLang    string `json:"detected_lang"`
	Translation     string `json:"translation"`
	BusinessVersion string `json:"business_version"`
}

var reJSONObject = regexp.MustCompile(`(?s)\{.*\}`)

func translateText(ctx context.Context, text string) (translateResult, error) {
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

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     "claude-opus-5",
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
