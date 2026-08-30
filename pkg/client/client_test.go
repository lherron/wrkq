package client

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type fakeTransport struct {
	calls  []fakeCall
	result func(method string, params any) json.RawMessage
}

type fakeCall struct {
	method string
	params any
}

func (f *fakeTransport) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeCall{method: method, params: params})
	return f.result(method, params), nil
}

func (f *fakeTransport) Close() error { return nil }

func TestTypedHelpersInjectNormalizedPrincipalAndDecode(t *testing.T) {
	transport := &fakeTransport{result: func(method string, _ any) json.RawMessage {
		switch method {
		case "wrkq.task.show", "wrkq.task.update":
			return json.RawMessage(`{"id":"T-00001","state":"open","labels":[],"meta":{}}`)
		case "wrkq.comment.add":
			return json.RawMessage(`{"id":"C-00001","body":"hello","meta":{}}`)
		default:
			return json.RawMessage(`{}`)
		}
	}}
	client, err := New(t.Context(), WithTransport(transport), WithPrincipalRef("agent:cody:project:wrkq"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	task, err := client.Task.Show("T-00001")
	if err != nil || task.ID != "T-00001" {
		t.Fatalf("Task.Show = %#v, %v", task, err)
	}
	state := "open"
	if _, err := client.Task.Update("T-00001", TaskPatch{State: &state}, TaskUpdateOptions{IdempotencyKey: "update-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Comment.Add("T-00001", "hello", CommentAddOptions{IdempotencyKey: "comment-1"}); err != nil {
		t.Fatal(err)
	}

	for _, index := range []int{1, 2} {
		body, err := json.Marshal(transport.calls[index].params)
		if err != nil {
			t.Fatal(err)
		}
		var params map[string]any
		if err := json.Unmarshal(body, &params); err != nil {
			t.Fatal(err)
		}
		if got := params["actor"]; got != "agent:cody" {
			t.Fatalf("call %d actor = %#v, want agent:cody", index, got)
		}
		if params["idempotencyKey"] == "" {
			t.Fatalf("call %d lost idempotency key: %s", index, body)
		}
	}
}

func TestRoomSayWaitCollectsReplies(t *testing.T) {
	transport := &fakeTransport{result: func(method string, _ any) json.RawMessage {
		switch method {
		case "wrkq.room.say":
			return json.RawMessage(`{
				"room":{"uuid":"r1","key":"T-00001","kind":"task","work":"open","activity":"active","labels":[],"links":[],"openedByPrincipalRef":"agent:cody","openedAt":"now","lastActivityAt":"now","memberCount":2,"messageCount":1,"etag":1,"createdAt":"now","updatedAt":"now"},
				"groupId":"EN-00001",
				"envelopes":[{"uuid":"e1","id":"EN-00001","messageSeq":1,"roomUuid":"r1","roomKey":"T-00001","roomKind":"task","from":{"principalRef":"agent:cody"},"to":{"principalRef":"agent:mable"},"replyTo":"agent:cody","obligation":"reply_required","body":"go","state":"acked","terminal":true,"meta":{},"presentedTo":[],"etag":1,"createdAt":"now","updatedAt":"now"}],
				"acked":[]
			}`)
		case "wrkq.monitor.stateView":
			return json.RawMessage(`{"met":true,"unmet":[]}`)
		case "wrkq.room.logView":
			return json.RawMessage(`{
				"room":{"uuid":"r1","key":"T-00001","kind":"task","work":"open","activity":"active","labels":[],"links":[],"openedByPrincipalRef":"agent:cody","openedAt":"now","lastActivityAt":"now","memberCount":2,"messageCount":2,"etag":2,"createdAt":"now","updatedAt":"now"},
				"items":[
					{"uuid":"e1","id":"EN-00001","messageSeq":1,"roomUuid":"r1","roomKey":"T-00001","roomKind":"task","from":{"principalRef":"agent:cody"},"to":{"principalRef":"agent:mable"},"replyTo":"agent:cody","obligation":"reply_required","body":"go","state":"acked","terminal":true,"meta":{},"presentedTo":[],"etag":1,"createdAt":"now","updatedAt":"now"},
					{"uuid":"e2","id":"EN-00002","messageSeq":2,"roomUuid":"r1","roomKey":"T-00001","roomKind":"task","from":{"principalRef":"agent:mable"},"to":{"principalRef":"agent:cody"},"replyTo":"agent:mable","obligation":"reply_required","body":"done","state":"pending","terminal":false,"meta":{},"presentedTo":[],"etag":1,"createdAt":"now","updatedAt":"now"}
				]
			}`)
		default:
			return json.RawMessage(`{}`)
		}
	}}
	client, err := New(t.Context(), WithTransport(transport), WithPrincipalRef("agent:cody"), WithScopeRef("cody@wrkq:T-00001"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Room.Say("T-00001", "go", RoomSayOptions{
		To:      []string{"mable@wrkq:primary"},
		Wait:    true,
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Replies) != 1 || result.Replies[0].Body != "done" {
		t.Fatalf("replies = %#v", result.Replies)
	}
}

func TestCallDecodesTypedOutput(t *testing.T) {
	transport := &fakeTransport{result: func(string, any) json.RawMessage {
		return json.RawMessage(`{"id":"T-00001","state":"open","labels":[],"meta":{}}`)
	}}
	client, err := New(t.Context(), WithTransport(transport))
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	if err := client.Call(t.Context(), "wrkq.task.show", map[string]string{"task": "T-00001"}, &task); err != nil {
		t.Fatal(err)
	}
	if task.ID != "T-00001" {
		t.Fatalf("decoded id = %q", task.ID)
	}
}
