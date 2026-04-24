package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/fatih/color"
	"github.com/kofany/gNb/internal/api"
	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/bot"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/nickmanager"
	"github.com/kofany/gNb/internal/oidentd"
	"github.com/kofany/gNb/internal/util"
	"github.com/sevlyar/go-daemon"
)

var version = "v1.3.0"

var (
	devMode         = flag.Bool("dev", false, "run in development mode (non-daemon)")
	versionFlag     = flag.Bool("v", false, "show version")
	versionFlagLong = flag.Bool("version", false, "show version")
)

const banner = `
                     ___      __             __ <<<<<<[get Nick bot]
        ____  ____  [ m ]__  / /_  __  __   / /____  ____ _____ ___
       / __ \/ __ \  / / _ \/ __ \/ / / /  / __/ _ \/ __ \ / __ \ __ \
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

func printVersion() {
	versionText := fmt.Sprintf("%s %s %s.",
		color.MagentaString("[gNb]"),
		color.GreenString("get Nick Bot by kofany"),
		color.YellowString(version),
	)
	fmt.Println(versionText)
}

func isDebian() bool {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return false
	}
	content := string(data)
	if strings.Contains(content, "ID=debian") {
		return true
	}
	return false
}

func main() {
	flag.Parse()

	if *versionFlag || *versionFlagLong {
		printVersion()
		os.Exit(0)
	}

	printBanner()

	color.Blue("Starting main function")
	color.Blue("Checking and creating config files")
	err := config.CheckAndCreateConfigFiles()
	if err != nil {
		color.Red("Error checking/creating config files: %v", err)
		os.Exit(1)
	}
	color.Green("Config files checked and created if necessary")

	// Wczytujemy konfigurację wcześnie, bo będzie potrzebna dla oidentd
	color.Blue("Loading initial configuration from YAML file")
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		color.Red("Failed to load configuration: %v", err)
		os.Exit(1)
	}
	color.Green("Configuration loaded successfully")

	// Wykonujemy logikę oidentd na samym początku
	uid := os.Geteuid()
	if uid == 0 {
		color.Blue("Running as root")
		// Sprawdzamy, czy plik /etc/oidentd.conf istnieje
		if _, err := os.Stat("/etc/oidentd.conf"); err == nil {
			color.Blue("/etc/oidentd.conf exists")
			// Sprawdzamy, czy system to Debian
			if isDebian() {
				color.Blue("System is Debian")
				// Uruchamiamy logikę oidentd bez pytania użytkownika
				color.Blue("Configuring oidentd without user interaction...")
				if err := oidentd.SetupOidentd(cfg); err != nil {
					color.Red("Failed to setup oidentd: %v", err)
					color.Yellow("Continuing without oidentd configuration")
				} else {
					color.Green("Oidentd configured successfully")
				}
			} else {
				color.Yellow("System is not Debian, skipping oidentd configuration")
			}
		} else {
			color.Yellow("/etc/oidentd.conf does not exist, skipping oidentd configuration")
		}
	} else {
		color.Yellow("Not running as root (UID: %d), oidentd configuration not available", uid)
	}

	// Go 1.20+ seeds math/rand automatically on first use; no manual Seed() required.

	// Daemonizacja
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

		child, err := cntxt.Reborn()
		if err != nil {
			color.Red("Unable to run: %v", err)
			os.Exit(1)
		}

		if child != nil {
			color.Green("[gNb] get Nick Bot by kofany %s is running in background with pid: %d.", version, child.Pid)
			return
		}
		defer cntxt.Release()
		// Daemon mode: util.InitLogger has not run yet, so the one
		// log.Printf below (on init failure) intentionally uses the
		// stdlib logger to ensure the failure is visible in bot.log.
		level := util.WARNING
		logFile := "bot.log"
		err = util.InitLogger(level, logFile)
		if err != nil {
			log.Printf("Failed to initialize logger after daemonization: %v", err)
			os.Exit(1)
		}
		util.Info("Daemon started; logger initialized at level %s", logLevelToString(level))
	} else {
		color.Yellow("Running in development mode (foreground)")

		// W trybie deweloperskim, inicjalizujemy logger tutaj
		color.Blue("Parsing log level from config")
		level, err := util.ParseLogLevel(cfg.Global.LogLevel)
		if err != nil {
			color.Red("Invalid log level in config: %v", err)
			os.Exit(1)
		}
		if level != util.DEBUG {
			color.Yellow("Overriding log level to DEBUG in development mode")
			level = util.DEBUG
		}
		color.Green("Log level set to %s", logLevelToString(level))

		logFile := "bot_dev.log"
		color.Blue("Initializing logger with file: %s", logFile)
		err = util.InitLogger(level, logFile)
		if err != nil {
			color.Red("Failed to initialize logger: %v", err)
			os.Exit(1)
		}
		defer util.CloseLogger()
		util.Info("Logger initialized with level: %s", logLevelToString(level))
	}

	// Reszta kodu pozostaje bez zmian

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

	// Uruchomienie botów
	color.Blue("Starting bots")
	go func() {
		botManager.StartBots()
		color.Blue("Starting NickManager's monitoring loop")
		nm.Start()
	}()

	// Panel API (WebSocket) — opcjonalne, gate'owane przez cfg.API.Enabled.
	// Token z configu musi być ustawiony, inaczej APIConfig.Validate już wcześniej zwróciłoby błąd.
	var apiCancel context.CancelFunc
	if cfg.API.Enabled {
		nodeID, idErr := api.LoadOrCreateNodeID(api.DefaultNodeIDPath())
		if idErr != nil {
			util.Error("API: failed to load/create node_id: %v", idErr)
		} else {
			apiSrv := api.New(cfg.API, nodeID, api.Deps{
				Config:      cfg,
				BotManager:  botManager,
				NickManager: nm,
			})
			botManager.SetEventSink(apiSrv.Sink())
			var apiCtx context.Context
			apiCtx, apiCancel = context.WithCancel(context.Background())
			go func() {
				if err := apiSrv.Run(apiCtx); err != nil {
					util.Error("API: %v", err)
				}
			}()
			util.Info("API: panel WebSocket endpoint enabled at %s", cfg.API.BindAddr)
		}
	}

	util.Debug("Configuration loaded: %+v", cfg)

	// Obsługa sygnałów
	color.Blue("Setting up signal handling for clean shutdown")
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Różne komunikaty dla różnych trybów
	if !*devMode {
		util.Info("Bot is running in daemon mode. Use 'kill -SIGTERM %d' to stop.", os.Getpid())
	} else {
		color.Green("Bot is running in development mode. Press Ctrl+C to exit.")
	}

	color.Blue("Waiting for shutdown signal")
	<-sigs

	// Zamykanie aplikacji
	color.Yellow("Shutdown signal received")
	if apiCancel != nil {
		apiCancel()
	}
	botManager.Stop()
	util.Info("Application has been shut down.")
	color.Green("Application has been shut down.")
}
