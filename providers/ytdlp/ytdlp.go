package ytdlp

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Track holds metadata for a YouTube video.
type Track struct {
	ID       string
	Title    string
	Author   string
	Duration int // seconds
	VideoURL string
}

// Search finds the first YouTube result matching query, or loads info for a direct URL.
func Search(query string) (*Track, error) {
	id := "ytsearch1:" + query
	if strings.HasPrefix(query, "http://") || strings.HasPrefix(query, "https://") {
		id = query
	}
	return fetchInfo(id)
}

func fetchInfo(identifier string) (*Track, error) {
	out, err := exec.Command("yt-dlp",
		"--no-playlist",
		"-j",
		"--quiet",
		identifier,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp: %w", err)
	}

	// Take the last non-empty line (yt-dlp outputs one JSON object per video)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var raw string
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			raw = lines[i]
			break
		}
	}
	if raw == "" {
		return nil, fmt.Errorf("yt-dlp: no output")
	}

	var info struct {
		ID         string  `json:"id"`
		Title      string  `json:"title"`
		Uploader   string  `json:"uploader"`
		Channel    string  `json:"channel"`
		Duration   float64 `json:"duration"`
		WebpageURL string  `json:"webpage_url"`
	}
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return nil, fmt.Errorf("yt-dlp parse: %w", err)
	}

	author := info.Uploader
	if author == "" {
		author = info.Channel
	}
	url := info.WebpageURL
	if url == "" && info.ID != "" {
		url = "https://www.youtube.com/watch?v=" + info.ID
	}

	return &Track{
		ID:       info.ID,
		Title:    info.Title,
		Author:   author,
		Duration: int(info.Duration),
		VideoURL: url,
	}, nil
}
