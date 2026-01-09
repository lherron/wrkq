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
	ProjectRoot      string `yaml:"project_root"`
	LogLevel         string `yaml:"log_level"`
	Output           string `yaml:"output"`
	Pager            string `yaml:"pager"`
}

// Load loads configuration from multiple sources with precedence:
// 1. Environment variables
// 2. ./.env.local (dotenv) - walks up parent directories to find it
// 3. ~/.config/wrkq/config.yaml (YAML)
func Load() (*Config, error) {
	cfg := &Config{
		AttachmentsMaxMB: 50,
		LogLevel:         "info",
		Output:           "table",
	}

	// Load .env.local if it exists (walking up parent directories)
	if envPath := findEnvLocal(); envPath != "" {
		_ = godotenv.Load(envPath)
	}

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
	if projectRoot := os.Getenv("WRKQ_PROJECT_ROOT"); projectRoot != "" {
		cfg.ProjectRoot = projectRoot
	}

	// Set defaults if not configured
	if cfg.DBPath == "" {
		// Check for project-local database first
		if _, err := os.Stat(".wrkq/wrkq.db"); err == nil {
			cfg.DBPath = ".wrkq/wrkq.db"
		} else {
			// Fall back to user-global database
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("failed to get home directory: %w", err)
			}
			cfg.DBPath = filepath.Join(homeDir, ".local", "share", "wrkq", "wrkq.db")
		}
	}

	if cfg.AttachDir == "" {
		// Use project-local attachments if using local database
		if cfg.DBPath == ".wrkq/wrkq.db" {
			cfg.AttachDir = ".wrkq/attachments"
		} else {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("failed to get home directory: %w", err)
			}
			cfg.AttachDir = filepath.Join(homeDir, ".local", "share", "wrkq", "attachments")
		}
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

// findEnvLocal searches for .env.local starting from cwd and walking up
// parent directories. Stops at the user's home directory.
// Returns the path to .env.local if found, empty string otherwise.
func findEnvLocal() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// If we can't get home dir, just check cwd
		if _, err := os.Stat(".env.local"); err == nil {
			return ".env.local"
		}
		return ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Clean paths for reliable comparison
	homeDir = filepath.Clean(homeDir)
	dir := filepath.Clean(cwd)

	for {
		envPath := filepath.Join(dir, ".env.local")
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}

		// Stop if we've reached home directory
		if dir == homeDir {
			break
		}

		// Get parent directory
		parent := filepath.Dir(dir)

		// Stop if we've reached the filesystem root
		if parent == dir {
			break
		}

		dir = parent
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
