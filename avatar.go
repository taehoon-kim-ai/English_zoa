package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ── section: avatars — profile photos everywhere a person shows up (topbar,
// team panel, battle lobby, notifications). Two sources, best first:
//
//  1. Slack profile photo via the Data API (users.lookupByEmail) — Slack
//     avatars at Applied are synced from Okta/Google, so this IS the
//     person's work photo. Attempted lazily (once per user per day) the
//     first time /api/me runs; needs the users:read + users:read.email
//     user scopes on the Data API token (slack.go).
//  2. Manual upload from the profile page (client-resized to a small
//     data: URL) — guaranteed to work even if Slack scopes are missing,
//     and always wins over the Slack photo once set.
//
// Owns no table — reads/writes users.avatar_url / users.avatar_checked_at.

const avatarMaxUploadBytes = 200 * 1024 // data: URL length cap (~150KB image)

// fetchSlackAvatar asks the Slack Data API for the user's profile image.
// Empty result (not an error) when the API/scopes aren't available.
func fetchSlackAvatar(email string) string {
	if !dataAPIReady() {
		return ""
	}
	resp, err := dataAPIGetSlack("/users.lookupByEmail?email=" + url.QueryEscape(email))
	if err != nil {
		log.Printf("avatar: slack lookup %s: %v", email, err)
		return ""
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		User  struct {
			Profile struct {
				Image192 string `json:"image_192"`
				Image72  string `json:"image_72"`
			} `json:"profile"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || !out.OK {
		if out.Error != "" {
			log.Printf("avatar: slack lookup %s: %s", email, out.Error)
		}
		return ""
	}
	if out.User.Profile.Image192 != "" {
		return out.User.Profile.Image192
	}
	return out.User.Profile.Image72
}

// ensureAvatar backfills a missing avatar from Slack, at most once per day
// per user. Fire-and-forget (called with `go` from /api/me).
func ensureAvatar(email string) {
	if !dbAvailable() || email == "" {
		return
	}
	ctx := context.Background()
	// Claim the daily attempt atomically so concurrent requests (and other
	// instances) don't stampede the Data API.
	ct, err := db.Exec(ctx, `
		UPDATE phraseup.users SET avatar_checked_at = NOW()
		WHERE email = $1 AND avatar_url = ''
		  AND (avatar_checked_at IS NULL OR avatar_checked_at < NOW() - INTERVAL '24 hours')
	`, email)
	if err != nil || ct.RowsAffected() == 0 {
		return
	}
	photo := fetchSlackAvatar(email)
	if photo == "" {
		return
	}
	if _, err := db.Exec(ctx, `
		UPDATE phraseup.users SET avatar_url = $2 WHERE email = $1 AND avatar_url = ''
	`, email, photo); err != nil {
		log.Printf("avatar: store %s: %v", email, err)
	}
}

func registerAvatarRoutes(r *gin.Engine) {
	// Manual upload: the profile page sends a small client-resized data: URL.
	// An empty avatar clears the override (Slack backfill may then refill it).
	r.POST("/api/profile/avatar", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			Avatar string `json:"avatar"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		avatar := strings.TrimSpace(body.Avatar)
		if avatar != "" && !strings.HasPrefix(avatar, "data:image/") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "avatar must be an image"})
			return
		}
		if len(avatar) > avatarMaxUploadBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "image too large — try a smaller photo"})
			return
		}
		checkedAt := time.Now()
		if _, err := db.Exec(c.Request.Context(), `
			UPDATE phraseup.users SET avatar_url = $2, avatar_checked_at = $3 WHERE email = $1
		`, email, avatar, checkedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "save failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "avatar_url": avatar})
	})
}
