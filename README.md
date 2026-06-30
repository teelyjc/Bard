# Bard

A multi-instance Discord music bot written in Go. One **Master** bot handles commands; multiple **Child** bots handle voice and audio playback — each voice channel in a guild can have its own child playing simultaneously.

---

## Architecture

```
Discord Users
     │  text commands
     ▼
  Master Bot  ──────────────────────────────────────────┐
  (commands only,                                       │ HTTP
   no voice)                                            │
                                    ┌───────────────────┤
                                    │                   │
                               Child Bot 0         Child Bot 1
                             (voice + audio)     (voice + audio)
                                    │                   │
                               yt-dlp + ffmpeg     yt-dlp + ffmpeg
                                    │                   │
                              Discord Voice       Discord Voice
                              Channel A           Channel B
```

- **Master** receives Discord text commands, routes them to the correct child via HTTP
- **Child** joins voice, downloads audio via yt-dlp, encodes via ffmpeg, streams to Discord
- Each active voice channel session is assigned to exactly one child
- Children are selected by health check — only running children are used

---

## Requirements

- Go 1.21+
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)
- [ffmpeg](https://ffmpeg.org/) (with `libopus` support)

```bash
# macOS
brew install yt-dlp ffmpeg

# Ubuntu / Debian
sudo apt install ffmpeg
pip install yt-dlp
```

---

## Configuration

Copy and edit `config.json`:

```json
{
  "configs": {
    "master": {
      "token": "MASTER_BOT_TOKEN"
    },
    "child": {
      "nodes": [
        { "token": "CHILD_BOT_0_TOKEN", "address": "localhost:8081" },
        { "token": "CHILD_BOT_1_TOKEN", "address": "localhost:8082" }
      ]
    }
  }
}
```

- **master.token** — Discord bot token for the master bot (text commands only)
- **child.nodes** — list of child bot tokens and their HTTP listen addresses
- You can add as many children as needed; each supports one voice channel at a time

### Creating Bot Tokens

1. Go to [Discord Developer Portal](https://discord.com/developers/applications)
2. Create a new application for each bot (one master + one per child)
3. Under **Bot**, enable:
   - **Message Content Intent** (master only)
   - **Server Members Intent** (optional)
4. Copy the token for each bot

### Bot Permissions

Invite each bot with these permissions:
- Master: `Send Messages`, `Read Message History`
- Child: `Connect`, `Speak`

Use OAuth2 URL with scopes: `bot` and permissions above.

---

## Docker

Build and run everything with Docker Compose:

```bash
# 1. Fill in your tokens in config.docker.json
cp config.docker.json config.docker.json   # edit with your tokens

# 2. Build the image and start all services
docker compose up --build
```

**`config.docker.json`** uses Docker service names as addresses (`child0:8081`, `child1:8082`) instead of `localhost`. Edit the `token` values before running — the `address` values are already correct for Docker.

To add more children, duplicate a `child` service in `docker-compose.yml` (e.g., `child2`) and add a matching node in `config.docker.json`.

---

## Running locally

Each instance runs as a separate process:

```bash
# Terminal 1 — Master
go run main.go master

# Terminal 2 — Child 0
go run main.go child 0

# Terminal 3 — Child 1 (optional)
go run main.go child 1
```

Or build first:

```bash
go build -o bard .

./bard master
./bard child 0
./bard child 1
```

---

## Commands

All commands are sent to the **Master** bot in any text channel.

| Command | Description |
|---|---|
| `!play <query or URL>` | Search YouTube and play, or add to queue |
| `!skip` | Skip the current track |
| `!stop` | Stop playback and clear the queue |
| `!pause` | Pause playback |
| `!resume` | Resume playback |
| `!queue` | Show the current queue |
| `!leave` | Leave the voice channel |
| `!ping` | Check if the bot is alive |

You must be in a voice channel to use any command. The bot joins your voice channel automatically on `!play`.

---

## Multi-Voice Channel

Multiple voice channels in the same guild can play music simultaneously, each handled by a different child:

```
VC-1  →  Child 0  →  plays song A
VC-2  →  Child 1  →  plays song B
```

- When you `!play`, the master checks which voice channel you are in
- If that channel already has a child assigned, commands go to that child
- If not, the master picks the first healthy available child
- When you `!stop` or `!leave`, the child is released back to the pool

The number of simultaneous voice channels is limited by how many child bots are configured and running.

---

## Project Structure

```
Bard/
├── main.go                  — entry point, parses master/child mode
├── config/
│   └── config.go            — loads config.json
├── master/
│   └── dispatcher.go        — command handler + child routing
├── child/
│   └── server.go            — HTTP server + voice + player management
├── audio/
│   ├── player.go            — queue, playback, skip/stop/pause
│   └── ogg.go               — ogg bitstream parser (extracts opus frames)
└── providers/
    └── ytdlp/
        └── ytdlp.go         — yt-dlp wrapper (search + audio info)
```
