// Package aigeneration implements an AI content generation pipeline.
//
// This workflow demonstrates:
// - Multi-stage AI generation with quality gates
// - Parallel execution (multiple reviewers)
// - Human-in-the-loop approval via signals
// - Branching based on content quality
// - Retries for AI service reliability
package aigeneration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lirancohen/skene/retry"
	"github.com/lirancohen/skene/workflow"
)

// =============================================================================
// Domain Types
// =============================================================================

// GenerationInput is the workflow input.
type GenerationInput struct {
	RequestID    string            `json:"request_id"`
	ContentType  string            `json:"content_type"` // "blog_post", "social_media", "email"
	Topic        string            `json:"topic"`
	Keywords     []string          `json:"keywords"`
	TargetLength int               `json:"target_length"` // word count
	Tone         string            `json:"tone"`          // "professional", "casual", "formal"
	Brand        string            `json:"brand"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// Step outputs

type OutlineOutput struct {
	Sections []Section `json:"sections"`
	Approach string    `json:"approach"`
}

type Section struct {
	Title       string   `json:"title"`
	KeyPoints   []string `json:"key_points"`
	TargetWords int      `json:"target_words"`
}

type DraftOutput struct {
	Content     string    `json:"content"`
	WordCount   int       `json:"word_count"`
	GeneratedAt time.Time `json:"generated_at"`
	Model       string    `json:"model"`
}

type QualityReviewOutput struct {
	Score         float64  `json:"score"`          // 0-1
	PassesQuality bool     `json:"passes_quality"` // score >= threshold
	Issues        []string `json:"issues,omitempty"`
	Suggestions   []string `json:"suggestions,omitempty"`
}

type FactCheckOutput struct {
	Verified       bool     `json:"verified"`
	ClaimsChecked  int      `json:"claims_checked"`
	FlaggedClaims  []string `json:"flagged_claims,omitempty"`
	ConfidenceScore float64 `json:"confidence_score"`
}

type BrandCheckOutput struct {
	OnBrand        bool     `json:"on_brand"`
	ToneMatch      float64  `json:"tone_match"`      // 0-1
	StyleMatch     float64  `json:"style_match"`     // 0-1
	Violations     []string `json:"violations,omitempty"`
}

type RefinementOutput struct {
	Content       string   `json:"content"`
	WordCount     int      `json:"word_count"`
	ChangesApplied []string `json:"changes_applied"`
	Iteration     int      `json:"iteration"`
}

type ApprovalOutput struct {
	Approved   bool      `json:"approved"`
	ApprovedBy string    `json:"approved_by,omitempty"`
	ApprovedAt time.Time `json:"approved_at,omitempty"`
	Comments   string    `json:"comments,omitempty"`
}

type PublishOutput struct {
	Published   bool      `json:"published"`
	URL         string    `json:"url,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
	ContentID   string    `json:"content_id"`
}

type FinalOutput struct {
	RequestID   string    `json:"request_id"`
	Status      string    `json:"status"` // "published", "rejected", "failed"
	ContentID   string    `json:"content_id,omitempty"`
	URL         string    `json:"url,omitempty"`
	FinalScore  float64   `json:"final_score"`
	CompletedAt time.Time `json:"completed_at"`
}

// =============================================================================
// AI Service Interface
// =============================================================================

type AIService interface {
	GenerateOutline(ctx context.Context, input GenerationInput) (*OutlineOutput, error)
	GenerateDraft(ctx context.Context, input GenerationInput, outline OutlineOutput) (*DraftOutput, error)
	ReviewQuality(ctx context.Context, content string) (*QualityReviewOutput, error)
	FactCheck(ctx context.Context, content string) (*FactCheckOutput, error)
	CheckBrand(ctx context.Context, content, brand, tone string) (*BrandCheckOutput, error)
	Refine(ctx context.Context, content string, suggestions []string, iteration int) (*RefinementOutput, error)
}

type PublishService interface {
	Publish(ctx context.Context, contentType, content string, metadata map[string]string) (*PublishOutput, error)
}

type aiServiceKey struct{}
type publishServiceKey struct{}

func WithAIService(ctx context.Context, svc AIService) context.Context {
	return context.WithValue(ctx, aiServiceKey{}, svc)
}

func GetAIService(ctx context.Context) AIService {
	svc, _ := ctx.Value(aiServiceKey{}).(AIService)
	return svc
}

func WithPublishService(ctx context.Context, svc PublishService) context.Context {
	return context.WithValue(ctx, publishServiceKey{}, svc)
}

func GetPublishService(ctx context.Context) PublishService {
	svc, _ := ctx.Value(publishServiceKey{}).(PublishService)
	return svc
}

// =============================================================================
// Step Definitions
// =============================================================================

// CreateOutline generates a content outline.
var CreateOutline = workflow.NewStep("create-outline", func(ctx workflow.Context) (OutlineOutput, error) {
	input := workflow.Input[GenerationInput](ctx)

	ai := GetAIService(ctx)
	if ai == nil {
		// Mock response for testing
		return OutlineOutput{
			Sections: []Section{
				{Title: "Introduction", KeyPoints: []string{"Hook", "Context"}, TargetWords: 100},
				{Title: "Main Content", KeyPoints: input.Keywords, TargetWords: input.TargetLength - 200},
				{Title: "Conclusion", KeyPoints: []string{"Summary", "CTA"}, TargetWords: 100},
			},
			Approach: "informative with storytelling elements",
		}, nil
	}

	outline, err := ai.GenerateOutline(ctx, input)
	if err != nil {
		return OutlineOutput{}, fmt.Errorf("outline generation failed: %w", err)
	}

	return *outline, nil
}).WithRetry(retry.Default()).WithTimeout(60 * time.Second)

// GenerateDraft creates the initial content draft.
var GenerateDraft = workflow.NewStep("generate-draft", func(ctx workflow.Context) (DraftOutput, error) {
	input := workflow.Input[GenerationInput](ctx)
	outline := CreateOutline.MustOutput(ctx)

	ai := GetAIService(ctx)
	if ai == nil {
		// Mock response
		mockContent := fmt.Sprintf("# %s\n\nThis is generated content about %s.\n\n",
			input.Topic, input.Topic)
		for _, kw := range input.Keywords {
			mockContent += fmt.Sprintf("Key point about %s.\n\n", kw)
		}
		return DraftOutput{
			Content:     mockContent,
			WordCount:   input.TargetLength,
			GeneratedAt: time.Now(),
			Model:       "mock-model",
		}, nil
	}

	draft, err := ai.GenerateDraft(ctx, input, outline)
	if err != nil {
		return DraftOutput{}, fmt.Errorf("draft generation failed: %w", err)
	}

	return *draft, nil
}).WithRetry(&retry.Policy{
	MaxAttempts:  3,
	InitialDelay: 2 * time.Second,
	MaxDelay:     30 * time.Second,
	Multiplier:   2.0,
}).WithTimeout(120 * time.Second)

// ReviewQuality performs AI-based quality review.
var ReviewQuality = workflow.NewStep("review-quality", func(ctx workflow.Context) (QualityReviewOutput, error) {
	draft := GenerateDraft.MustOutput(ctx)

	ai := GetAIService(ctx)
	if ai == nil {
		return QualityReviewOutput{
			Score:         0.85,
			PassesQuality: true,
			Suggestions:   []string{"Consider adding more examples"},
		}, nil
	}

	review, err := ai.ReviewQuality(ctx, draft.Content)
	if err != nil {
		return QualityReviewOutput{}, fmt.Errorf("quality review failed: %w", err)
	}

	return *review, nil
}).WithTimeout(60 * time.Second)

// FactCheck verifies factual claims in the content.
var FactCheck = workflow.NewStep("fact-check", func(ctx workflow.Context) (FactCheckOutput, error) {
	draft := GenerateDraft.MustOutput(ctx)

	ai := GetAIService(ctx)
	if ai == nil {
		return FactCheckOutput{
			Verified:        true,
			ClaimsChecked:   5,
			ConfidenceScore: 0.92,
		}, nil
	}

	result, err := ai.FactCheck(ctx, draft.Content)
	if err != nil {
		return FactCheckOutput{}, fmt.Errorf("fact check failed: %w", err)
	}

	return *result, nil
}).WithTimeout(90 * time.Second)

// CheckBrand verifies brand consistency.
var CheckBrand = workflow.NewStep("check-brand", func(ctx workflow.Context) (BrandCheckOutput, error) {
	input := workflow.Input[GenerationInput](ctx)
	draft := GenerateDraft.MustOutput(ctx)

	ai := GetAIService(ctx)
	if ai == nil {
		return BrandCheckOutput{
			OnBrand:    true,
			ToneMatch:  0.88,
			StyleMatch: 0.91,
		}, nil
	}

	result, err := ai.CheckBrand(ctx, draft.Content, input.Brand, input.Tone)
	if err != nil {
		return BrandCheckOutput{}, fmt.Errorf("brand check failed: %w", err)
	}

	return *result, nil
}).WithTimeout(60 * time.Second)

// RefineContent improves the draft based on review feedback.
var RefineContent = workflow.NewStep("refine-content", func(ctx workflow.Context) (RefinementOutput, error) {
	draft := GenerateDraft.MustOutput(ctx)
	quality := ReviewQuality.MustOutput(ctx)
	facts := FactCheck.MustOutput(ctx)
	brand := CheckBrand.MustOutput(ctx)

	// Collect all suggestions
	var suggestions []string
	suggestions = append(suggestions, quality.Suggestions...)
	suggestions = append(suggestions, quality.Issues...)
	suggestions = append(suggestions, facts.FlaggedClaims...)
	suggestions = append(suggestions, brand.Violations...)

	if len(suggestions) == 0 {
		// No changes needed
		return RefinementOutput{
			Content:        draft.Content,
			WordCount:      draft.WordCount,
			ChangesApplied: []string{},
			Iteration:      0,
		}, nil
	}

	ai := GetAIService(ctx)
	if ai == nil {
		return RefinementOutput{
			Content:        draft.Content + "\n\n[Refined based on feedback]",
			WordCount:      draft.WordCount + 10,
			ChangesApplied: suggestions,
			Iteration:      1,
		}, nil
	}

	result, err := ai.Refine(ctx, draft.Content, suggestions, 1)
	if err != nil {
		return RefinementOutput{}, fmt.Errorf("refinement failed: %w", err)
	}

	return *result, nil
}).WithTimeout(120 * time.Second)

// RequestApproval waits for human approval via signal.
var RequestApproval = workflow.NewStep("request-approval", func(ctx workflow.Context) (ApprovalOutput, error) {
	input := workflow.Input[GenerationInput](ctx)
	quality := ReviewQuality.MustOutput(ctx)

	// If quality score is very high, auto-approve
	if quality.Score >= 0.95 {
		return ApprovalOutput{
			Approved:   true,
			ApprovedBy: "auto-approval",
			ApprovedAt: time.Now(),
			Comments:   fmt.Sprintf("Auto-approved due to high quality score: %.2f", quality.Score),
		}, nil
	}

	// Wait for human approval signal
	type ApprovalSignal struct {
		Approved bool   `json:"approved"`
		Approver string `json:"approver"`
		Comments string `json:"comments"`
	}

	signal, err := workflow.WaitForSignalTyped[ApprovalSignal](ctx, "content-approval", 72*time.Hour)
	if err != nil {
		return ApprovalOutput{}, fmt.Errorf("approval timeout for request %s: %w", input.RequestID, err)
	}

	return ApprovalOutput{
		Approved:   signal.Approved,
		ApprovedBy: signal.Approver,
		ApprovedAt: time.Now(),
		Comments:   signal.Comments,
	}, nil
})

// PublishContent publishes the approved content.
var PublishContent = workflow.NewStep("publish-content", func(ctx workflow.Context) (PublishOutput, error) {
	input := workflow.Input[GenerationInput](ctx)
	refined := RefineContent.MustOutput(ctx)
	approval := RequestApproval.MustOutput(ctx)

	if !approval.Approved {
		return PublishOutput{Published: false}, errors.New("content not approved for publication")
	}

	pub := GetPublishService(ctx)
	if pub == nil {
		return PublishOutput{
			Published:   true,
			URL:         fmt.Sprintf("https://example.com/content/%s", input.RequestID),
			PublishedAt: time.Now(),
			ContentID:   fmt.Sprintf("content-%s", input.RequestID),
		}, nil
	}

	result, err := pub.Publish(ctx, input.ContentType, refined.Content, input.Metadata)
	if err != nil {
		return PublishOutput{}, fmt.Errorf("publish failed: %w", err)
	}

	return *result, nil
}).WithRetry(retry.Default())

// Complete finalizes the workflow.
var Complete = workflow.NewStep("complete", func(ctx workflow.Context) (FinalOutput, error) {
	input := workflow.Input[GenerationInput](ctx)
	quality := ReviewQuality.MustOutput(ctx)
	approval := RequestApproval.MustOutput(ctx)

	status := "published"
	var url, contentID string

	if !approval.Approved {
		status = "rejected"
	} else {
		published := PublishContent.MustOutput(ctx)
		if !published.Published {
			status = "failed"
		} else {
			url = published.URL
			contentID = published.ContentID
		}
	}

	return FinalOutput{
		RequestID:   input.RequestID,
		Status:      status,
		ContentID:   contentID,
		URL:         url,
		FinalScore:  quality.Score,
		CompletedAt: time.Now(),
	}, nil
})

// =============================================================================
// Workflow Definition
// =============================================================================

// AIGenerationWorkflow orchestrates the complete content generation pipeline.
//
// DAG structure:
//
//	CreateOutline
//	     │
//	     ▼
//	GenerateDraft
//	     │
//	     ├──────────────┬──────────────┐
//	     ▼              ▼              ▼
//	ReviewQuality   FactCheck     CheckBrand    (parallel)
//	     │              │              │
//	     └──────────────┴──────────────┘
//	                    │
//	                    ▼
//	             RefineContent
//	                    │
//	                    ▼
//	            RequestApproval (waits for signal)
//	                    │
//	                    ▼
//	             PublishContent
//	                    │
//	                    ▼
//	               Complete
//
var AIGenerationWorkflow = workflow.Define("ai-content-generation",
	CreateOutline.After(),
	GenerateDraft.After(CreateOutline),
	// These three run in parallel after draft is ready
	ReviewQuality.After(GenerateDraft),
	FactCheck.After(GenerateDraft),
	CheckBrand.After(GenerateDraft),
	// Refinement waits for all reviews
	RefineContent.After(ReviewQuality, FactCheck, CheckBrand),
	// Human approval
	RequestApproval.After(RefineContent),
	// Publish only if approved
	PublishContent.After(RequestApproval),
	// Final output
	Complete.After(PublishContent),
)
