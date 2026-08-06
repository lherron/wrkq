//go:build wrkq_local

package workflow

import "github.com/lherron/wrkq/internal/db"

type TransitionDecisionInput struct {
	Instance           *Instance
	Template           *Template
	Transition         TransitionSpec
	Task               *taskDoc
	Evidence           []Evidence
	Obligations        []Obligation
	Checks             map[string]CheckRun
	Facts              map[string]interface{}
	Role               string
	PrincipalRef       string
	RoleQuery          queryer
	DependencyQuery    rowsQueryer
	CheckDatabase      *db.DB
	RequireRoleBinding bool
}

type TransitionDecision struct {
	Legal         bool
	Outcome       *OutcomeCase
	ExpectedState *State
	Blockers      []Blocker
	FollowUps     []TransitionFollowUp
}

type TransitionFollowUp struct {
	Kind    string
	Ref     string
	CheckID string
	Blocker Blocker
}