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

// Bootstrap only. Each feature lives in its own file — profile.go, quiz.go,
// score.go (phrase.go is a shared phrase-sourcing pipeline, not its own
// screen) — so two people can work on separate sections without touching
// this one: main() just wires DB init + each section's register*Routes(r) +
// static file serving.

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
	go touchLastActive(email) // best-effort presence signal for the main page's team panel (profile.go)
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

	registerProfileRoutes(r)
	registerQuizRoutes(r)
	registerLeaderboardRoutes(r)
	registerTedTalkRoutes(r)
	registerTranslateRoutes(r)
	registerNewsRoutes(r)
	registerStatsRoutes(r)
	registerBattleRoutes(r)
	registerWeeklyRoutes(r)
	registerWordbookRoutes(r)

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
	log.Printf("PhraseUp serving on :%s\n", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
