package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ── section: TED Talk of the day — main page hero. No table of its own —
// a fixed curated list rotated deterministically by date, so "today's talk"
// is the same for the whole team and any past date is reproducible without
// persisting anything. Video ids below were verified against ted.com/
// youtube.com search results before hardcoding (never guess a video id —
// a wrong one just embeds a broken/wrong player).

type TEDTalk struct {
	VideoID string `json:"video_id"`
	Title   string `json:"title"`
	Speaker string `json:"speaker"`
}

var tedTalks = []TEDTalk{
	{"eIho2S0ZahI", "How to speak so that people want to listen", "Julian Treasure"},
	{"Ks-_Mh1QhMc", "Your body language may shape who you are", "Amy Cuddy"},
	{"qp0HIF3SfI4", "How great leaders inspire action", "Simon Sinek"},
	{"R1vskiVDwl4", "10 ways to have a better conversation", "Celeste Headlee"},
	{"rrkrvAUbU9Y", "The puzzle of motivation", "Dan Pink"},
	{"iCvmsMzlF7o", "The power of vulnerability", "Brené Brown"},
	{"c0KYU2j0TM4", "The power of introverts", "Susan Cain"},
	{"1nYFpuc2Umk", "The secret structure of great talks", "Nancy Duarte"},
}

// tedTalkEpoch anchors the rotation so "today's talk" is reproducible for
// any date (past or future) without storing anything — day N since this
// anchor always maps to the same list index.
var tedTalkEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func tedTalkForDate(date time.Time) TEDTalk {
	days := int(date.Sub(tedTalkEpoch).Hours() / 24)
	idx := days % len(tedTalks)
	if idx < 0 {
		idx += len(tedTalks)
	}
	return tedTalks[idx]
}

func registerTedTalkRoutes(r *gin.Engine) {
	r.GET("/api/tedtalk", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		dateStr := c.Query("date")
		var date time.Time
		if dateStr == "" {
			date = time.Now().In(seoulTZ)
		} else {
			parsed, err := time.ParseInLocation("2006-01-02", dateStr, seoulTZ)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid date"})
				return
			}
			date = parsed
		}
		talk := tedTalkForDate(date)
		c.JSON(http.StatusOK, gin.H{
			"date":     date.Format("2006-01-02"),
			"video_id": talk.VideoID,
			"title":    talk.Title,
			"speaker":  talk.Speaker,
		})
	})
}
