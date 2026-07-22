package store

import (
	"reflect"
	"testing"

	"github.com/lherron/wrkq/internal/domain"
)

func TestAdornmentsStoreModelsCarryNullableColumns(t *testing.T) {
	for _, test := range []struct {
		model   reflect.Type
		field   string
		jsonTag string
		dbTag   string
	}{
		{reflect.TypeOf(domain.Task{}), "Outcome", "outcome,omitempty", "outcome"},
		{reflect.TypeOf(domain.Task{}), "CampaignUUID", "campaign_uuid,omitempty", "campaign_uuid"},
		{reflect.TypeOf(domain.Container{}), "CampaignState", "campaign_state,omitempty", "campaign_state"},
		{reflect.TypeOf(domain.Container{}), "Specification", "specification,omitempty", "specification"},
		{reflect.TypeOf(domain.Comment{}), "TaskUUID", "task_uuid,omitempty", "task_uuid"},
		{reflect.TypeOf(domain.Comment{}), "ContainerUUID", "container_uuid,omitempty", "container_uuid"},
		{reflect.TypeOf(domain.Comment{}), "Kind", "kind,omitempty", "kind"},
	} {
		t.Run(test.model.Name()+"/"+test.field, func(t *testing.T) {
			field, ok := test.model.FieldByName(test.field)
			if !ok {
				t.Fatalf("%s missing field %s", test.model.Name(), test.field)
			}
			if field.Type.Kind() != reflect.Pointer || field.Type.Elem().Kind() != reflect.String {
				t.Errorf("%s.%s type=%s, want nullable string", test.model.Name(), test.field, field.Type)
			}
			if got := field.Tag.Get("json"); got != test.jsonTag {
				t.Errorf("%s.%s json tag=%q, want %q", test.model.Name(), test.field, got, test.jsonTag)
			}
			if got := field.Tag.Get("db"); got != test.dbTag {
				t.Errorf("%s.%s db tag=%q, want %q", test.model.Name(), test.field, got, test.dbTag)
			}
		})
	}
}
