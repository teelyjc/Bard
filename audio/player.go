package audio

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"Bard/internal/logger"
	"Bard/providers/ytdlp"

	"github.com/bwmarrin/discordgo"
	"github.com/rs/zerolog"
)

// TrackInfo is exported metadata about a track, used for monitoring responses.
type TrackInfo struct {
	Title    string `json:"title"`
	Author   string `json:"author"`
	Duration int    `json:"duration"`
}

// PlayerStatus is a point-in-time snapshot of the player state for monitoring.
type PlayerStatus struct {
	Playing     bool       `json:"playing"`
	Paused      bool       `json:"paused"`
	Current     *TrackInfo `json:"current,omitempty"`
	QueueLength int        `json:"queue_length"`
}

// Status returns a snapshot of the player's current state.
func (p *Player) Status() PlayerStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := PlayerStatus{
		Playing:     p.current != nil,
		Paused:      p.paused,
		QueueLength: len(p.queue),
	}
	if p.current != nil {
		s.Current = &TrackInfo{
			Title:    p.current.Title,
			Author:   p.current.Author,
			Duration: p.current.Duration,
		}
	}
	return s
}

// Player manages a per-guild audio queue and streams opus to a Discord voice connection.
type Player struct {
	mu      sync.Mutex
	log     zerolog.Logger
	guildID string
	vc      *discordgo.VoiceConnection

	queue   []ytdlp.Track
	current *ytdlp.Track
	stopCh  chan struct{}

	paused    bool
	unpauseCh chan struct{}
}

// New creates a Player for the given guild using the provided voice connection.
func New(guildID string, vc *discordgo.VoiceConnection) *Player {
	return &Player{
		log:       logger.With("audio").With().Str("guild", guildID).Logger(),
		guildID:   guildID,
		vc:        vc,
		stopCh:    make(chan struct{}),
		unpauseCh: make(chan struct{}),
	}
}

// Enqueue appends tracks to the queue. Returns true if the queue was empty
// (caller should call PlayNext to start playback).
func (p *Player) Enqueue(tracks ...ytdlp.Track) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	wasEmpty := len(p.queue) == 0 && p.current == nil
	p.queue = append(p.queue, tracks...)
	return wasEmpty
}

// PlayNext dequeues the next track and begins streaming it in a goroutine.
func (p *Player) PlayNext() {
	p.mu.Lock()
	if len(p.queue) == 0 {
		p.current = nil
		p.mu.Unlock()
		return
	}
	track := p.queue[0]
	p.queue = p.queue[1:]
	p.current = &track
	stopCh := p.stopCh
	p.mu.Unlock()

	go func() {
		p.log.Info().Str("title", track.Title).Msg("playing")
		if err := p.stream(track, stopCh); err != nil {
			p.log.Error().Err(err).Msg("stream error")
		}
		// Auto-advance only if this session wasn't replaced by Skip/Stop
		p.mu.Lock()
		same := p.stopCh == stopCh
		p.mu.Unlock()
		if same {
			p.PlayNext()
		}
	}()
}

// Skip stops the current track and starts the next one immediately.
func (p *Player) Skip() {
	p.mu.Lock()
	old := p.stopCh
	p.stopCh = make(chan struct{})
	p.current = nil
	p.mu.Unlock()
	close(old)
	p.PlayNext()
}

// Stop clears the queue and stops the current track.
func (p *Player) Stop() {
	p.mu.Lock()
	old := p.stopCh
	p.stopCh = make(chan struct{})
	p.current = nil
	p.queue = nil
	p.mu.Unlock()
	close(old)
}

// Pause pauses or resumes playback.
func (p *Player) Pause(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if paused && !p.paused {
		p.paused = true
	} else if !paused && p.paused {
		p.paused = false
		close(p.unpauseCh)
		p.unpauseCh = make(chan struct{})
	}
}

// FormatQueue returns a human-readable queue string.
func (p *Player) FormatQueue() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var sb strings.Builder
	if p.current != nil {
		fmt.Fprintf(&sb, "**Now playing:** %s — %s\n", p.current.Title, p.current.Author)
	}
	if len(p.queue) == 0 {
		if p.current == nil {
			return "Queue is empty."
		}
		return sb.String()
	}
	sb.WriteString("**Queue:**\n")
	limit := len(p.queue)
	if limit > 10 {
		limit = 10
	}
	for i, t := range p.queue[:limit] {
		fmt.Fprintf(&sb, "%d. %s — %s\n", i+1, t.Title, t.Author)
	}
	if len(p.queue) > 10 {
		fmt.Fprintf(&sb, "...and %d more", len(p.queue)-10)
	}
	return sb.String()
}

func (p *Player) stream(track ytdlp.Track, stopCh <-chan struct{}) error {
	// yt-dlp downloads audio to stdout; ffmpeg reads from stdin and encodes to ogg/opus.
	// Piping avoids signed-URL expiry and lets yt-dlp handle all YouTube auth headers.
	dl := exec.Command("yt-dlp",
		"-f", "bestaudio",
		"-o", "-",
		"--quiet",
		"--no-warnings",
		track.VideoURL,
	)
	enc := exec.Command("ffmpeg",
		"-i", "pipe:0",
		"-vn",
		"-c:a", "libopus",
		"-ar", "48000",
		"-ac", "2",
		"-b:a", "96k",
		"-f", "ogg",
		"-loglevel", "error",
		"pipe:1",
	)

	dlOut, err := dl.StdoutPipe()
	if err != nil {
		return err
	}
	enc.Stdin = dlOut
	enc.Stderr = os.Stderr // surface ffmpeg errors to the bot log

	encOut, err := enc.StdoutPipe()
	if err != nil {
		return err
	}

	if err := dl.Start(); err != nil {
		return fmt.Errorf("yt-dlp start: %w", err)
	}
	if err := enc.Start(); err != nil {
		dl.Process.Kill()
		return fmt.Errorf("ffmpeg start: %w", err)
	}
	defer func() {
		dl.Process.Kill()
		enc.Process.Kill()
		dl.Wait()
		enc.Wait()
	}()

	p.vc.Speaking(true)
	defer p.vc.Speaking(false)

	sent := 0
	err = ReadOggOpus(encOut, func(pkt []byte) bool {
		if sent == 0 {
			p.log.Debug().Int("bytes_per_frame", len(pkt)).Msg("streaming started")
		}
		sent++

		p.mu.Lock()
		if p.paused {
			ch := p.unpauseCh
			p.mu.Unlock()
			select {
			case <-ch:
			case <-stopCh:
				return false
			}
		} else {
			p.mu.Unlock()
		}

		select {
		case p.vc.OpusSend <- pkt:
			return true
		case <-stopCh:
			return false
		}
	})
	p.log.Info().Int("frames", sent).Msg("stream done")
	return err
}
