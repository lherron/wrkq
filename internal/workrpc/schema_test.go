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
		"Outcome:outcome,omitempty:*string:string",
		"CreatedByPrincipalRef:createdByPrincipalRef,omitempty:string:string",
	} {
		if !strings.Contains(fingerprint, want) {
			t.Fatalf("WrkqTask schema fingerprint missing %q:\n%s", want, fingerprint)
		}
	}
}

func TestProtocolSchemaHashIncludesInitializeHandshakeShape(t *testing.T) {
	typ := dtoSchemaTypes["RPCInitializeResult"]
	if typ == nil {
		t.Fatal("RPCInitializeResult missing from dtoSchemaTypes")
	}
	fingerprint := dtoSchemaFingerprint("RPCInitializeResult", typ)
	for _, want := range []string{
		"ProtocolSchemaHash:protocolSchemaHash:string:string",
		"Revision:revision:string:string",
	} {
		if !strings.Contains(fingerprint, want) {
			t.Fatalf("initialize schema fingerprint missing %q:\n%s", want, fingerprint)
		}
	}
}

func TestProtocolSchemaHashIncludesExactLabelFilterParams(t *testing.T) {
	for _, name := range []string{"WrkqFindListViewParams", "WrkqSearchListViewParams"} {
		typ := dtoSchemaTypes[name]
		if typ == nil {
			t.Fatalf("%s missing from dtoSchemaTypes", name)
		}
		fingerprint := dtoSchemaFingerprint(name, typ)
		if !strings.Contains(fingerprint, "Labels:labels,omitempty:slice[string:string]") {
			t.Fatalf("%s schema fingerprint missing repeatable labels field:\n%s", name, fingerprint)
		}
	}
}

func TestProtocolSchemaHashIncludesActionClaimPredecessorShape(t *testing.T) {
	typ := dtoSchemaTypes["WrkfActionClaimPredecessor"]
	if typ == nil {
		t.Fatal("WrkfActionClaimPredecessor missing from dtoSchemaTypes")
	}
	fingerprint := dtoSchemaFingerprint("WrkfActionClaimPredecessor", typ)
	for _, want := range []string{
		"SettleStatus:settleStatus:string:string",
		"Settled:settled:bool:bool",
	} {
		if !strings.Contains(fingerprint, want) {
			t.Fatalf("action claim predecessor schema fingerprint missing %q:\n%s", want, fingerprint)
		}
	}
}

func TestProtocolSchemaHashIncludesPromiseContracts(t *testing.T) {
	for _, method := range []string{
		"wrkq.promise.add", "wrkq.promise.show", "wrkq.promise.list", "wrkq.promise.ready",
		"wrkq.promise.edit", "wrkq.promise.renew", "wrkq.promise.resolve", "wrkq.promise.abandon",
		"wrkq.promise.attach", "wrkq.promise.detach", "wrkq.promise.delete",
	} {
		if !contains(methodCatalog, method) {
			t.Errorf("%s missing from method catalog", method)
		}
	}
	for name, fields := range map[string][]string{
		"WrkqPromise":           {"OwnerPrincipalRef:ownerPrincipalRef:string:string", "SubjectRef:subjectRef:*wrkqapi.WrkqPromiseSubjectRef", "ReviewAt:reviewAt:string:string"},
		"WrkqPromiseAddParams":  {"ReviewAt:reviewAt,omitempty:string:string", "ReviewIn:reviewIn,omitempty:string:string", "OnBehalf:onBehalf,omitempty:bool:bool"},
		"WrkqPromiseEditParams": {"IfMatch:ifMatch,omitempty:int64:int64"},
	} {
		typ := dtoSchemaTypes[name]
		if typ == nil {
			t.Fatalf("%s missing from dtoSchemaTypes", name)
		}
		fingerprint := dtoSchemaFingerprint(name, typ)
		for _, field := range fields {
			if !strings.Contains(fingerprint, field) {
				t.Errorf("%s schema missing %q:\n%s", name, field, fingerprint)
			}
		}
	}
}

func TestProtocolSchemaHashIncludesWorkflowInstancesEnvelope(t *testing.T) {
	typ := dtoSchemaTypes["WrkqWorkflowInstancesResult"]
	if typ == nil {
		t.Fatal("WrkqWorkflowInstancesResult missing from dtoSchemaTypes")
	}
	fingerprint := dtoSchemaFingerprint("WrkqWorkflowInstancesResult", typ)
	if want := "Instances:instances:slice[*workflow.Instance{"; !strings.Contains(fingerprint, want) {
		t.Fatalf("workflow instances schema fingerprint missing %q:\n%s", want, fingerprint)
	}
	if !contains(methodCatalog, "wrkq.workflow.instances") {
		t.Fatal("wrkq.workflow.instances missing from the canonical method catalog")
	}
	if contains(methodCatalog, "wrkf.task.instances") {
		t.Fatal("forbidden wrkf.task.instances method entered the canonical catalog")
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
