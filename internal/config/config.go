package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	DBPath           string `yaml:"db_path"`
	AttachDir        string `yaml:"attach_dir"`
	AttachmentsMaxMB int    `yaml:"attachments_max_mb"`
	DefaultActor     string `yaml:"default_actor"`
	LogLevel         string `yaml:"log_level"`
	Output           string `yaml:"output"`
	Pager            string `yaml:"pager"`
}

// Load loads configuration from multiple sources with precedence:
// 1. Environment variables
// 2. ./.env.local (dotenv)
// 3. ~/.config/wrkq/config.yaml (YAML)
func Load() (*Config, error) {
	cfg := &Config{
		AttachmentsMaxMB: 50,
		LogLevel:         "info",
		Output:           "table",
	}

	// Load .env.local if it exists
	_ = godotenv.Load(".env.local")

	// Load ~/.config/wrkq/config.yaml if it exists
	if err := loadYAMLConfig(cfg); err != nil {
		// YAML config is optional, so we don't fail if it doesn't exist
	}

	// Override with environment variables
	if dbPath := getEnvOrFile("WRKQ_DB_PATH", "WRKQ_DB_PATH_FILE"); dbPath != "" {
		cfg.DBPath = dbPath
	}
	if attachDir := os.Getenv("WRKQ_ATTACH_DIR"); attachDir != "" {
		cfg.AttachDir = attachDir
	}
	if logLevel := os.Getenv("WRKQ_LOG_LEVEL"); logLevel != "" {
		cfg.LogLevel = logLevel
	}
	if output := os.Getenv("WRKQ_OUTPUT"); output != "" {
		cfg.Output = output
	}
	if pager := os.Getenv("WRKQ_PAGER"); pager != "" {
		cfg.Pager = pager
	}
	if defaultActor := os.Getenv("WRKQ_ACTOR"); defaultActor != "" {
		cfg.DefaultActor = defaultActor
	}

	// Set defaults if not configured
	if cfg.DBPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		cfg.DBPath = filepath.Join(homeDir, ".local", "share", "wrkq", "wrkq.db")
	}

	if cfg.AttachDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		cfg.AttachDir = filepath.Join(homeDir, ".local", "share", "wrkq", "attachments")
	}

	return cfg, nil
}

// loadYAMLConfig loads configuration from ~/.config/wrkq/config.yaml
func loadYAMLConfig(cfg *Config) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(homeDir, ".config", "wrkq", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(data, cfg)
}

// getEnvOrFile gets an environment variable value, or reads it from a file
// if the _FILE variant is set
func getEnvOrFile(envVar, fileVar string) string {
	if val := os.Getenv(envVar); val != "" {
		return val
	}

	if filePath := os.Getenv(fileVar); filePath != "" {
		data, err := os.ReadFile(filePath)
		if err == nil {
			return string(data)
		}
	}

	return ""
}

// GetActorID returns the current actor ID from environment or config
// Priority: WRKQ_ACTOR_ID > WRKQ_ACTOR > config.default_actor
func (c *Config) GetActorID() string {
	if actorID := os.Getenv("WRKQ_ACTOR_ID"); actorID != "" {
		return actorID
	}
	if actor := os.Getenv("WRKQ_ACTOR"); actor != "" {
		return actor
	}
	return c.DefaultActor
}
