package workrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestProtocolSchemaHashIncludesDTOShape(t *testing.T) {
	if got, legacy := ProtocolSchemaHash(), legacyNameOnlyProtocolHash(); got == legacy {
		t.Fatalf("ProtocolSchemaHash is still name-only: %s", got)
	}

	taskType := dtoSchemaTypes["WrkqTask"]
	if taskType == nil {
		t.Fatal("WrkqTask missing from dtoSchemaTypes")
	}
	fingerprint := dtoSchemaFingerprint("WrkqTask", taskType)
	for _, want := range []string{
		"Title:title:string:string",
		"ProjectUUID:projectUuid:string:string",
		"CreatedByPrincipalRef:createdByPrincipalRef,omitempty:string:string",
	} {
		if !strings.Contains(fingerprint, want) {
			t.Fatalf("WrkqTask schema fingerprint missing %q:\n%s", want, fingerprint)
		}
	}
}

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

func legacyNameOnlyProtocolHash() string {
	h := sha256.New()
	_, _ = fmt.Fprintln(h, "protocolVersion:"+ProtocolVersion)
	for _, method := range MethodCatalog() {
		_, _ = fmt.Fprintln(h, "method:"+method)
	}
	for _, code := range ErrorCodeCatalog() {
		_, _ = fmt.Fprintln(h, "error:"+code)
	}
	for _, dto := range dtoCatalog {
		_, _ = fmt.Fprintln(h, "dto:"+dto)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
