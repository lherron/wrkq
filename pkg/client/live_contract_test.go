package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type observingTransport struct {
	delegate Transport
	last     map[string]json.RawMessage
}

func (o *observingTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := o.delegate.Call(ctx, method, params)
	if err == nil {
		o.last[method] = append(json.RawMessage(nil), raw...)
	}
	return raw, err
}

func (o *observingTransport) Close() error { return o.delegate.Close() }

// TestLiveHelperContract is intentionally opt-in because it writes one comment,
// one resolved promise, one no-op task update, and one room log entry. It drives
// every public helper against the configured real daemon and rejects response
// keys that are absent from the public result DTO.
func TestLiveHelperContract(t *testing.T) {
	if os.Getenv("WRKQ_CLIENT_LIVE_CONTRACT") != "1" {
		t.Skip("set WRKQ_CLIENT_LIVE_CONTRACT=1 to exercise the configured live daemon")
	}
	taskID := envOr("WRKQ_CLIENT_CONTRACT_TASK", "T-07732")
	project := envOr("WRKQ_CLIENT_CONTRACT_PROJECT", "wrkq")
	nonce := time.Now().UTC().Format("20060102T150405.000000000")

	client, err := New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	observer := &observingTransport{delegate: client.transport, last: map[string]json.RawMessage{}}
	client.transport = observer
	defer func() { _ = client.Close() }()

	var rawTask Task
	must(t, "Call", client.Call(t.Context(), "wrkq.task.show", map[string]string{"task": taskID}, &rawTask))
	assertObserved(t, observer, "wrkq.task.show", reflect.TypeOf(Task{}))

	task, err := client.Task.Show(taskID)
	must(t, "Task.Show", err)
	assertObserved(t, observer, "wrkq.task.show", reflect.TypeOf(Task{}))

	tasks, err := client.Task.List(TaskListOptions{States: []string{task.State}, Limit: 1})
	must(t, "Task.List", err)
	if len(tasks.Items) == 0 {
		t.Fatal("Task.List returned no items")
	}
	assertObserved(t, observer, "wrkq.task.list", reflect.TypeOf(TaskListResult{}))

	state := task.State
	outcome := ""
	if task.Outcome != nil {
		outcome = *task.Outcome
	}
	labels := append([]string(nil), task.Labels...)
	updated, err := client.Task.Update(taskID, TaskPatch{State: &state, Outcome: &outcome, Labels: &labels}, TaskUpdateOptions{
		ExpectETag:     &task.ETag,
		IdempotencyKey: "go-client-contract-task-" + nonce,
	})
	must(t, "Task.Update", err)
	assertObserved(t, observer, "wrkq.task.update", reflect.TypeOf(Task{}))

	comment, err := client.Comment.Add(taskID, "Live Go client contract "+nonce, CommentAddOptions{IdempotencyKey: "go-client-contract-comment-" + nonce})
	must(t, "Comment.Add", err)
	assertObserved(t, observer, "wrkq.comment.add", reflect.TypeOf(Comment{}))

	promise, err := client.Promise.Add(PromiseAddOptions{Subject: "Live Go client contract " + nonce, Task: taskID, ReviewIn: "1h"})
	must(t, "Promise.Add", err)
	assertObserved(t, observer, "wrkq.promise.add", reflect.TypeOf(Promise{}))

	promises, err := client.Promise.List(PromiseListOptions{Task: taskID})
	must(t, "Promise.List", err)
	if len(promises.Items) == 0 {
		t.Fatal("Promise.List returned no items")
	}
	assertObserved(t, observer, "wrkq.promise.list", reflect.TypeOf(PromiseListResult{}))

	renewed, err := client.Promise.Renew(promise.ID, PromiseRenewOptions{ReviewIn: "2h", IfMatch: promise.ETag})
	must(t, "Promise.Renew", err)
	assertObserved(t, observer, "wrkq.promise.renew", reflect.TypeOf(Promise{}))

	resolved, err := client.Promise.Resolve(renewed.ID, PromiseResolveOptions{IfMatch: renewed.ETag})
	must(t, "Promise.Resolve", err)
	assertObserved(t, observer, "wrkq.promise.resolve", reflect.TypeOf(Promise{}))

	container, err := client.Container.Show(project)
	must(t, "Container.Show", err)
	assertObserved(t, observer, "wrkq.container.show", reflect.TypeOf(Container{}))

	say, err := client.Room.Say(taskID, "Live Go client contract "+nonce, RoomSayOptions{IdempotencyKey: "go-client-contract-say-" + nonce})
	must(t, "Room.Say", err)
	assertObserved(t, observer, "wrkq.room.say", reflect.TypeOf(RoomSayResult{}))

	inbox, err := client.Room.Inbox()
	must(t, "Room.Inbox", err)
	assertObserved(t, observer, "wrkq.envelope.inboxView", reflect.TypeOf(EnvelopeInboxView{}))

	log, err := client.Room.Log(say.Room.Key)
	must(t, "Room.Log", err)
	assertObserved(t, observer, "wrkq.room.logView", reflect.TypeOf(RoomLog{}))

	shown, err := client.Room.Show(say.Room.Key)
	must(t, "Room.Show", err)
	if shown.Room == nil {
		t.Fatal("Room.Show did not return a room")
	}
	assertObserved(t, observer, "wrkq.room.show", reflect.TypeOf(Room{}))

	t.Logf("contract ok: task=%s updated_etag=%d comment=%s promise=%s/%s container=%s room=%s inbox_groups=%d log_items=%d protocol_hash=pinned",
		updated.ID, updated.ETag, comment.ID, resolved.ID, resolved.State, container.Path, say.Room.Key, len(inbox.Groups), len(log.Items))
}

func must(t *testing.T, helper string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", helper, err)
	}
	t.Log(helper + ": ok")
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func assertObserved(t *testing.T, observer *observingTransport, method string, typ reflect.Type) {
	t.Helper()
	raw := observer.last[method]
	if len(raw) == 0 {
		t.Fatalf("%s produced no observed result", method)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s raw result: %v", method, err)
	}
	if err := diffJSONShape(value, typ, method); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: observed keys match %s", method, typ.Name())
}

func diffJSONShape(value any, typ reflect.Type, path string) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Interface || typ.Kind() == reflect.Map {
		return nil
	}
	switch current := value.(type) {
	case map[string]any:
		fields := jsonFields(typ)
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fieldType, ok := fields[key]
			if !ok {
				return fmt.Errorf("%s: observed undeclared JSON key %q in %s", path, key, typ)
			}
			if err := diffJSONShape(current[key], fieldType, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
			return fmt.Errorf("%s: observed array for %s", path, typ)
		}
		for index, item := range current {
			if err := diffJSONShape(item, typ.Elem(), fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func jsonFields(typ reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	if typ.Kind() != reflect.Struct {
		return fields
	}
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			for nestedName, nestedType := range jsonFields(field.Type) {
				fields[nestedName] = nestedType
			}
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}
