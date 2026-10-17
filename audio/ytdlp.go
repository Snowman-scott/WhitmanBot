package audio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Song holds everything the player and embeds need for one track.
type Song struct {
	Title       string
	Artist      string
	Thumbnail   string
	Duration    int    // seconds
	YoutubeURL  string // page URL; the audio itself is fetched when the track starts
	RequestedBy string
}

// audioFormat prefers YouTube's Opus audio, which needs the least conversion.
const audioFormat = "bestaudio[ext=webm]/bestaudio[ext=opus]/bestaudio/best"

// DurationStr returns a formatted "m:ss" string.
func (s *Song) DurationStr() string {
	m := s.Duration / 60
	sec := s.Duration % 60
	return fmt.Sprintf("%d:%02d", m, sec)
}

var bracketClean = regexp.MustCompile(`\(.*?\)`)

// Resolve looks up a YouTube search query or URL via yt-dlp and returns a
// ready-to-play Song. yt-dlp must be in PATH (or set YTDLP_BIN env var).
func Resolve(query, requestedBy string) (*Song, error) {
	search := query
	if !strings.HasPrefix(query, "http") {
		search = "ytsearch1:" + query
	}

	var stderr bytes.Buffer
	cmd := exec.Command(
		"yt-dlp",
		"--format", audioFormat,
		"--dump-json",
		"--no-playlist",
		"--quiet",
		"--no-warnings",
		search,
	)
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w\n%s", err, stderr.String())
	}

	var info map[string]any
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("could not parse yt-dlp output: %w", err)
	}

	// Search results come wrapped in entries[]; direct URLs are the root object.
	if entries, ok := info["entries"].([]any); ok && len(entries) > 0 {
		if entry, ok := entries[0].(map[string]any); ok {
			info = entry
		}
	}

	title := strings.TrimSpace(bracketClean.ReplaceAllString(strVal(info, "title"), ""))
	if title == "" {
		title = strVal(info, "title")
	}

	return &Song{
		Title:       title,
		Artist:      strVal(info, "uploader"),
		Thumbnail:   strVal(info, "thumbnail"),
		Duration:    intVal(info, "duration"),
		YoutubeURL:  strVal(info, "webpage_url"),
		RequestedBy: requestedBy,
	}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func strVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intVal(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}
