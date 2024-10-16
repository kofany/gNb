package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Global   GlobalConfig `yaml:"global"`
	Bots     []BotConfig  `yaml:"bots"`
	Channels []string     `yaml:"channels"`
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
	return &config, nil
}

func (cfg *BotConfig) ServerAddress() string {
	return fmt.Sprintf("%s:%d", cfg.Server, cfg.Port)
}
