package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // embed IANA tz DB — distroless/static has no /usr/share/zoneinfo

	"github.com/gin-gonic/gin"
)

//go:embed all:web
var webFS embed.FS

var seoulTZ *time.Location

func init() {
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		log.Fatalf("load Asia/Seoul: %v", err)
	}
	seoulTZ = loc
}

// isCloudRun returns true when running on Apps Platform (Cloud Run).
func isCloudRun() bool { return os.Getenv("K_SERVICE") != "" }

// iapEmail extracts the verified user email IAP injects on every web request.
// Empty when running locally without IAP.
func iapEmail(c *gin.Context) string {
	return strings.TrimPrefix(c.GetHeader("X-Goog-Authenticated-User-Email"), "accounts.google.com:")
}

// currentEmail resolves identity: the IAP header in production, or a
// DEV_USER_EMAIL env fallback for local development without IAP.
func currentEmail(c *gin.Context) string {
	if email := iapEmail(c); email != "" {
		return email
	}
	if !isCloudRun() {
		return strings.TrimSpace(os.Getenv("DEV_USER_EMAIL"))
	}
	return ""
}

func requireEmail(c *gin.Context) (string, bool) {
	email := currentEmail(c)
	if email == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no authenticated user — set DEV_USER_EMAIL locally"})
		return "", false
	}
	return email, true
}

func main() {
	appCtx := context.Background()

	// Scoring/leaderboard is the whole point of this app, so unlike a
	// dashboard with live-fetch fallbacks, we fail fast without a DB.
	if err := initDB(appCtx); err != nil {
		log.Fatalf("DB unavailable: %v", err)
	}

	r := gin.Default()

	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	r.GET("/api/me", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "profile unavailable"})
			return
		}
		streak, err := recordLogin(ctx, email, time.Now())
		if err != nil {
			log.Printf("recordLogin: %v", err)
		}
		profile, err := getUser(ctx, email)
		if err != nil {
			log.Printf("getUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "profile unavailable"})
			return
		}
		score, err := getScore(ctx, email)
		if err != nil {
			log.Printf("getScore: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{
			"email":          email,
			"nickname":       profile.Nickname,
			"status_message": profile.StatusMessage,
			"streak":         streak,
			"score":          score,
		})
	})

	r.POST("/api/profile", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			Nickname      string `json:"nickname"`
			StatusMessage string `json:"status_message"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
			return
		}
		if err := updateProfile(ctx, email, body.Nickname, body.StatusMessage); err != nil {
			log.Printf("updateProfile: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.GET("/api/calendar", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		events, err := getLoginHistory(c.Request.Context(), email, 90)
		if err != nil {
			log.Printf("getLoginHistory: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "calendar unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"events": events})
	})

	r.GET("/api/phrase/today", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		phrase, err := ensureTodayPhrase(ctx)
		if err != nil {
			log.Printf("ensureTodayPhrase: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "phrase unavailable"})
			return
		}
		attempt, err := getAttempt(ctx, email, phrase.ID)
		if err != nil {
			log.Printf("getAttempt: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{"phrase": phrase, "attempt": attempt})
	})

	r.POST("/api/phrase/attempt", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			PhraseID int    `json:"phrase_id"`
			Result   string `json:"result"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || (body.Result != "known" && body.Result != "unknown") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "attempt failed"})
			return
		}
		scoreDelta, err := recordAttempt(ctx, email, body.PhraseID, body.Result)
		if err != nil {
			log.Printf("recordAttempt: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "attempt failed"})
			return
		}
		score, err := getScore(ctx, email)
		if err != nil {
			log.Printf("getScore: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "score_delta": scoreDelta, "score": score})
	})

	r.GET("/api/leaderboard", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		rows, err := getLeaderboard(c.Request.Context(), 50)
		if err != nil {
			log.Printf("getLeaderboard: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "leaderboard unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"leaderboard": rows})
	})

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	r.NoRoute(func(c *gin.Context) {
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("English_zoa serving on :%s\n", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
