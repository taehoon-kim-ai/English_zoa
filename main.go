package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"log/slog"
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

// initLogging switches to JSON logs on Cloud Run so Cloud Logging parses
// severity/message natively. slog.SetDefault also reroutes the legacy `log`
// package, so every existing log.Printf becomes structured for free.
// Local dev keeps the default human-readable output.
func initLogging() {
	if !isCloudRun() {
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// Rename to Cloud Logging's special fields.
			switch a.Key {
			case slog.LevelKey:
				a.Key = "severity"
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	})))
}

// requestLogger logs failed (4xx/5xx) and slow requests with the Cloud Trace
// id so they can be correlated with Cloud Run's own request log. Healthy
// fast requests are deliberately not logged — Cloud Run already records
// every request, and the battle screen polls at 1s per player.
func requestLogger() gin.HandlerFunc {
	project := os.Getenv("PROJECT_ID")
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		latency := time.Since(start)
		if status < 400 && latency < time.Second {
			return
		}
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"latency_ms", latency.Milliseconds(),
		}
		// Header format: TRACE_ID/SPAN_ID;o=1 — Cloud Logging wants
		// projects/<id>/traces/<TRACE_ID> under this exact key.
		if tc := c.GetHeader("X-Cloud-Trace-Context"); tc != "" && project != "" {
			traceID, _, _ := strings.Cut(tc, "/")
			attrs = append(attrs, "logging.googleapis.com/trace", "projects/"+project+"/traces/"+traceID)
		}
		if status >= 500 {
			slog.Error("request", attrs...)
		} else {
			slog.Warn("request", attrs...)
		}
	}
}

// warnScan makes dropped rows visible: list handlers skip a bad row rather
// than fail the whole response, but doing so silently hid data problems.
func warnScan(where string, err error) { log.Printf("%s: dropped row: %v", where, err) }

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
	initLogging()

	// Scoring/leaderboard is the whole point of this app, so unlike a
	// dashboard with live-fetch fallbacks, we fail fast without a DB.
	if err := initDB(appCtx); err != nil {
		log.Fatalf("DB unavailable: %v", err)
	}

	var r *gin.Engine
	if isCloudRun() {
		gin.SetMode(gin.ReleaseMode)
		r = gin.New()
		r.Use(gin.Recovery(), requestLogger())
	} else {
		r = gin.Default() // debug logger is useful locally
	}
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
