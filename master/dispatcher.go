package master

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"Bard/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// healthClient is shared across all health checks — short timeout, no state.
var healthClient = &http.Client{Timeout: 2 * time.Second}

// ChildClient is the master's HTTP client for one child instance.
type ChildClient struct {
	Address string
	client  http.Client
}

// Dispatcher routes voice channels to child instances.
// Each active voice channel session is assigned to exactly one child.
type Dispatcher struct {
	children []*ChildClient
	assigned sync.Map // voiceChannelID → *ChildClient
	mu       sync.Mutex
}

func NewDispatcher(addresses []string) *Dispatcher {
	d := &Dispatcher{}
	for _, addr := range addresses {
		d.children = append(d.children, &ChildClient{
			Address: addr,
			client:  http.Client{Timeout: 30 * time.Second},
		})
	}
	return d
}

// Healthy does a quick ping to the child's /health endpoint.
func (c *ChildClient) Healthy() bool {
	resp, err := healthClient.Get("http://" + c.Address + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ForChannel returns the child already handling channelID, or assigns the
// least-loaded healthy child to it. Each child can serve multiple guilds
// simultaneously; load is measured by number of active channel assignments.
// Returns nil only if every child is unreachable.
func (d *Dispatcher) ForChannel(channelID string) *ChildClient {
	if c, ok := d.assigned.Load(channelID); ok {
		return c.(*ChildClient)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Double-check under lock in case another goroutine just assigned it.
	if c, ok := d.assigned.Load(channelID); ok {
		return c.(*ChildClient)
	}

	// Count active sessions per child.
	load := make(map[*ChildClient]int, len(d.children))
	d.assigned.Range(func(_, v any) bool {
		load[v.(*ChildClient)]++
		return true
	})

	// Pick the healthy child with the fewest active sessions.
	var best *ChildClient
	bestLoad := int(^uint(0) >> 1) // max int
	for _, c := range d.children {
		if c.Healthy() && load[c] < bestLoad {
			best = c
			bestLoad = load[c]
		}
	}
	if best == nil {
		return nil // all children unreachable
	}
	d.assigned.Store(channelID, best)
	return best
}

// Release frees the child assigned to channelID back into the pool.
func (d *Dispatcher) Release(channelID string) {
	d.assigned.Delete(channelID)
}

// — Monitoring —

// trackSnapshot mirrors the JSON shape of audio.TrackInfo for deserialization.
type trackSnapshot struct {
	Title    string `json:"title"`
	Author   string `json:"author"`
	Duration int    `json:"duration"`
}

// guildSnapshot mirrors the per-guild JSON returned by a child's /status endpoint.
type guildSnapshot struct {
	GuildID     string         `json:"guild_id"`
	Playing     bool           `json:"playing"`
	Paused      bool           `json:"paused"`
	Current     *trackSnapshot `json:"current,omitempty"`
	QueueLength int            `json:"queue_length"`
}

// ChildSnapshot is the aggregated monitoring view of one child instance.
type ChildSnapshot struct {
	Address       string          `json:"address"`
	Healthy       bool            `json:"healthy"`
	VoiceChannels []string        `json:"voice_channels"`
	Guilds        []guildSnapshot `json:"guilds"`
}

// fetchStatus calls the child's /status endpoint and returns per-guild state.
func (c *ChildClient) fetchStatus() ([]guildSnapshot, error) {
	resp, err := healthClient.Get("http://" + c.Address + "/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result struct {
		Guilds []guildSnapshot `json:"guilds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Guilds, nil
}

// Snapshots returns the current monitoring state of all children.
func (d *Dispatcher) Snapshots() []ChildSnapshot {
	voiceChans := make(map[*ChildClient][]string)
	d.assigned.Range(func(k, v any) bool {
		c := v.(*ChildClient)
		voiceChans[c] = append(voiceChans[c], k.(string))
		return true
	})

	snaps := make([]ChildSnapshot, len(d.children))
	for i, c := range d.children {
		healthy := c.Healthy()
		guilds, _ := c.fetchStatus()
		vcs := voiceChans[c]
		if vcs == nil {
			vcs = []string{}
		}
		if guilds == nil {
			guilds = []guildSnapshot{}
		}
		snaps[i] = ChildSnapshot{
			Address:       c.Address,
			Healthy:       healthy,
			VoiceChannels: vcs,
			Guilds:        guilds,
		}
	}
	return snaps
}

// — Child HTTP calls —

type playResult struct {
	Title  string
	Author string
	Queued bool
}

func (c *ChildClient) post(path string, body any) ([]byte, error) {
	b, _ := json.Marshal(body)
	resp, err := c.client.Post("http://"+c.Address+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("child %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(data, &e)
		return nil, fmt.Errorf("%s", e.Error)
	}
	return data, nil
}

func (c *ChildClient) Join(guildID, channelID string) error {
	_, err := c.post("/join", map[string]string{"guild_id": guildID, "channel_id": channelID})
	return err
}

func (c *ChildClient) Leave(guildID string) error {
	_, err := c.post("/leave", map[string]string{"guild_id": guildID})
	return err
}

func (c *ChildClient) Play(guildID, query string) (*playResult, error) {
	data, err := c.post("/play", map[string]string{"guild_id": guildID, "query": query})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Title  string `json:"title"`
		Author string `json:"author"`
		Queued bool   `json:"queued"`
	}
	json.Unmarshal(data, &resp)
	return &playResult{Title: resp.Title, Author: resp.Author, Queued: resp.Queued}, nil
}

func (c *ChildClient) Skip(guildID string) error {
	_, err := c.post("/skip", map[string]string{"guild_id": guildID})
	return err
}

func (c *ChildClient) Stop(guildID string) error {
	_, err := c.post("/stop", map[string]string{"guild_id": guildID})
	return err
}

func (c *ChildClient) Pause(guildID string, paused bool) error {
	_, err := c.post("/pause", map[string]any{"guild_id": guildID, "paused": paused})
	return err
}

func (c *ChildClient) Queue(guildID string) (string, error) {
	data, err := c.post("/queue", map[string]string{"guild_id": guildID})
	if err != nil {
		return "", err
	}
	var resp struct {
		Text string `json:"text"`
	}
	json.Unmarshal(data, &resp)
	return resp.Text, nil
}

// — Slash commands —

// Commands is the list of application commands registered with Discord.
var Commands = []*discordgo.ApplicationCommand{
	{Name: "ping", Description: "Check if the bot is alive"},
	{
		Name:        "play",
		Description: "Play a song or add it to the queue",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "query",
				Description: "Song name or URL",
				Required:    true,
			},
		},
	},
	{Name: "skip", Description: "Skip the current track"},
	{Name: "stop", Description: "Stop playback and leave the voice channel"},
	{Name: "queue", Description: "Show the current queue"},
	{Name: "pause", Description: "Pause playback"},
	{Name: "resume", Description: "Resume playback"},
	{Name: "leave", Description: "Leave the voice channel"},
}

// RegisterCommands registers all slash commands globally with Discord.
// Returns the created commands so the caller can delete them on shutdown.
func RegisterCommands(s *discordgo.Session) ([]*discordgo.ApplicationCommand, error) {
	registered := make([]*discordgo.ApplicationCommand, 0, len(Commands))
	for _, cmd := range Commands {
		c, err := s.ApplicationCommandCreate(s.State.User.ID, "", cmd)
		if err != nil {
			return registered, fmt.Errorf("register /%s: %w", cmd.Name, err)
		}
		registered = append(registered, c)
	}
	return registered, nil
}

// HandleInteraction dispatches a slash command interaction to the right handler.
func HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate, d *Dispatcher) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	log := logger.With("master").With().
		Str("guild", i.GuildID).
		Str("user", i.Member.User.ID).
		Str("cmd", data.Name).
		Logger()

	reply := func(msg string) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: msg},
		})
	}

	userVoiceChannelID := func() string {
		// Try the local state cache first (fast path).
		vs, err := s.State.VoiceState(i.GuildID, i.Member.User.ID)
		if err == nil {
			if vs.ChannelID == "" {
				log.Warn().Msg("user has voice state but no channel ID")
				return ""
			}
			return vs.ChannelID
		}

		// Cache miss — fall back to the REST API. This covers the case where
		// the user was already in a VC when the bot started (the GUILD_CREATE
		// voice state list may not include them) or a VOICE_STATE_UPDATE was missed.
		log.Debug().Err(err).Msg("voice state cache miss, fetching from API")
		vs, err = s.UserVoiceState(i.GuildID, i.Member.User.ID)
		if err != nil {
			log.Warn().Err(err).Msg("voice state API lookup failed")
			return ""
		}
		if vs.ChannelID == "" {
			log.Warn().Msg("user has voice state but no channel ID")
			return ""
		}
		return vs.ChannelID
	}

	stopAndLeave := func(vcID string) {
		c := d.ForChannel(vcID)
		if c == nil {
			return
		}
		c.Stop(i.GuildID)
		c.Leave(i.GuildID)
		d.Release(vcID)
	}

	switch data.Name {
	case "ping":
		reply("Pong!")

	case "play":
		query := data.Options[0].StringValue()
		vcID := userVoiceChannelID()
		if vcID == "" {
			log.Warn().Msg("play blocked: user not in a voice channel")
			reply("Join a voice channel first.")
			return
		}
		c := d.ForChannel(vcID)
		if c == nil {
			log.Warn().Str("vc", vcID).Msg("play blocked: no child available for channel")
			reply("All players are busy or unavailable.")
			return
		}
		log.Info().Str("vc", vcID).Str("child", c.Address).Str("query", query).Msg("play requested")
		// Defer immediately — yt-dlp lookup can take several seconds,
		// and Discord requires a response within 3 seconds.
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		})
		go func() {
			edit := func(msg string) {
				s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
			}
			log.Info().Str("vc", vcID).Str("child", c.Address).Msg("joining voice channel")
			if err := c.Join(i.GuildID, vcID); err != nil {
				log.Error().Err(err).Str("vc", vcID).Str("child", c.Address).Msg("join failed")
				edit("Could not join voice: " + err.Error())
				return
			}
			log.Info().Str("query", query).Msg("searching track")
			result, err := c.Play(i.GuildID, query)
			if err != nil {
				log.Error().Err(err).Str("query", query).Msg("play failed")
				edit("Error: " + err.Error())
				return
			}
			if result.Queued {
				log.Info().Str("title", result.Title).Msg("track queued")
				edit("Added to queue: **" + result.Title + "**")
			} else {
				log.Info().Str("title", result.Title).Msg("track playing")
				edit("Now playing: **" + result.Title + "**")
			}
		}()

	case "skip":
		vcID := userVoiceChannelID()
		if vcID == "" {
			reply("You're not in a voice channel.")
			return
		}
		if c := d.ForChannel(vcID); c != nil {
			if err := c.Skip(i.GuildID); err != nil {
				reply("Error: " + err.Error())
				return
			}
		}
		reply("Skipped.")

	case "stop":
		vcID := userVoiceChannelID()
		if vcID == "" {
			reply("You're not in a voice channel.")
			return
		}
		stopAndLeave(vcID)
		reply("Stopped.")

	case "queue":
		vcID := userVoiceChannelID()
		if vcID == "" {
			reply("Queue is empty.")
			return
		}
		c := d.ForChannel(vcID)
		if c == nil {
			reply("Queue is empty.")
			return
		}
		text, err := c.Queue(i.GuildID)
		if err != nil {
			reply("Error: " + err.Error())
			return
		}
		reply(text)

	case "pause":
		vcID := userVoiceChannelID()
		if vcID == "" {
			reply("You're not in a voice channel.")
			return
		}
		if c := d.ForChannel(vcID); c != nil {
			c.Pause(i.GuildID, true)
		}
		reply("Paused.")

	case "resume":
		vcID := userVoiceChannelID()
		if vcID == "" {
			reply("You're not in a voice channel.")
			return
		}
		if c := d.ForChannel(vcID); c != nil {
			c.Pause(i.GuildID, false)
		}
		reply("Resumed.")

	case "leave":
		vcID := userVoiceChannelID()
		if vcID == "" {
			reply("You're not in a voice channel.")
			return
		}
		stopAndLeave(vcID)
		reply("Left the voice channel.")
	}
}
