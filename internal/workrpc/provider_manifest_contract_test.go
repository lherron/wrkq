package workrpc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/workrpc"
	"gopkg.in/yaml.v3"
)

func TestProviderManifestProtocolVersionsMatchRuntime(t *testing.T) {
	manifest := readProviderManifest(t)

	if len(manifest.Bindings) == 0 {
		t.Fatal("cap/provider.wrkq.yaml must declare at least one binding")
	}

	for _, binding := range manifest.Bindings {
		got := binding.Lifecycle.Initialize.Params.ProtocolVersion
		if got != workrpc.ProtocolVersion {
			t.Errorf(
				"binding %q rpc.initialize protocolVersion = %q, want %q",
				binding.ID,
				got,
				workrpc.ProtocolVersion,
			)
		}
	}

	got := manifest.XSource.ProtocolVersion.Value
	if got != workrpc.ProtocolVersion {
		t.Errorf(
			"x-source.protocolVersion.value = %q, want %q",
			got,
			workrpc.ProtocolVersion,
		)
	}
}

func readProviderManifest(t *testing.T) providerManifest {
	t.Helper()

	path := filepath.Join("..", "..", "cap", "provider.wrkq.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var manifest providerManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return manifest
}

type providerManifest struct {
	Bindings []struct {
		ID        string `yaml:"id"`
		Lifecycle struct {
			Initialize struct {
				Params struct {
					ProtocolVersion string `yaml:"protocolVersion"`
				} `yaml:"params"`
			} `yaml:"initialize"`
		} `yaml:"lifecycle"`
	} `yaml:"bindings"`
	XSource struct {
		ProtocolVersion struct {
			Value string `yaml:"value"`
		} `yaml:"protocolVersion"`
	} `yaml:"x-source"`
}
