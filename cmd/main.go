package main

import (
	"flag"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/bot"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/nickmanager"
	"github.com/kofany/gNb/internal/util"
	"github.com/sevlyar/go-daemon"
)

var (
	devMode = flag.Bool("dev", false, "run in development mode (non-daemon)")
)

const banner = `                   _      __             __
    ____  ____    (_)__  / /_  __  __   / /____  ____ _____ ___
   / __ \/ __ \  / / _ \/ __ \/ / / /  / __/ _ \/ __ ` + "`" + `/ __ ` + "`" + `__ \
  / /_/ / /_/ / / /  __/ /_/ / /_/ /  / /_/  __/ /_/ / / / / / /
 / .___/\____/_/ /\___/_.___/\__, /   \__/\___/\__,_/_/ /_/ /_/
/_/         /___/           /____/              pick Nick bot
`

func logLevelToString(level util.LogLevel) string {
	switch level {
	case util.DEBUG:
		return "DEBUG"
	case util.INFO:
		return "INFO"
	case util.WARNING:
		return "WARNING"
	case util.ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

func printBanner() {
	color.Cyan(banner)
}

func main() {
	printBanner()

	color.Blue("Starting main function")
	color.Blue("Checking and creating config files")
	err := config.CheckAndCreateConfigFiles()
	if err != nil {
		color.Red("Error checking/creating config files: %v", err)
		os.Exit(1)
	}
	color.Green("Config files checked and created if necessary")

	color.Blue("Parsing flags")
	flag.Parse()

	color.Blue("Initializing random number generator")
	rand.Seed(time.Now().UnixNano())

	if !*devMode {
		color.Yellow("Starting in daemon mode")
		printBanner()
		cntxt := &daemon.Context{
			PidFileName: "bot.pid",
			PidFilePerm: 0644,
			LogFileName: "bot.log",
			LogFilePerm: 0640,
			WorkDir:     "./",
			Umask:       027,
		}
		d, err := cntxt.Reborn()
		if err != nil {
			color.Red("Unable to run: %v", err)
			os.Exit(1)
		}
		if d != nil {
			color.Green("[pNb] pick Nick bot is running in background with pid: %d", d.Pid)
			return
		}
		defer cntxt.Release()
		log.Print("Daemon started")
	} else {
		color.Yellow("Starting in development mode")
	}

	color.Blue("Loading configuration from YAML file")
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		color.Red("Failed to load configuration: %v", err)
		os.Exit(1)
	}
	color.Green("Configuration loaded successfully")

	var level util.LogLevel
	if !*devMode {
		level = util.WARNING
		color.Yellow("Log level set to WARNING in daemon mode")
	} else {
		color.Blue("Parsing log level from config")
		level, err = util.ParseLogLevel(cfg.Global.LogLevel)
		if err != nil {
			color.Red("Invalid log level in config: %v", err)
			os.Exit(1)
		}
		color.Green("Log level set to %s", logLevelToString(level))
	}

	logFile := "bot.log"
	if *devMode {
		logFile = "bot_dev.log"
	}
	color.Blue("Initializing logger with file: %s", logFile)
	err = util.InitLogger(level, logFile)
	if err != nil {
		color.Red("Failed to initialize logger: %v", err)
		os.Exit(1)
	}
	defer util.CloseLogger()

	util.Info("Logger initialized with level: %s", logLevelToString(level))

	color.Blue("Loading owners from JSON file")
	owners, err := auth.LoadOwners("configs/owners.json")
	if err != nil {
		color.Red("Failed to load owners: %v", err)
		return
	}
	util.Debug("Owners loaded: %+v", owners)

	color.Blue("Creating and initializing NickManager")
	nm := nickmanager.NewNickManager()
	err = nm.LoadNicks("data/nicks.json")
	if err != nil {
		color.Red("Failed to load nicks: %v", err)
		return
	}
	util.Debug("NickManager initialized with nicks: %+v", nm.GetNicksToCatch())

	color.Blue("Creating BotManager")
	botManager := bot.NewBotManager(cfg, owners, nm)

	color.Blue("Starting bots")
	go botManager.StartBots()

	color.Blue("Starting NickManager's monitoring loop")
	go nm.Start()

	util.Debug("Configuration loaded: %+v", cfg)

	color.Blue("Setting up signal handling for clean shutdown")
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	color.Green("Bot is running. Press Ctrl+C to exit.")

	color.Blue("Waiting for shutdown signal")
	<-sigs

	color.Yellow("Shutdown signal received")
	botManager.Stop()
	util.Info("Application has been shut down.")
	color.Green("Application has been shut down.")
}
