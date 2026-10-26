package workrpc

import (
	"reflect"
	"testing"
)

func TestDTOSchemaCatalogCoversEveryProtocolDTO(t *testing.T) {
	for _, name := range dtoCatalog {
		if dtoSchemaTypes[name] == nil {
			t.Errorf("%s is in dtoCatalog but has no schema type", name)
		}
	}
	for name := range dtoSchemaTypes {
		if !contains(dtoCatalog, name) {
			t.Errorf("%s has a schema type but is not in dtoCatalog", name)
		}
	}
}

func TestDTOSchemaFingerprintChangesForFieldAndTagDrift(t *testing.T) {
	type baseline struct {
		ID    string `json:"id"`
		Title string `json:"title,omitempty"`
	}
	type tagDrift struct {
		ID    string `json:"id"`
		Title string `json:"name,omitempty"`
	}
	type fieldDrift struct {
		ID    string `json:"id"`
		Title string `json:"title,omitempty"`
		State string `json:"state"`
	}

	base := dtoSchemaFingerprint("TestDTO", reflect.TypeOf(baseline{}))
	if got := dtoSchemaFingerprint("TestDTO", reflect.TypeOf(tagDrift{})); got == base {
		t.Fatalf("tag drift did not perturb DTO schema fingerprint:\n%s", got)
	}
	if got := dtoSchemaFingerprint("TestDTO", reflect.TypeOf(fieldDrift{})); got == base {
		t.Fatalf("field drift did not perturb DTO schema fingerprint:\n%s", got)
	}
}
