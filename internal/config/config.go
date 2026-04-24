package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/fatih/color"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Global   GlobalConfig `yaml:"global"`
	Bots     []BotConfig  `yaml:"bots"`
	Channels []string     `yaml:"channels"`
	API      APIConfig    `yaml:"api"`
}

type APIConfig struct {
	Enabled        bool   `yaml:"enabled"`
	NodeName       string `yaml:"node_name"`
	BindAddr       string `yaml:"bind_addr"`
	AuthToken      string `yaml:"auth_token"`
	TLSCertFile    string `yaml:"tls_cert_file"`
	TLSKeyFile     string `yaml:"tls_key_file"`
	EventBuffer    int    `yaml:"event_buffer"`
	MaxConnections int    `yaml:"max_connections"`
}

func (a *APIConfig) ApplyDefaults() {
	if a.BindAddr == "" {
		a.BindAddr = "127.0.0.1:7766"
	}
	if a.EventBuffer <= 0 {
		a.EventBuffer = 1000
	}
	if a.MaxConnections <= 0 {
		a.MaxConnections = 4
	}
	if a.NodeName == "" {
		a.NodeName = "gnb-node"
	}
}

func (a *APIConfig) Validate() error {
	if !a.Enabled {
		return nil
	}
	if len(a.AuthToken) < 32 {
		return fmt.Errorf("api.auth_token must be at least 32 characters when api.enabled: true")
	}
	if (a.TLSCertFile == "") != (a.TLSKeyFile == "") {
		return fmt.Errorf("api.tls_cert_file and api.tls_key_file must be set together or both empty")
	}
	if _, _, err := net.SplitHostPort(a.BindAddr); err != nil {
		return fmt.Errorf("api.bind_addr invalid: %w", err)
	}
	return nil
}

func generateAPIToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "PLEASE_SET_A_64_CHAR_HEX_TOKEN"
	}
	return hex.EncodeToString(b[:])
}

type GlobalConfig struct {
	LogLevel            string   `yaml:"log_level"`
	IsonInterval        int      `yaml:"ison_interval"`
	MaxNickLength       int      `yaml:"max_nick_length"`
	CommandPrefixes     []string `yaml:"owner_command_prefixes"`
	NickAPI             NickAPI  `yaml:"nick_api"`
	Channels            []string `yaml:"channels"`
	ReconnectRetries    int      `yaml:"reconnect_retries"`
	ReconnectInterval   int      `yaml:"reconnect_interval"`
	MassCommandCooldown int      `yaml:"mass_command_cooldown"`
}

type NickAPI struct {
	URL           string `yaml:"url"`
	MaxWordLength int    `yaml:"max_word_length"`
	Timeout       int    `yaml:"timeout"`
}

type BotConfig struct {
	Server string `yaml:"server"`
	Port   int    `yaml:"port"`
	SSL    bool   `yaml:"ssl"`
	Vhost  string `yaml:"vhost"`
}

func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}
	config.API.ApplyDefaults()
	if err := config.API.Validate(); err != nil {
		return nil, err
	}
	return &config, nil
}

func (cfg *BotConfig) ServerAddress() string {
	return fmt.Sprintf("%s:%d", cfg.Server, cfg.Port)
}

func CheckAndCreateConfigFiles() error {
	folders := []string{"configs", "data"}
	files := map[string]string{
		"configs/config.yaml": `global:
  # Please do not play with global values if you are not sure what you are doing
  log_level: warning  # Logging level: debug, info, warning, error
  ison_interval: 1  # Interval in seconds between ISON requests
  nick_api:
    url: 'https://i.got.al/words.php'
    max_word_length: 12
    timeout: 5  # Timeout for API requests in seconds
  max_nick_length: 15
  owner_command_prefixes:
    - "!"
    - "."
    - "@"
  reconnect_retries: 3
  reconnect_interval: 2
  mass_command_cooldown: 5

bots:
  - server: mirc.irc.al  #example server
    port: 6667
    ssl: false
    vhost: 192.168.176.35  # example IPv4
  - server: mirc.irc.al
    port: 6667
    ssl: false
    vhost: 2a02:2454:ffff:0101:1c56:2b73:771e:f9dd  # example IPv6

channels:
  - "#irc.al"  #example channel

owner_command_prefixes:
  - "!"
  - "."
  - "@"

# Panel API (WebSocket). Set enabled: true to turn on, then restart.
# Bind defaults to 127.0.0.1. Expose via cloudflared for wss://.
# api:
#   enabled: false
#   node_name: "my-node"
#   bind_addr: "127.0.0.1:7766"
#   auth_token: "REPLACE_WITH_GENERATED_TOKEN"
#   tls_cert_file: ""
#   tls_key_file: ""
#   event_buffer: 1000
#   max_connections: 4`,
		"configs/owners.json": `{
  "owners": [
    "*!*ident@hostname"
  ]
}`,
		"data/nicks.json": `{
  "nicks": [
    "CoolBot",
    "NickKeeper",
    "IRCGuardian",
    "NetWatcher"
  ]
}`,
	}

	missingItems := []string{}

	cyan := color.New(color.FgCyan).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()

	fmt.Println(cyan("Checking core files:"))

	// Check and create folders
	for _, folder := range folders {
		fmt.Printf("%-25s", cyan(folder))
		if _, err := os.Stat(folder); os.IsNotExist(err) {
			if err := os.MkdirAll(folder, 0755); err != nil {
				fmt.Println(red("[ ERROR ]"))
				return fmt.Errorf("failed to create folder %s: %v", folder, err)
			}
			missingItems = append(missingItems, folder)
			fmt.Println(yellow("[ CREATED ]"))
		} else {
			fmt.Println(green("[ OK ]"))
		}
	}

	// Check and create files
	for file, content := range files {
		fmt.Printf("%-25s", cyan(file))
		if _, err := os.Stat(file); os.IsNotExist(err) {
			if file == "configs/config.yaml" {
				content = strings.Replace(content, "REPLACE_WITH_GENERATED_TOKEN", generateAPIToken(), 1)
			}
			if err := os.WriteFile(file, []byte(content), 0644); err != nil {
				fmt.Println(red("[ ERROR ]"))
				return fmt.Errorf("failed to create file %s: %v", file, err)
			}
			missingItems = append(missingItems, file)
			fmt.Println(yellow("[ CREATED ]"))
		} else {
			fmt.Println(green("[ OK ]"))
		}
	}

	if len(missingItems) > 0 {
		fmt.Println("\n" + yellow("The following items were missing and have been created with example content:"))
		for _, item := range missingItems {
			fmt.Printf("- %s\n", cyan(item))
		}
		fmt.Println("\n" + yellow("Please edit these files with your desired configuration before running the bot again."))
		fmt.Println(red("Exiting the program."))
		os.Exit(1)
	}

	fmt.Println("\n" + green("All necessary folders and files are present."))
	return nil
}
