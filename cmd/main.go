package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/bot"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/nickmanager"
	"github.com/kofany/gNb/internal/util"
)

func main() {
	// Load configuration from YAML file
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Parse log level from configuration
	level, err := util.ParseLogLevel(cfg.Global.LogLevel)
	if err != nil {
		log.Fatalf("Invalid log level in config: %v", err)
	}

	// Initialize logger
	err = util.InitLogger(level, "bot.log")
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer util.CloseLogger()

	util.Info("Logger initialized with level: %s", cfg.Global.LogLevel)

	// Load owners from JSON file
	owners, err := auth.LoadOwners("configs/owners.json")
	if err != nil {
		util.Error("Failed to load owners: %v", err)
		return
	}

	util.Debug("Owners loaded: %+v", owners)

	// Create and initialize NickManager
	nm := nickmanager.NewNickManager()
	err = nm.LoadNicks("data/nicks.json")
	if err != nil {
		util.Error("Failed to load nicks: %v", err)
		return
	}

	util.Debug("NickManager initialized with nicks: %+v", nm.GetNicksToCatch())

	// Create BotManager
	botManager := bot.NewBotManager(cfg, owners, nm)

	// Start bots
	botManager.StartBots()

	// Start NickManager's monitoring loop
	nm.Start()

	util.Debug("Configuration loaded: %+v", cfg)

	// Handle system signals for clean shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	util.Info("Bot is running. Press Ctrl+C to exit.")

	// Wait for shutdown signal
	<-sigs

	// Stop bots and exit application
	botManager.Stop()
	util.Info("Application has been shut down.")
}
