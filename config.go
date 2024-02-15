package main

import (
	"os"

	"github.com/goccy/go-yaml"
	log "github.com/sirupsen/logrus"
)

type MatrixConfig struct {
	Homeserver string `yaml:"homeserver"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
}

type Config struct {
	Address                string         `yaml:"address"`
	OwnHomeserver          MatrixConfig   `yaml:"own_homeserver"`
	RemoteHomeservers      []MatrixConfig `yaml:"remote_homeservers"`
	PingRoom               string         `yaml:"ping_room"`
	PingRateSeconds        int            `yaml:"ping_rate_seconds"`
	PingThresholdSeconds   int            `yaml:"ping_threshold_seconds"`
	PingJsonURL            string         `yaml:"ping_json_url"`
	BlacklistedHomeservers []string       `yaml:"blacklisted_homeservers"`
}

func ReadConfig() Config {
	var err error
	var b []byte
	var config Config
	if b, err = os.ReadFile("config.yaml"); err != nil {
		log.Errorf("Failed to read config file: %s", err)
		os.Exit(1)
	}

	// Load yaml
	if err := yaml.Unmarshal(b, &config); err != nil {
		log.Errorf("Failed to load config: %s", err)
		os.Exit(1)
	}

	// Ensure we have a PingRoom
	if config.PingRoom == "" {
		log.Errorln("No ping room defined")
		os.Exit(1)
	}

	// Ensure we have a PingJsonURL
	if config.PingJsonURL == "" {
		log.Errorln("No ping json url defined")
		os.Exit(1)
	}

	// Ensure we have a PingRateSeconds
	if config.PingRateSeconds == 0 {
		config.PingRateSeconds = 60
	}

	// Ensure we have a PingThresholdSeconds
	if config.PingThresholdSeconds == 0 {
		config.PingThresholdSeconds = 240
	}

	// Ensure the matrix homeserver values are not empty
	if config.OwnHomeserver.Homeserver == "" {
		log.Errorln("No own homeserver defined")
		os.Exit(1)
	}
	if config.OwnHomeserver.Username == "" {
		log.Errorln("No own homeserver username defined")
		os.Exit(1)
	}
	if config.OwnHomeserver.Password == "" {
		log.Errorln("No own homeserver password defined")
		os.Exit(1)
	}

	// Ensure we have at least one Remote Homeserver
	if len(config.RemoteHomeservers) == 0 {
		log.Errorln("No remote homeservers defined")
		os.Exit(1)
	}

	// Ensure the remote homeserver values are not empty
	for n, remoteHomeserver := range config.RemoteHomeservers {
		if remoteHomeserver.Homeserver == "" {
			log.Errorf("No remote homeserver defined for homeserver %d", n)
			os.Exit(1)
		}
		if remoteHomeserver.Username == "" {
			log.Errorf("No remote homeserver username defined for homeserver %s", remoteHomeserver.Homeserver)
			os.Exit(1)
		}
		if remoteHomeserver.Password == "" {
			log.Errorf("No remote homeserver password defined for homeserver %s", remoteHomeserver.Homeserver)
			os.Exit(1)
		}
	}

	return config
}
