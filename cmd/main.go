package main

import (
	"flag"
	"fmt" // Dodano import fmt
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

// Globalna zmienna version. Może być nadpisana podczas kompilacji za pomocą -ldflags.
var version = "v1.1.1"

var (
	devMode         = flag.Bool("dev", false, "run in development mode (non-daemon)")
	versionFlag     = flag.Bool("v", false, "show version")       // Flaga -v
	versionFlagLong = flag.Bool("version", false, "show version") // Flaga --version
)

const banner = `
                 ___      __             __ <<<<<<[get Nick bot]
    ____  ____  [ m ]__  / /_  __  __   / /____  ____ _____ ___
   / __ \/ __ \  / / _ \/ __ \/ / / /  / __/ _ \/ __ ` + "`" + `/ __ ` + "`" + `__ \
  / /_/ / /_/ / / /  __/ /_/ / /_/ /  / /_/  __/ /_/ / / / / / /
 / .___/\____/_/ /\___/_.___/\__, /blo\__/\___/\__,_/_/ /_/ /_/
/_/  ruciu  /___/   dominik /____/                     kofany

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

// Funkcja do wyświetlania wersji
func printVersion() {
	// Tworzenie kolorowego ciągu znaków
	versionText := fmt.Sprintf("%s %s %s.",
		color.MagentaString("[gNb]"),
		color.GreenString("get Nick Bot by kofany"),
		color.YellowString(version),
	)
	fmt.Println(versionText)
}

func main() {
	// Definicja flag
	flag.Parse()

	// Sprawdzenie flagi wersji przed wyświetleniem baneru
	if *versionFlag || *versionFlagLong {
		printVersion()
		os.Exit(0)
	}

	// Wyświetlenie baneru tylko jeśli flaga wersji nie jest ustawiona
	printBanner()

	color.Blue("Starting main function")
	color.Blue("Checking and creating config files")
	err := config.CheckAndCreateConfigFiles()
	if err != nil {
		color.Red("Error checking/creating config files: %v", err)
		os.Exit(1)
	}
	color.Green("Config files checked and created if necessary")

	color.Blue("Initializing random number generator")
	rand.Seed(time.Now().UnixNano())

	if !*devMode {
		color.Yellow("Starting in daemon mode")
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
			color.Green("[gNb] get Nick Bot by kofany %s is running in background with pid: %d.", version, d.Pid)
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
