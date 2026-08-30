package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/wrkqapi"
)

const roomWaitPollInterval = 100 * time.Millisecond

// RoomService exposes the wrkc say/inbox/log/show surface.
type RoomService struct{ client *Client }

type RoomSayOptions struct {
	To             []string
	FYI            bool
	New            bool
	RespondTo      string
	Record         bool
	IdempotencyKey string
	Meta           map[string]any
	Wait           bool
	Timeout        time.Duration
}

// RoomSayResult is the canonical say receipt plus client-owned wait results.
type RoomSayResult struct {
	wrkqapi.WrkqRoomSayResult
	Replies  []Envelope `json:"-"`
	Failures []Envelope `json:"-"`
}

type RoomInboxOptions struct {
	IncludeFailed bool
	ScopeRef      string
}

type RoomLogOptions struct {
	Task  string
	Limit int
}

// RoomShowResult contains exactly one of Room or Envelope.
type RoomShowResult struct {
	Room     *Room
	Envelope *Envelope
}

// WaitError reports a timed-out, cancelled, or failed say --wait while keeping
// the complete receipt available to the caller.
type WaitError struct {
	Reason string
	Unmet  []string
	Result *RoomSayResult
	Cause  error
}

func (e *WaitError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("room say wait %s: %v", e.Reason, e.Cause)
	}
	if len(e.Unmet) > 0 {
		return fmt.Sprintf("room say wait %s; still open: %s", e.Reason, strings.Join(e.Unmet, ", "))
	}
	return "room say wait " + e.Reason
}

func (e *WaitError) Unwrap() error { return e.Cause }

func (s RoomService) Say(ref, body string, options ...RoomSayOptions) (*RoomSayResult, error) {
	principal, err := s.client.mutationPrincipal()
	if err != nil {
		return nil, err
	}
	opts := first(options)
	if opts.Wait && len(opts.To) == 0 {
		return nil, errors.New("room say wait requires at least one addressee")
	}
	params := struct {
		Ref            string         `json:"ref,omitempty"`
		Body           string         `json:"body"`
		To             []string       `json:"to,omitempty"`
		FYI            bool           `json:"fyi,omitempty"`
		New            bool           `json:"new,omitempty"`
		RespondTo      string         `json:"respondTo,omitempty"`
		Record         bool           `json:"record,omitempty"`
		IdempotencyKey string         `json:"idempotencyKey,omitempty"`
		Meta           map[string]any `json:"meta,omitempty"`
		PrincipalRef   string         `json:"principalRef,omitempty"`
		ScopeRef       string         `json:"scopeRef,omitempty"`
	}{ref, body, opts.To, opts.FYI, opts.New, opts.RespondTo, opts.Record, opts.IdempotencyKey, opts.Meta, principal, s.client.scopeRef}
	var out RoomSayResult
	if err := s.client.call("wrkq.room.say", params, &out); err != nil {
		return nil, err
	}
	if !opts.Wait {
		return &out, nil
	}
	if err := s.waitForGroup(&out, opts.Timeout); err != nil {
		return &out, err
	}
	return &out, nil
}

func (s RoomService) Inbox(options ...RoomInboxOptions) (*EnvelopeInboxView, error) {
	opts := first(options)
	scopeRef := opts.ScopeRef
	if scopeRef == "" {
		scopeRef = s.client.scopeRef
	}
	params := struct {
		ScopeRef      string `json:"scopeRef,omitempty"`
		IncludeFailed bool   `json:"includeFailed,omitempty"`
		PrincipalRef  string `json:"principalRef,omitempty"`
	}{scopeRef, opts.IncludeFailed, s.client.principalRef}
	var out EnvelopeInboxView
	if err := s.client.call("wrkq.envelope.inboxView", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s RoomService) Log(room string, options ...RoomLogOptions) (*RoomLog, error) {
	opts := first(options)
	params := struct {
		Room         string `json:"room"`
		Task         string `json:"task,omitempty"`
		Limit        int    `json:"limit,omitempty"`
		PrincipalRef string `json:"principalRef,omitempty"`
		ScopeRef     string `json:"scopeRef,omitempty"`
	}{room, opts.Task, opts.Limit, s.client.principalRef, s.client.scopeRef}
	var out RoomLog
	if err := s.client.call("wrkq.room.logView", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s RoomService) Show(selector string) (*RoomShowResult, error) {
	result := &RoomShowResult{}
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(selector)), "EN-") {
		params := struct {
			Envelope     string `json:"envelope"`
			PrincipalRef string `json:"principalRef,omitempty"`
			ScopeRef     string `json:"scopeRef,omitempty"`
		}{selector, s.client.principalRef, s.client.scopeRef}
		var envelope Envelope
		if err := s.client.call("wrkq.envelope.show", params, &envelope); err != nil {
			return nil, err
		}
		result.Envelope = &envelope
		return result, nil
	}
	params := struct {
		Room         string `json:"room"`
		PrincipalRef string `json:"principalRef,omitempty"`
		ScopeRef     string `json:"scopeRef,omitempty"`
	}{selector, s.client.principalRef, s.client.scopeRef}
	var room Room
	if err := s.client.call("wrkq.room.show", params, &room); err != nil {
		return nil, err
	}
	result.Room = &room
	return result, nil
}

func (s RoomService) waitForGroup(result *RoomSayResult, timeout time.Duration) error {
	if result.GroupID == "" {
		return &WaitError{Reason: "returned no group", Result: result}
	}
	ctx := s.client.ctx
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	ticker := time.NewTicker(roomWaitPollInterval)
	defer ticker.Stop()
	for {
		var snapshot struct {
			Met   bool     `json:"met"`
			Unmet []string `json:"unmet"`
		}
		if err := s.client.Call(ctx, "wrkq.monitor.stateView", struct {
			Tasks     []string `json:"tasks"`
			Condition string   `json:"condition"`
		}{[]string{result.GroupID}, "terminal"}, &snapshot); err != nil {
			return &WaitError{Reason: "failed", Result: result, Cause: err}
		}
		if snapshot.Met {
			return s.collectWaitResult(result)
		}
		select {
		case <-ctx.Done():
			return &WaitError{Reason: "cancelled", Unmet: snapshot.Unmet, Result: result, Cause: context.Cause(ctx)}
		case <-timer:
			return &WaitError{Reason: "timed out", Unmet: snapshot.Unmet, Result: result, Cause: context.DeadlineExceeded}
		case <-ticker.C:
		}
	}
}

func (s RoomService) collectWaitResult(result *RoomSayResult) error {
	log, err := s.Log(result.Room.Key)
	if err != nil {
		return &WaitError{Reason: "failed to read replies", Result: result, Cause: err}
	}
	sent := map[string]bool{}
	counterparties := map[string]bool{}
	sender := ""
	var lastSeq int64
	for _, envelope := range result.Envelopes {
		sent[envelope.ID] = true
		sender = envelope.From.PrincipalRef
		if envelope.To != nil {
			counterparties[envelope.To.PrincipalRef] = true
		}
		if envelope.MessageSeq > lastSeq {
			lastSeq = envelope.MessageSeq
		}
	}
	for _, envelope := range log.Items {
		if sent[envelope.ID] && envelope.State == "failed" {
			result.Failures = append(result.Failures, envelope)
		}
		if sent[envelope.ID] || envelope.MessageSeq <= lastSeq || !counterparties[envelope.From.PrincipalRef] {
			continue
		}
		if envelope.To != nil && envelope.To.PrincipalRef == sender {
			result.Replies = append(result.Replies, envelope)
		}
	}
	if len(result.Failures) > 0 {
		return &WaitError{Reason: "ended with failed envelopes", Result: result}
	}
	return nil
}
