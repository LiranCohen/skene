// Package approval implements a multi-level document approval workflow.
//
// This workflow demonstrates:
// - Signal-based human approvals
// - Timeouts for approval deadlines
// - Escalation logic
// - Branching based on document type
// - Audit trail via events
package approval

import (
	"errors"
	"fmt"
	"time"

	"github.com/lirancohen/skene/workflow"
)

// =============================================================================
// Domain Types
// =============================================================================

// DocumentInput is the workflow input.
type DocumentInput struct {
	DocumentID   string            `json:"document_id"`
	Title        string            `json:"title"`
	Type         string            `json:"type"` // "contract", "invoice", "expense", "policy"
	Amount       float64           `json:"amount,omitempty"`
	Submitter    string            `json:"submitter"`
	SubmitterID  string            `json:"submitter_id"`
	Department   string            `json:"department"`
	Priority     string            `json:"priority"` // "low", "normal", "high", "urgent"
	DueDate      time.Time         `json:"due_date,omitempty"`
	Attachments  []string          `json:"attachments,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// Approval represents a single approval decision.
type Approval struct {
	ApproverID   string    `json:"approver_id"`
	ApproverName string    `json:"approver_name"`
	Decision     string    `json:"decision"` // "approved", "rejected", "escalated"
	Comments     string    `json:"comments,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// ApprovalSignal is the signal payload for approvals.
type ApprovalSignal struct {
	ApproverID   string `json:"approver_id"`
	ApproverName string `json:"approver_name"`
	Decision     string `json:"decision"` // "approve", "reject", "escalate"
	Comments     string `json:"comments,omitempty"`
}

// Step outputs

type ValidationOutput struct {
	Valid          bool     `json:"valid"`
	Errors         []string `json:"errors,omitempty"`
	RequiredLevel  int      `json:"required_level"` // 1, 2, or 3
	Approvers      []string `json:"approvers"`      // IDs of required approvers
}

type Level1Output struct {
	Approval   Approval `json:"approval"`
	NextAction string   `json:"next_action"` // "level2", "complete", "rejected"
}

type Level2Output struct {
	Approval   Approval `json:"approval"`
	NextAction string   `json:"next_action"` // "level3", "complete", "rejected"
}

type Level3Output struct {
	Approval   Approval `json:"approval"`
	NextAction string   `json:"next_action"` // "complete", "rejected"
}

type FinalOutput struct {
	DocumentID   string     `json:"document_id"`
	Status       string     `json:"status"` // "approved", "rejected", "expired"
	Approvals    []Approval `json:"approvals"`
	CompletedAt  time.Time  `json:"completed_at"`
	TotalTime    string     `json:"total_time"`
}

// =============================================================================
// Approval Thresholds
// =============================================================================

// getRequiredLevel determines approval level based on document type and amount.
func getRequiredLevel(docType string, amount float64) int {
	switch docType {
	case "expense":
		if amount < 100 {
			return 1
		} else if amount < 1000 {
			return 2
		}
		return 3
	case "invoice":
		if amount < 5000 {
			return 1
		} else if amount < 25000 {
			return 2
		}
		return 3
	case "contract":
		return 3 // Contracts always need executive approval
	case "policy":
		return 2 // Policies need manager approval
	default:
		return 1
	}
}

// getApprovalTimeout returns timeout based on priority.
func getApprovalTimeout(priority string) time.Duration {
	switch priority {
	case "urgent":
		return 4 * time.Hour
	case "high":
		return 24 * time.Hour
	case "normal":
		return 72 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

// =============================================================================
// Step Definitions
// =============================================================================

// ValidateDocument validates the document and determines approval requirements.
var ValidateDocument = workflow.NewStep("validate-document", func(ctx workflow.Context) (ValidationOutput, error) {
	input := workflow.Input[DocumentInput](ctx)

	var errs []string

	// Basic validation
	if input.DocumentID == "" {
		errs = append(errs, "document_id is required")
	}
	if input.Title == "" {
		errs = append(errs, "title is required")
	}
	if input.Submitter == "" {
		errs = append(errs, "submitter is required")
	}

	// Amount validation for financial documents
	if input.Type == "expense" || input.Type == "invoice" {
		if input.Amount <= 0 {
			errs = append(errs, "amount must be positive for financial documents")
		}
	}

	if len(errs) > 0 {
		return ValidationOutput{
			Valid:  false,
			Errors: errs,
		}, nil
	}

	level := getRequiredLevel(input.Type, input.Amount)

	// Determine approvers based on level
	var approvers []string
	switch level {
	case 1:
		approvers = []string{"manager"}
	case 2:
		approvers = []string{"manager", "director"}
	case 3:
		approvers = []string{"manager", "director", "executive"}
	}

	return ValidationOutput{
		Valid:         true,
		RequiredLevel: level,
		Approvers:     approvers,
	}, nil
})

// Level1Approval handles first-level (manager) approval.
var Level1Approval = workflow.NewStep("level1-approval", func(ctx workflow.Context) (Level1Output, error) {
	input := workflow.Input[DocumentInput](ctx)
	validation := ValidateDocument.MustOutput(ctx)

	if !validation.Valid {
		return Level1Output{}, errors.New("cannot process invalid document")
	}

	timeout := getApprovalTimeout(input.Priority)
	signalName := fmt.Sprintf("approval-level1-%s", input.DocumentID)

	// Wait for approval signal
	signal, err := workflow.WaitForSignalTyped[ApprovalSignal](ctx, signalName, timeout)
	if err != nil {
		// Timeout - auto-escalate for high priority, reject for others
		if input.Priority == "urgent" || input.Priority == "high" {
			return Level1Output{
				Approval: Approval{
					ApproverID:   "system",
					ApproverName: "Auto-Escalation",
					Decision:     "escalated",
					Comments:     "Approval timeout - auto-escalated",
					Timestamp:    time.Now(),
				},
				NextAction: "level2",
			}, nil
		}
		return Level1Output{
			Approval: Approval{
				ApproverID:   "system",
				ApproverName: "System",
				Decision:     "rejected",
				Comments:     "Approval timeout - document expired",
				Timestamp:    time.Now(),
			},
			NextAction: "rejected",
		}, nil
	}

	approval := Approval{
		ApproverID:   signal.ApproverID,
		ApproverName: signal.ApproverName,
		Decision:     signal.Decision,
		Comments:     signal.Comments,
		Timestamp:    time.Now(),
	}

	// Determine next action
	nextAction := "complete"
	switch signal.Decision {
	case "reject":
		nextAction = "rejected"
		approval.Decision = "rejected"
	case "escalate":
		nextAction = "level2"
		approval.Decision = "escalated"
	case "approve":
		approval.Decision = "approved"
		if validation.RequiredLevel > 1 {
			nextAction = "level2"
		}
	}

	return Level1Output{
		Approval:   approval,
		NextAction: nextAction,
	}, nil
})

// Level2Approval handles second-level (director) approval.
var Level2Approval = workflow.NewStep("level2-approval", func(ctx workflow.Context) (Level2Output, error) {
	input := workflow.Input[DocumentInput](ctx)
	validation := ValidateDocument.MustOutput(ctx)
	level1 := Level1Approval.MustOutput(ctx)

	// Skip if not needed
	if level1.NextAction != "level2" {
		return Level2Output(level1), nil
	}

	timeout := getApprovalTimeout(input.Priority)
	signalName := fmt.Sprintf("approval-level2-%s", input.DocumentID)

	signal, err := workflow.WaitForSignalTyped[ApprovalSignal](ctx, signalName, timeout)
	if err != nil {
		// Timeout handling
		if input.Priority == "urgent" {
			return Level2Output{
				Approval: Approval{
					ApproverID:   "system",
					ApproverName: "Auto-Escalation",
					Decision:     "escalated",
					Comments:     "Level 2 timeout - auto-escalated to executive",
					Timestamp:    time.Now(),
				},
				NextAction: "level3",
			}, nil
		}
		return Level2Output{
			Approval: Approval{
				ApproverID:   "system",
				ApproverName: "System",
				Decision:     "rejected",
				Comments:     "Level 2 approval timeout",
				Timestamp:    time.Now(),
			},
			NextAction: "rejected",
		}, nil
	}

	approval := Approval{
		ApproverID:   signal.ApproverID,
		ApproverName: signal.ApproverName,
		Decision:     signal.Decision,
		Comments:     signal.Comments,
		Timestamp:    time.Now(),
	}

	nextAction := "complete"
	switch signal.Decision {
	case "reject":
		nextAction = "rejected"
		approval.Decision = "rejected"
	case "escalate":
		nextAction = "level3"
		approval.Decision = "escalated"
	case "approve":
		approval.Decision = "approved"
		if validation.RequiredLevel > 2 {
			nextAction = "level3"
		}
	}

	return Level2Output{
		Approval:   approval,
		NextAction: nextAction,
	}, nil
})

// Level3Approval handles third-level (executive) approval.
var Level3Approval = workflow.NewStep("level3-approval", func(ctx workflow.Context) (Level3Output, error) {
	input := workflow.Input[DocumentInput](ctx)
	level2 := Level2Approval.MustOutput(ctx)

	// Skip if not needed
	if level2.NextAction != "level3" {
		return Level3Output(level2), nil
	}

	timeout := getApprovalTimeout(input.Priority)
	signalName := fmt.Sprintf("approval-level3-%s", input.DocumentID)

	signal, err := workflow.WaitForSignalTyped[ApprovalSignal](ctx, signalName, timeout)
	if err != nil {
		return Level3Output{
			Approval: Approval{
				ApproverID:   "system",
				ApproverName: "System",
				Decision:     "rejected",
				Comments:     "Executive approval timeout",
				Timestamp:    time.Now(),
			},
			NextAction: "rejected",
		}, nil
	}

	approval := Approval{
		ApproverID:   signal.ApproverID,
		ApproverName: signal.ApproverName,
		Decision:     signal.Decision,
		Comments:     signal.Comments,
		Timestamp:    time.Now(),
	}

	nextAction := "complete"
	if signal.Decision == "reject" {
		nextAction = "rejected"
		approval.Decision = "rejected"
	} else {
		approval.Decision = "approved"
	}

	return Level3Output{
		Approval:   approval,
		NextAction: nextAction,
	}, nil
})

// FinalizeApproval creates the final approval record.
var FinalizeApproval = workflow.NewStep("finalize-approval", func(ctx workflow.Context) (FinalOutput, error) {
	input := workflow.Input[DocumentInput](ctx)
	validation := ValidateDocument.MustOutput(ctx)
	level1 := Level1Approval.MustOutput(ctx)
	level2 := Level2Approval.MustOutput(ctx)
	level3 := Level3Approval.MustOutput(ctx)

	// Collect all approvals
	var approvals []Approval
	approvals = append(approvals, level1.Approval)

	if level1.NextAction == "level2" || level2.Approval.ApproverID != level1.Approval.ApproverID {
		approvals = append(approvals, level2.Approval)
	}

	if level2.NextAction == "level3" || (level3.Approval.ApproverID != "" && level3.Approval.ApproverID != level2.Approval.ApproverID) {
		approvals = append(approvals, level3.Approval)
	}

	// Determine final status
	status := "approved"
	finalAction := level3.NextAction
	if finalAction == "" {
		finalAction = level2.NextAction
	}
	if finalAction == "" {
		finalAction = level1.NextAction
	}
	if finalAction == "rejected" {
		status = "rejected"
	}

	if !validation.Valid {
		status = "invalid"
	}

	return FinalOutput{
		DocumentID:  input.DocumentID,
		Status:      status,
		Approvals:   approvals,
		CompletedAt: time.Now(),
		TotalTime:   time.Since(time.Now()).String(), // Would need start time from history
	}, nil
})

// =============================================================================
// Workflow Definition
// =============================================================================

// ApprovalWorkflow orchestrates the document approval process.
//
// DAG structure:
//
//	ValidateDocument
//	       │
//	       ▼
//	Level1Approval (waits for signal)
//	       │
//	       ▼
//	Level2Approval (waits for signal, may skip)
//	       │
//	       ▼
//	Level3Approval (waits for signal, may skip)
//	       │
//	       ▼
//	FinalizeApproval
//
var ApprovalWorkflow = workflow.Define("document-approval",
	ValidateDocument.After(),
	Level1Approval.After(ValidateDocument),
	Level2Approval.After(Level1Approval),
	Level3Approval.After(Level2Approval),
	FinalizeApproval.After(Level3Approval),
)
