package child

import (
	"encoding/json"
	"net/http"
	"sync"

	"Bard/audio"
	"Bard/internal/logger"
	"Bard/providers/ytdlp"

	"github.com/bwmarrin/discordgo"
)

// Server is the child HTTP server. It handles voice joining and audio playback
// on behalf of the master bot. It never sends messages to Discord.
type Server struct {
	session    *discordgo.Session
	voiceConns sync.Map // guildID → *discordgo.VoiceConnection
	players    sync.Map // guildID → *audio.Player
}

func New(session *discordgo.Session) *Server {
	return &Server{session: session}
}

func (s *Server) Listen(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/join", s.handleJoin)
	mux.HandleFunc("/leave", s.handleLeave)
	mux.HandleFunc("/play", s.handlePlay)
	mux.HandleFunc("/skip", s.handleSkip)
	mux.HandleFunc("/stop", s.handleStop)
	mux.HandleFunc("/pause", s.handlePause)
	mux.HandleFunc("/queue", s.handleQueue)
	log := logger.With("child")
	log.Info().Str("address", addr).Msg("listening")
	return http.ListenAndServe(addr, mux)
}

// — Request / response types —

type guildRequest struct {
	GuildID string `json:"guild_id"`
}

type joinRequest struct {
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
}

type playRequest struct {
	GuildID string `json:"guild_id"`
	Query   string `json:"query"`
}

type pauseRequest struct {
	GuildID string `json:"guild_id"`
	Paused  bool   `json:"paused"`
}

type playResponse struct {
	OK     bool   `json:"ok"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Queued bool   `json:"queued"`
}

// — HTTP helpers —

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func okResp(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func decode(r *http.Request, v any) bool {
	return json.NewDecoder(r.Body).Decode(v) == nil
}

// — Handlers —

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	okResp(w)
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	var req joinRequest
	if !decode(r, &req) {
		fail(w, http.StatusBadRequest,"invalid request")
		return
	}
	vc, err := s.session.ChannelVoiceJoin(req.GuildID, req.ChannelID, false, false)
	if err != nil {
		fail(w, http.StatusInternalServerError,err.Error())
		return
	}
	s.voiceConns.Store(req.GuildID, vc)
	okResp(w)
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	var req guildRequest
	if !decode(r, &req) {
		fail(w, http.StatusBadRequest,"invalid request")
		return
	}
	s.destroyPlayer(req.GuildID)
	if vc, loaded := s.voiceConns.LoadAndDelete(req.GuildID); loaded {
		vc.(*discordgo.VoiceConnection).Disconnect()
	}
	okResp(w)
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	var req playRequest
	if !decode(r, &req) {
		fail(w, http.StatusBadRequest,"invalid request")
		return
	}

	vcVal, ok := s.voiceConns.Load(req.GuildID)
	if !ok {
		fail(w, http.StatusBadRequest,"not in voice channel")
		return
	}
	vc := vcVal.(*discordgo.VoiceConnection)

	track, err := ytdlp.Search(req.Query)
	if err != nil {
		fail(w, http.StatusInternalServerError,"could not find track: "+err.Error())
		return
	}

	p, _ := s.players.LoadOrStore(req.GuildID, audio.New(req.GuildID, vc))
	player := p.(*audio.Player)

	wasEmpty := player.Enqueue(*track)
	if wasEmpty {
		player.PlayNext()
	}

	writeJSON(w, http.StatusOK, playResponse{
		OK:     true,
		Title:  track.Title,
		Author: track.Author,
		Queued: !wasEmpty,
	})
}

func (s *Server) handleSkip(w http.ResponseWriter, r *http.Request) {
	var req guildRequest
	if !decode(r, &req) {
		fail(w, http.StatusBadRequest,"invalid request")
		return
	}
	p, loaded := s.players.Load(req.GuildID)
	if !loaded {
		fail(w, http.StatusNotFound,"no active player")
		return
	}
	p.(*audio.Player).Skip()
	okResp(w)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	var req guildRequest
	if !decode(r, &req) {
		fail(w, http.StatusBadRequest,"invalid request")
		return
	}
	s.destroyPlayer(req.GuildID)
	okResp(w)
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	var req pauseRequest
	if !decode(r, &req) {
		fail(w, http.StatusBadRequest,"invalid request")
		return
	}
	if p, ok := s.players.Load(req.GuildID); ok {
		p.(*audio.Player).Pause(req.Paused)
	}
	okResp(w)
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	var req guildRequest
	if !decode(r, &req) {
		fail(w, http.StatusBadRequest,"invalid request")
		return
	}
	p, loaded := s.players.Load(req.GuildID)
	if !loaded {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": "Queue is empty."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": p.(*audio.Player).FormatQueue()})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	type guildStatus struct {
		GuildID string `json:"guild_id"`
		audio.PlayerStatus
	}
	var guilds []guildStatus
	s.players.Range(func(k, v any) bool {
		guilds = append(guilds, guildStatus{
			GuildID:      k.(string),
			PlayerStatus: v.(*audio.Player).Status(),
		})
		return true
	})
	if guilds == nil {
		guilds = []guildStatus{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "guilds": guilds})
}

// — Internal helpers —

func (s *Server) destroyPlayer(guildID string) {
	if p, ok := s.players.LoadAndDelete(guildID); ok {
		p.(*audio.Player).Stop()
	}
}
