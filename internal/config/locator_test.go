package config

import "testing"

func TestApplyDBLocatorRemote(t *testing.T) {
	cfg := &Config{}
	if err := ApplyDBLocator(cfg, "rpc://max3", false); err != nil {
		t.Fatalf("ApplyDBLocator remote: %v", err)
	}
	if cfg.DBPath != "" {
		t.Fatalf("DBPath=%q want empty for remote locator", cfg.DBPath)
	}
	if cfg.RemoteEndpoint != "max3:7171" {
		t.Fatalf("RemoteEndpoint=%q want max3:7171", cfg.RemoteEndpoint)
	}
	if cfg.DBLocator != "rpc://max3" {
		t.Fatalf("DBLocator=%q want rpc://max3", cfg.DBLocator)
	}
}

func TestApplyDBLocatorPathOnlyRejectsRemote(t *testing.T) {
	cfg := &Config{}
	if err := ApplyDBLocator(cfg, "rpc://max3:7171", true); err == nil {
		t.Fatal("expected path-only remote rejection")
	}
}

func TestApplyDBLocatorLocalPath(t *testing.T) {
	cfg := &Config{}
	if err := ApplyDBLocator(cfg, "/tmp/wrkq.db", false); err != nil {
		t.Fatalf("ApplyDBLocator local: %v", err)
	}
	if cfg.DBPath != "/tmp/wrkq.db" || cfg.DBLocator != "/tmp/wrkq.db" || cfg.RemoteEndpoint != "" {
		t.Fatalf("local locator mismatch: %#v", cfg)
	}
}

func TestLoadExplicitWRKQDBRemoteWinsOverStaleRemoteDBPath(t *testing.T) {
	t.Setenv("WRKQ_DB", "rpc://max3:7171")
	t.Setenv("WRKQ_DB_PATH", "rpc://stale:7171")
	t.Setenv("WRKQ_DB_PATH_FILE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RemoteEndpoint != "max3:7171" {
		t.Fatalf("RemoteEndpoint=%q want max3:7171", cfg.RemoteEndpoint)
	}
	if cfg.DBPath != "" {
		t.Fatalf("DBPath=%q want empty for remote locator", cfg.DBPath)
	}
}

func TestLoadRemoteDBPathWithoutWRKQDBIsRejected(t *testing.T) {
	t.Setenv("WRKQ_DB", "")
	t.Setenv("WRKQ_DB_PATH", "rpc://stale:7171")
	t.Setenv("WRKQ_DB_PATH_FILE", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected path-only WRKQ_DB_PATH rejection")
	}
}
