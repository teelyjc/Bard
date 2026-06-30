package main

import (
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"Bard/child"
	"Bard/config"
	"Bard/internal/logger"
	"Bard/master"

	"github.com/bwmarrin/discordgo"
)

func main() {
	mode, index := parseArgs()
	cfg := mustLoad()

	switch mode {
	case "master":
		runMaster(cfg)
	case "child":
		runChild(cfg, index)
	default:
		logger.Fatal().Msgf("unknown mode %q — use 'master' or 'child [index]'", mode)
	}
}

func parseArgs() (mode string, index int) {
	args := os.Args[1:]
	mode = "master"
	if len(args) > 0 {
		mode = args[0]
	}
	if len(args) > 1 {
		var err error
		index, err = strconv.Atoi(args[1])
		if err != nil {
			logger.Fatal().Str("arg", args[1]).Msg("invalid child index")
		}
	}
	return
}

func mustLoad() *config.Config {
	cfg, err := config.Load("config.json")
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}
	return cfg
}

// runMaster starts the master bot. It only interacts with users over Discord
// slash commands — all audio is delegated to child instances via HTTP.
func runMaster(cfg *config.Config) {
	log := logger.With("master")
	log.Info().Msg("starting")

	addrs := make([]string, len(cfg.ChildNodes()))
	for i, n := range cfg.ChildNodes() {
		addrs[i] = n.Address
	}
	disp := master.NewDispatcher(addrs)

	mon := master.NewMonitor(disp)
	go func() {
		if err := mon.Listen(cfg.MasterMonitorAddress()); err != nil {
			log.Fatal().Err(err).Msg("monitor server")
		}
	}()

	dg, err := discordgo.New("Bot " + cfg.MasterToken())
	if err != nil {
		log.Fatal().Err(err).Msg("create session")
	}

	var registeredCmds []*discordgo.ApplicationCommand
	dg.AddHandlerOnce(func(s *discordgo.Session, e *discordgo.Ready) {
		log.Info().Str("user", e.User.Username+"#"+e.User.Discriminator).Msg("logged in")
		cmds, err := master.RegisterCommands(s)
		if err != nil {
			log.Fatal().Err(err).Msg("register commands")
		}
		registeredCmds = cmds
		log.Info().Int("count", len(cmds)).Msg("slash commands registered")
	})
	dg.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		master.HandleInteraction(s, i, disp)
	})

	// Voice states needed to resolve the user's current voice channel.
	dg.Identify.Intents = discordgo.IntentsGuildVoiceStates

	if err = dg.Open(); err != nil {
		log.Fatal().Err(err).Msg("open connection")
	}
	defer func() {
		for _, cmd := range registeredCmds {
			if err := dg.ApplicationCommandDelete(dg.State.User.ID, "", cmd.ID); err != nil {
				log.Error().Err(err).Str("command", cmd.Name).Msg("delete command")
			}
		}
		dg.Close()
	}()

	waitSignal()
}

// runChild starts a child player instance. It connects to Discord for voice
// only and exposes an HTTP server for the master to call. It never sends
// messages to Discord.
func runChild(cfg *config.Config, index int) {
	node, err := cfg.ChildNode(index)
	if err != nil {
		logger.Fatal().Err(err).Msg("resolve child node")
	}

	log := logger.With("child").With().Int("index", index).Str("address", node.Address).Logger()
	log.Info().Msg("starting")

	dg, err := discordgo.New("Bot " + node.Token)
	if err != nil {
		log.Fatal().Err(err).Msg("create session")
	}

	dg.AddHandlerOnce(func(s *discordgo.Session, e *discordgo.Ready) {
		srv := child.New(s)
		go func() {
			if err := srv.Listen(node.Address); err != nil {
				log.Fatal().Err(err).Msg("http server")
			}
		}()
		log.Info().Str("user", e.User.Username+"#"+e.User.Discriminator).Msg("logged in")
	})

	// Voice intents only — child never reads or sends messages.
	dg.Identify.Intents = discordgo.IntentsGuildVoiceStates

	if err = dg.Open(); err != nil {
		log.Fatal().Err(err).Msg("open connection")
	}
	defer dg.Close()

	waitSignal()
}

func waitSignal() {
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM)
	<-sc
}
