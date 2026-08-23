//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

var promiseDayDuration = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)d`)

func (a *API) PromiseAdd(ctx context.Context, p PromiseAddParams) (*WrkqPromise, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	owner := attr.PrincipalRef
	if strings.TrimSpace(p.OwnerPrincipalRef) != "" {
		owner, err = normalizePromiseOwner(p.OwnerPrincipalRef)
		if err != nil {
			return nil, err
		}
	}
	var onBehalfAssertedBy *string
	if owner != attr.PrincipalRef {
		if !p.OnBehalf {
			return nil, NewForbiddenError("creating a promise for another owner requires onBehalf", map[string]any{
				"ownerPrincipalRef":  owner,
				"callerPrincipalRef": attr.PrincipalRef,
			})
		}
		onBehalfAssertedBy = &attr.PrincipalRef
	}

	reviewAt, _, err := normalizePromiseReviewTime(p.ReviewAt, p.ReviewIn, true)
	if err != nil {
		return nil, err
	}
	taskUUID, containerUUID, defaultSubject, err := a.resolvePromiseTarget(p.Task, p.Container)
	if err != nil {
		return nil, err
	}
	subject := strings.TrimSpace(p.Subject)
	if subject == "" {
		subject = defaultSubject
	}
	if subject == "" {
		return nil, NewValidationError("promise subject is required for a standalone promise", map[string]any{"field": "subject"})
	}

	created, err := a.store.Promises.CreateWithAttribution(attr, store.PromiseCreateParams{
		OwnerPrincipalRef: owner, Subject: subject, ReviewQuestion: p.ReviewQuestion,
		SubjectTaskUUID: taskUUID, SubjectContainerUUID: containerUUID,
		ReviewAt: reviewAt, Meta: metaString(p.Meta), OnBehalfAssertedBy: onBehalfAssertedBy,
	})
	if err != nil {
		return nil, mapPromiseStoreError(err, "")
	}
	return a.promiseDTO(ctx, created)
}

func (a *API) PromiseShow(ctx context.Context, p PromiseShowParams) (*WrkqPromise, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	promise, err := a.getPromise(p.Promise)
	if err != nil {
		return nil, err
	}
	return a.promiseDTO(ctx, promise)
}

func (a *API) PromiseList(ctx context.Context, p PromiseListParams) (*WrkqPromiseListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owner, err := a.promiseReadOwner(p.OwnerPrincipalRef, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	var state domain.PromiseState
	if strings.TrimSpace(p.State) != "" && p.State != "all" {
		state = domain.PromiseState(p.State)
		if err := domain.ValidatePromiseState(state); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "state"})
		}
	}
	taskUUID, containerUUID, _, err := a.resolvePromiseTarget(p.Task, p.Container)
	if err != nil {
		return nil, err
	}
	params := store.PromiseListParams{OwnerPrincipalRef: owner, State: state}
	if taskUUID != nil {
		params.SubjectTaskUUID = *taskUUID
	}
	if containerUUID != nil {
		params.SubjectContainerUUID = *containerUUID
	}
	rows, err := a.store.Promises.List(params)
	if err != nil {
		return nil, mapPromiseStoreError(err, "")
	}
	return a.promiseListDTO(ctx, rows)
}

func (a *API) PromiseReady(ctx context.Context, p PromiseReadyParams) (*WrkqPromiseListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owner, err := a.promiseReadOwner(p.OwnerPrincipalRef, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	rows, err := a.store.Promises.Ready(owner)
	if err != nil {
		return nil, mapPromiseStoreError(err, "")
	}
	return a.promiseListDTO(ctx, rows)
}

func (a *API) PromiseEdit(ctx context.Context, p PromiseEditParams) (*WrkqPromise, error) {
	promise, attr, err := a.ownedPromise(ctx, p.Promise, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	fields := map[string]interface{}{}
	if p.Subject != nil {
		subject := strings.TrimSpace(*p.Subject)
		if subject == "" {
			return nil, NewValidationError("promise subject is required", map[string]any{"field": "subject"})
		}
		fields["subject"] = subject
	}
	if p.ReviewQuestion != nil {
		if strings.TrimSpace(*p.ReviewQuestion) == "" {
			fields["review_question"] = nil
		} else {
			fields["review_question"] = *p.ReviewQuestion
		}
	}
	if p.Meta != nil {
		encoded := metaString(*p.Meta)
		if encoded == nil {
			fields["meta"] = nil
		} else {
			fields["meta"] = *encoded
		}
	}
	if reviewAt, supplied, nerr := normalizePromiseReviewTime(p.ReviewAt, p.ReviewIn, false); nerr != nil {
		return nil, nerr
	} else if supplied {
		fields["review_at"] = reviewAt
	}
	if len(fields) == 0 {
		return nil, NewValidationError("at least one promise edit field is required", nil)
	}
	if _, err := a.store.Promises.UpdateFieldsWithAttribution(attr, promise.UUID, fields, p.IfMatch); err != nil {
		return nil, mapPromiseStoreError(err, p.Promise)
	}
	updated, err := a.store.Promises.GetByUUID(promise.UUID)
	if err != nil {
		return nil, mapPromiseStoreError(err, p.Promise)
	}
	return a.promiseDTO(ctx, updated)
}

func (a *API) PromiseRenew(ctx context.Context, p PromiseReviewParams) (*WrkqPromise, error) {
	promise, attr, err := a.ownedPromise(ctx, p.Promise, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	reviewAt, _, err := normalizePromiseReviewTime(p.ReviewAt, p.ReviewIn, true)
	if err != nil {
		return nil, err
	}
	updated, err := a.store.Promises.RenewWithAttribution(attr, promise.UUID, store.PromiseReviewParams{ReviewAt: reviewAt, Note: p.Note}, p.IfMatch)
	if err != nil {
		return nil, mapPromiseStoreError(err, p.Promise)
	}
	return a.promiseDTO(ctx, updated)
}

func (a *API) PromiseResolve(ctx context.Context, p PromiseReviewParams) (*WrkqPromise, error) {
	return a.promiseClose(ctx, p, "resolve")
}

func (a *API) PromiseAbandon(ctx context.Context, p PromiseReviewParams) (*WrkqPromise, error) {
	return a.promiseClose(ctx, p, "abandon")
}

func (a *API) promiseClose(ctx context.Context, p PromiseReviewParams, verb string) (*WrkqPromise, error) {
	promise, attr, err := a.ownedPromise(ctx, p.Promise, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ReviewAt) != "" || strings.TrimSpace(p.ReviewIn) != "" {
		return nil, NewValidationError(verb+" does not accept reviewAt or reviewIn", nil)
	}
	var updated *domain.Promise
	if verb == "resolve" {
		updated, err = a.store.Promises.ResolveWithAttribution(attr, promise.UUID, p.Note, p.IfMatch)
	} else {
		updated, err = a.store.Promises.AbandonWithAttribution(attr, promise.UUID, p.Note, p.IfMatch)
	}
	if err != nil {
		return nil, mapPromiseStoreError(err, p.Promise)
	}
	return a.promiseDTO(ctx, updated)
}

func (a *API) PromiseAttach(ctx context.Context, p PromiseRetargetParams) (*WrkqPromise, error) {
	promise, attr, err := a.ownedPromise(ctx, p.Promise, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	taskUUID, containerUUID, _, err := a.resolvePromiseTarget(p.Task, p.Container)
	if err != nil {
		return nil, err
	}
	if taskUUID == nil && containerUUID == nil {
		return nil, NewValidationError("promise attach requires exactly one task or container", nil)
	}
	var updated *domain.Promise
	if taskUUID != nil {
		updated, err = a.store.Promises.AttachTaskWithAttribution(attr, promise.UUID, *taskUUID, p.IfMatch)
	} else {
		updated, err = a.store.Promises.AttachContainerWithAttribution(attr, promise.UUID, *containerUUID, p.IfMatch)
	}
	if err != nil {
		return nil, mapPromiseStoreError(err, p.Promise)
	}
	return a.promiseDTO(ctx, updated)
}

func (a *API) PromiseDetach(ctx context.Context, p PromiseRetargetParams) (*WrkqPromise, error) {
	promise, attr, err := a.ownedPromise(ctx, p.Promise, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Task) != "" || strings.TrimSpace(p.Container) != "" {
		return nil, NewValidationError("promise detach does not accept a target", nil)
	}
	updated, err := a.store.Promises.DetachWithAttribution(attr, promise.UUID, p.IfMatch)
	if err != nil {
		return nil, mapPromiseStoreError(err, p.Promise)
	}
	return a.promiseDTO(ctx, updated)
}

func (a *API) PromiseDelete(ctx context.Context, p PromiseDeleteParams) (*WrkqPromise, error) {
	promise, attr, err := a.ownedPromise(ctx, p.Promise, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(p.Mode)
	switch mode {
	case "", "soft", "abandon":
		updated, err := a.store.Promises.AbandonWithAttribution(attr, promise.UUID, nil, p.IfMatch)
		if err != nil {
			return nil, mapPromiseStoreError(err, p.Promise)
		}
		return a.promiseDTO(ctx, updated)
	case "purge":
		pre, err := a.promiseDTO(ctx, promise)
		if err != nil {
			return nil, err
		}
		if err := a.store.Promises.PurgeWithAttribution(attr, promise.UUID, p.IfMatch); err != nil {
			return nil, mapPromiseStoreError(err, p.Promise)
		}
		return pre, nil
	default:
		return nil, NewValidationError("invalid promise delete mode: "+mode, map[string]any{"field": "mode", "expected": "soft | abandon | purge"})
	}
}

func (a *API) ownedPromise(ctx context.Context, selector, principalRef string) (*domain.Promise, attribution.Attribution, error) {
	if err := ctx.Err(); err != nil {
		return nil, attribution.Attribution{}, err
	}
	attr, err := a.attributionFor(principalRef)
	if err != nil {
		return nil, attribution.Attribution{}, err
	}
	promise, err := a.getPromise(selector)
	if err != nil {
		return nil, attribution.Attribution{}, err
	}
	if promise.OwnerPrincipalRef != attr.PrincipalRef {
		return nil, attribution.Attribution{}, NewForbiddenError("only the promise owner may mutate it", map[string]any{
			"promise": promise.ID, "ownerPrincipalRef": promise.OwnerPrincipalRef,
			"callerPrincipalRef": attr.PrincipalRef,
		})
	}
	return promise, attr, nil
}

func (a *API) getPromise(selector string) (*domain.Promise, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, NewValidationError("promise selector is required", map[string]any{"field": "promise"})
	}
	promise, err := a.store.Promises.Get(selector)
	if err != nil {
		return nil, mapPromiseStoreError(err, selector)
	}
	return promise, nil
}

func (a *API) promiseReadOwner(rawOwner, principalRef string) (string, error) {
	if strings.TrimSpace(rawOwner) != "" {
		return normalizePromiseOwner(rawOwner)
	}
	attr, err := a.attributionFor(principalRef)
	if err != nil {
		return "", err
	}
	return attr.PrincipalRef, nil
}

func normalizePromiseOwner(raw string) (string, error) {
	owner, err := attribution.NormalizeCompat(raw)
	if err != nil {
		return "", NewValidationError("invalid ownerPrincipalRef: "+err.Error(), map[string]any{"field": "ownerPrincipalRef"})
	}
	return owner, nil
}

// normalizePromiseReviewTime is the sole API authority for absolute and
// relative promise review input. Relative values use the server process clock;
// clients never submit the base instant.
func normalizePromiseReviewTime(reviewAt, reviewIn string, required bool) (string, bool, error) {
	reviewAt = strings.TrimSpace(reviewAt)
	reviewIn = strings.TrimSpace(reviewIn)
	if reviewAt != "" && reviewIn != "" {
		return "", false, NewValidationError("reviewAt and reviewIn are mutually exclusive", map[string]any{"fields": []string{"reviewAt", "reviewIn"}})
	}
	if reviewAt == "" && reviewIn == "" {
		if required {
			return "", false, NewValidationError("exactly one of reviewAt or reviewIn is required", map[string]any{"fields": []string{"reviewAt", "reviewIn"}})
		}
		return "", false, nil
	}
	if reviewAt != "" {
		canonical, err := domain.NormalizePromiseReviewAt(reviewAt)
		if err != nil {
			return "", false, NewValidationError(err.Error(), map[string]any{"field": "reviewAt"})
		}
		return canonical, true, nil
	}
	duration, err := parsePromiseDuration(reviewIn)
	if err != nil {
		return "", false, NewValidationError("invalid reviewIn: "+err.Error(), map[string]any{"field": "reviewIn"})
	}
	return time.Now().UTC().Add(duration).Truncate(time.Second).Format("2006-01-02T15:04:05Z"), true, nil
}

func parsePromiseDuration(raw string) (time.Duration, error) {
	expanded := promiseDayDuration.ReplaceAllStringFunc(raw, func(term string) string {
		days, _ := strconv.ParseFloat(strings.TrimSuffix(term, "d"), 64)
		return strconv.FormatFloat(days*24, 'f', -1, 64) + "h"
	})
	duration, err := time.ParseDuration(expanded)
	if err != nil {
		return 0, fmt.Errorf("expected a positive duration such as 7d or 36h")
	}
	if duration <= 0 {
		return 0, fmt.Errorf("duration must be greater than zero")
	}
	return duration, nil
}

func (a *API) resolvePromiseTarget(task, container string) (*string, *string, string, error) {
	task = strings.TrimSpace(task)
	container = strings.TrimSpace(container)
	if task != "" && container != "" {
		return nil, nil, "", NewValidationError("promise may reference at most one task or container", map[string]any{"fields": []string{"task", "container"}})
	}
	if task != "" {
		uuid, _, err := selectors.ResolveTask(a.db, task)
		if err != nil {
			return nil, nil, "", NewNotFoundError(task, "task")
		}
		var title string
		if err := a.db.QueryRow("SELECT title FROM tasks WHERE uuid = ?", uuid).Scan(&title); err != nil {
			return nil, nil, "", NewInternalError(err)
		}
		return &uuid, nil, title, nil
	}
	if container != "" {
		uuid, _, err := selectors.ResolveContainer(a.db, container)
		if err != nil {
			return nil, nil, "", NewNotFoundError(container, "container")
		}
		var title string
		if err := a.db.QueryRow("SELECT title FROM containers WHERE uuid = ?", uuid).Scan(&title); err != nil {
			return nil, nil, "", NewInternalError(err)
		}
		return nil, &uuid, title, nil
	}
	return nil, nil, "", nil
}

func (a *API) promiseListDTO(ctx context.Context, rows []domain.Promise) (*WrkqPromiseListResult, error) {
	result := &WrkqPromiseListResult{Items: make([]WrkqPromise, 0, len(rows))}
	for index := range rows {
		dto, err := a.promiseDTO(ctx, &rows[index])
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, *dto)
	}
	return result, nil
}

func (a *API) promiseDTO(ctx context.Context, promise *domain.Promise) (*WrkqPromise, error) {
	dto := &WrkqPromise{
		UUID: promise.UUID, ID: promise.ID, OwnerPrincipalRef: promise.OwnerPrincipalRef,
		Subject: promise.Subject, ReviewQuestion: promise.ReviewQuestion,
		ReviewAt: promise.ReviewAt, State: string(promise.State), ClosedAt: promise.ClosedAt,
		LastReviewedAt: promise.LastReviewedAt, LastReviewNote: promise.LastReviewNote,
		Meta: map[string]any{}, ETag: promise.ETag, CreatedAt: toRFC3339(promise.CreatedAt),
		UpdatedAt: toRFC3339(promise.UpdatedAt), CreatedByPrincipalRef: promise.CreatedByPrincipalRef,
		UpdatedByPrincipalRef: promise.UpdatedByPrincipalRef,
	}
	if promise.Meta != nil {
		dto.Meta = parseMeta(*promise.Meta)
	}
	if promise.SubjectTaskUUID != nil {
		ref := &WrkqPromiseSubjectRef{Type: "task", UUID: *promise.SubjectTaskUUID}
		err := a.db.QueryRowContext(ctx, `
			SELECT t.id, COALESCE(cp.path || '/' || t.slug, t.slug)
			  FROM tasks t LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid
			 WHERE t.uuid = ?`, *promise.SubjectTaskUUID).Scan(&ref.ID, &ref.Path)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, NewNotFoundError(*promise.SubjectTaskUUID, "promise subject task")
			}
			return nil, NewInternalError(err)
		}
		dto.SubjectRef = ref
	}
	if promise.SubjectContainerUUID != nil {
		ref := &WrkqPromiseSubjectRef{Type: "container", UUID: *promise.SubjectContainerUUID}
		err := a.db.QueryRowContext(ctx, `
			SELECT c.id, COALESCE(v.path, c.slug)
			  FROM containers c LEFT JOIN v_container_paths v ON v.uuid = c.uuid
			 WHERE c.uuid = ?`, *promise.SubjectContainerUUID).Scan(&ref.ID, &ref.Path)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, NewNotFoundError(*promise.SubjectContainerUUID, "promise subject container")
			}
			return nil, NewInternalError(err)
		}
		dto.SubjectRef = ref
	}
	return dto, nil
}

func mapPromiseStoreError(err error, selector string) error {
	var notFound *store.PromiseNotFoundError
	if errors.As(err, &notFound) {
		if selector == "" {
			selector = notFound.Selector
		}
		return NewNotFoundError(selector, "promise")
	}
	var wrongState *store.PromiseWrongStateError
	if errors.As(err, &wrongState) {
		return NewWrongStateError(map[string]any{"state": wrongState.State, "verb": wrongState.Verb})
	}
	var mismatch *domain.ETagMismatchError
	if errors.As(err, &mismatch) {
		return NewConflictError("promise etag precondition failed", map[string]any{"expectedEtag": mismatch.Expected, "currentEtag": mismatch.Actual})
	}
	return NewInternalError(err)
}
