// Package datapipeline implements an ETL data processing workflow.
//
// This workflow demonstrates:
// - Multi-source data extraction
// - Parallel extraction from different sources
// - Data transformation and validation
// - Incremental loading with checkpoints
// - Error handling and partial success
package datapipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/lirancohen/skene/retry"
	"github.com/lirancohen/skene/workflow"
)

// =============================================================================
// Domain Types
// =============================================================================

// PipelineInput is the workflow input.
type PipelineInput struct {
	PipelineID    string            `json:"pipeline_id"`
	PipelineName  string            `json:"pipeline_name"`
	Sources       []DataSource      `json:"sources"`
	Destination   Destination       `json:"destination"`
	Options       PipelineOptions   `json:"options"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type DataSource struct {
	Name          string            `json:"name"`
	Type          string            `json:"type"` // "database", "api", "file", "stream"
	ConnectionStr string            `json:"connection_string"`
	Query         string            `json:"query,omitempty"`
	Filters       map[string]string `json:"filters,omitempty"`
}

type Destination struct {
	Type          string `json:"type"` // "database", "warehouse", "file"
	ConnectionStr string `json:"connection_string"`
	Table         string `json:"table"`
	Mode          string `json:"mode"` // "append", "replace", "upsert"
}

type PipelineOptions struct {
	BatchSize       int           `json:"batch_size"`
	ParallelWorkers int           `json:"parallel_workers"`
	ValidateSchema  bool          `json:"validate_schema"`
	DeduplicateBy   []string      `json:"deduplicate_by,omitempty"`
	Timeout         time.Duration `json:"timeout"`
	FailOnError     bool          `json:"fail_on_error"`
}

// Record represents a single data record.
type Record map[string]any

// Step outputs

type InitOutput struct {
	StartTime     time.Time `json:"start_time"`
	SourceCount   int       `json:"source_count"`
	Configuration string    `json:"configuration_hash"`
}

type ExtractionOutput struct {
	SourceName    string    `json:"source_name"`
	RecordCount   int       `json:"record_count"`
	ExtractedAt   time.Time `json:"extracted_at"`
	DurationMs    int64     `json:"duration_ms"`
	BytesRead     int64     `json:"bytes_read"`
	Checksum      string    `json:"checksum"`
	Error         string    `json:"error,omitempty"`
}

type MergeOutput struct {
	TotalRecords    int      `json:"total_records"`
	SourceBreakdown map[string]int `json:"source_breakdown"`
	MergedAt        time.Time `json:"merged_at"`
}

type TransformOutput struct {
	InputRecords     int       `json:"input_records"`
	OutputRecords    int       `json:"output_records"`
	DroppedRecords   int       `json:"dropped_records"`
	TransformedAt    time.Time `json:"transformed_at"`
	Transformations  []string  `json:"transformations_applied"`
}

type ValidationOutput struct {
	TotalRecords    int              `json:"total_records"`
	ValidRecords    int              `json:"valid_records"`
	InvalidRecords  int              `json:"invalid_records"`
	ErrorsByField   map[string]int   `json:"errors_by_field,omitempty"`
	SampleErrors    []ValidationError `json:"sample_errors,omitempty"`
}

type ValidationError struct {
	RecordIndex int    `json:"record_index"`
	Field       string `json:"field"`
	Value       any    `json:"value"`
	Error       string `json:"error"`
}

type DedupeOutput struct {
	InputRecords  int       `json:"input_records"`
	OutputRecords int       `json:"output_records"`
	Duplicates    int       `json:"duplicates_removed"`
	DedupedAt     time.Time `json:"deduped_at"`
}

type LoadOutput struct {
	RecordsLoaded   int       `json:"records_loaded"`
	RecordsFailed   int       `json:"records_failed"`
	LoadedAt        time.Time `json:"loaded_at"`
	DurationMs      int64     `json:"duration_ms"`
	BytesWritten    int64     `json:"bytes_written"`
}

type PipelineOutput struct {
	PipelineID      string                   `json:"pipeline_id"`
	Status          string                   `json:"status"` // "success", "partial", "failed"
	StartTime       time.Time                `json:"start_time"`
	EndTime         time.Time                `json:"end_time"`
	TotalDuration   string                   `json:"total_duration"`
	Extractions     []ExtractionOutput       `json:"extractions"`
	Transform       TransformOutput          `json:"transform"`
	Validation      ValidationOutput         `json:"validation"`
	Dedupe          DedupeOutput             `json:"dedupe"`
	Load            LoadOutput               `json:"load"`
	Summary         PipelineSummary          `json:"summary"`
}

type PipelineSummary struct {
	SourcesProcessed  int     `json:"sources_processed"`
	SourcesFailed     int     `json:"sources_failed"`
	RecordsExtracted  int     `json:"records_extracted"`
	RecordsTransformed int    `json:"records_transformed"`
	RecordsLoaded     int     `json:"records_loaded"`
	RecordsFailed     int     `json:"records_failed"`
	SuccessRate       float64 `json:"success_rate"`
}

// =============================================================================
// Service Interfaces
// =============================================================================

type ExtractorService interface {
	Extract(ctx context.Context, source DataSource) ([]Record, error)
}

type TransformerService interface {
	Transform(ctx context.Context, records []Record, rules []string) ([]Record, error)
}

type LoaderService interface {
	Load(ctx context.Context, dest Destination, records []Record) (*LoadOutput, error)
}

type extractorKey struct{}
type transformerKey struct{}
type loaderKey struct{}

func WithExtractor(ctx context.Context, svc ExtractorService) context.Context {
	return context.WithValue(ctx, extractorKey{}, svc)
}

func GetExtractor(ctx context.Context) ExtractorService {
	svc, _ := ctx.Value(extractorKey{}).(ExtractorService)
	return svc
}

func WithTransformer(ctx context.Context, svc TransformerService) context.Context {
	return context.WithValue(ctx, transformerKey{}, svc)
}

func GetTransformer(ctx context.Context) TransformerService {
	svc, _ := ctx.Value(transformerKey{}).(TransformerService)
	return svc
}

func WithLoader(ctx context.Context, svc LoaderService) context.Context {
	return context.WithValue(ctx, loaderKey{}, svc)
}

func GetLoader(ctx context.Context) LoaderService {
	svc, _ := ctx.Value(loaderKey{}).(LoaderService)
	return svc
}

// =============================================================================
// Step Definitions
// =============================================================================

// Initialize prepares the pipeline execution.
var Initialize = workflow.NewStep("initialize", func(ctx workflow.Context) (InitOutput, error) {
	input := workflow.Input[PipelineInput](ctx)

	// Set defaults
	if input.Options.BatchSize == 0 {
		input.Options.BatchSize = 1000
	}
	if input.Options.ParallelWorkers == 0 {
		input.Options.ParallelWorkers = 4
	}

	return InitOutput{
		StartTime:     time.Now(),
		SourceCount:   len(input.Sources),
		Configuration: fmt.Sprintf("batch=%d,workers=%d", input.Options.BatchSize, input.Options.ParallelWorkers),
	}, nil
})

// ExtractAll extracts data from all sources in parallel.
var ExtractAll = workflow.NewStep("extract-all", func(ctx workflow.Context) ([]ExtractionOutput, error) {
	input := workflow.Input[PipelineInput](ctx)
	_ = Initialize.MustOutput(ctx)

	results := make([]ExtractionOutput, len(input.Sources))
	extractor := GetExtractor(ctx)

	// Process each source
	// In a real implementation, this would use workflow.Map for true parallelism
	for i, source := range input.Sources {
		start := time.Now()

		var recordCount int
		var extractErr string

		if extractor == nil {
			// Mock extraction
			recordCount = 1000 + i*500 // Vary by source
		} else {
			records, err := extractor.Extract(ctx, source)
			if err != nil {
				extractErr = err.Error()
				if input.Options.FailOnError {
					return nil, fmt.Errorf("extraction failed for %s: %w", source.Name, err)
				}
			} else {
				recordCount = len(records)
			}
		}

		results[i] = ExtractionOutput{
			SourceName:  source.Name,
			RecordCount: recordCount,
			ExtractedAt: time.Now(),
			DurationMs:  time.Since(start).Milliseconds(),
			BytesRead:   int64(recordCount * 500), // Estimate
			Checksum:    fmt.Sprintf("sha256:%s-%d", source.Name, recordCount),
			Error:       extractErr,
		}
	}

	return results, nil
}).WithRetry(&retry.Policy{
	MaxAttempts:  3,
	InitialDelay: 5 * time.Second,
	MaxDelay:     60 * time.Second,
	Multiplier:   2.0,
}).WithTimeout(5 * time.Minute)

// MergeData merges data from all sources.
var MergeData = workflow.NewStep("merge-data", func(ctx workflow.Context) (MergeOutput, error) {
	extractions := ExtractAll.MustOutput(ctx)

	breakdown := make(map[string]int)
	total := 0

	for _, ext := range extractions {
		if ext.Error == "" {
			breakdown[ext.SourceName] = ext.RecordCount
			total += ext.RecordCount
		}
	}

	return MergeOutput{
		TotalRecords:    total,
		SourceBreakdown: breakdown,
		MergedAt:        time.Now(),
	}, nil
})

// TransformData applies transformations to the data.
var TransformData = workflow.NewStep("transform-data", func(ctx workflow.Context) (TransformOutput, error) {
	input := workflow.Input[PipelineInput](ctx)
	merged := MergeData.MustOutput(ctx)

	// Define transformations based on destination type
	transformations := []string{
		"normalize_timestamps",
		"trim_strings",
		"convert_nulls",
	}

	// Add destination-specific transformations
	if input.Destination.Type == "warehouse" {
		transformations = append(transformations, "partition_by_date")
	}

	transformer := GetTransformer(ctx)
	outputRecords := merged.TotalRecords
	droppedRecords := 0

	if transformer != nil {
		// In real implementation, would pass actual records
		// records, err := transformer.Transform(ctx, mergedRecords, transformations)
		// For mock, just simulate some records being dropped
		droppedRecords = merged.TotalRecords / 100 // 1% dropped
		outputRecords = merged.TotalRecords - droppedRecords
	} else {
		// Mock: 1% of records dropped during transform
		droppedRecords = merged.TotalRecords / 100
		outputRecords = merged.TotalRecords - droppedRecords
	}

	return TransformOutput{
		InputRecords:    merged.TotalRecords,
		OutputRecords:   outputRecords,
		DroppedRecords:  droppedRecords,
		TransformedAt:   time.Now(),
		Transformations: transformations,
	}, nil
}).WithTimeout(10 * time.Minute)

// ValidateData validates the transformed data.
var ValidateData = workflow.NewStep("validate-data", func(ctx workflow.Context) (ValidationOutput, error) {
	input := workflow.Input[PipelineInput](ctx)
	transformed := TransformData.MustOutput(ctx)

	if !input.Options.ValidateSchema {
		return ValidationOutput{
			TotalRecords:   transformed.OutputRecords,
			ValidRecords:   transformed.OutputRecords,
			InvalidRecords: 0,
		}, nil
	}

	// Mock validation - 99% valid
	invalidCount := transformed.OutputRecords / 100
	validCount := transformed.OutputRecords - invalidCount

	errorsByField := map[string]int{
		"email":   invalidCount / 3,
		"phone":   invalidCount / 3,
		"zipcode": invalidCount / 3,
	}

	var sampleErrors []ValidationError
	if invalidCount > 0 {
		sampleErrors = []ValidationError{
			{RecordIndex: 42, Field: "email", Value: "invalid-email", Error: "invalid email format"},
			{RecordIndex: 156, Field: "phone", Value: "12345", Error: "phone too short"},
		}
	}

	return ValidationOutput{
		TotalRecords:   transformed.OutputRecords,
		ValidRecords:   validCount,
		InvalidRecords: invalidCount,
		ErrorsByField:  errorsByField,
		SampleErrors:   sampleErrors,
	}, nil
})

// DeduplicateData removes duplicate records.
var DeduplicateData = workflow.NewStep("deduplicate-data", func(ctx workflow.Context) (DedupeOutput, error) {
	input := workflow.Input[PipelineInput](ctx)
	validated := ValidateData.MustOutput(ctx)

	if len(input.Options.DeduplicateBy) == 0 {
		return DedupeOutput{
			InputRecords:  validated.ValidRecords,
			OutputRecords: validated.ValidRecords,
			Duplicates:    0,
			DedupedAt:     time.Now(),
		}, nil
	}

	// Mock deduplication - 5% duplicates
	duplicates := validated.ValidRecords / 20
	outputRecords := validated.ValidRecords - duplicates

	return DedupeOutput{
		InputRecords:  validated.ValidRecords,
		OutputRecords: outputRecords,
		Duplicates:    duplicates,
		DedupedAt:     time.Now(),
	}, nil
})

// LoadData loads the data into the destination.
var LoadData = workflow.NewStep("load-data", func(ctx workflow.Context) (LoadOutput, error) {
	input := workflow.Input[PipelineInput](ctx)
	deduped := DeduplicateData.MustOutput(ctx)

	start := time.Now()
	loader := GetLoader(ctx)

	if loader == nil {
		// Mock load
		return LoadOutput{
			RecordsLoaded:  deduped.OutputRecords,
			RecordsFailed:  0,
			LoadedAt:       time.Now(),
			DurationMs:     100,
			BytesWritten:   int64(deduped.OutputRecords * 500),
		}, nil
	}

	result, err := loader.Load(ctx, input.Destination, nil) // Would pass actual records
	if err != nil {
		return LoadOutput{}, fmt.Errorf("load failed: %w", err)
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return *result, nil
}).WithRetry(&retry.Policy{
	MaxAttempts:  5,
	InitialDelay: 10 * time.Second,
	MaxDelay:     2 * time.Minute,
	Multiplier:   2.0,
}).WithTimeout(30 * time.Minute)

// Finalize creates the final pipeline report.
var Finalize = workflow.NewStep("finalize", func(ctx workflow.Context) (PipelineOutput, error) {
	input := workflow.Input[PipelineInput](ctx)
	init := Initialize.MustOutput(ctx)
	extractions := ExtractAll.MustOutput(ctx)
	transform := TransformData.MustOutput(ctx)
	validation := ValidateData.MustOutput(ctx)
	dedupe := DeduplicateData.MustOutput(ctx)
	load := LoadData.MustOutput(ctx)

	// Calculate summary
	sourcesFailed := 0
	recordsExtracted := 0
	for _, ext := range extractions {
		if ext.Error != "" {
			sourcesFailed++
		} else {
			recordsExtracted += ext.RecordCount
		}
	}

	status := "success"
	if sourcesFailed > 0 && sourcesFailed < len(extractions) {
		status = "partial"
	} else if sourcesFailed == len(extractions) {
		status = "failed"
	}
	if load.RecordsFailed > 0 {
		status = "partial"
	}

	successRate := 0.0
	if recordsExtracted > 0 {
		successRate = float64(load.RecordsLoaded) / float64(recordsExtracted) * 100
	}

	return PipelineOutput{
		PipelineID:    input.PipelineID,
		Status:        status,
		StartTime:     init.StartTime,
		EndTime:       time.Now(),
		TotalDuration: time.Since(init.StartTime).String(),
		Extractions:   extractions,
		Transform:     transform,
		Validation:    validation,
		Dedupe:        dedupe,
		Load:          load,
		Summary: PipelineSummary{
			SourcesProcessed:   len(extractions) - sourcesFailed,
			SourcesFailed:      sourcesFailed,
			RecordsExtracted:   recordsExtracted,
			RecordsTransformed: transform.OutputRecords,
			RecordsLoaded:      load.RecordsLoaded,
			RecordsFailed:      validation.InvalidRecords + load.RecordsFailed,
			SuccessRate:        successRate,
		},
	}, nil
})

// =============================================================================
// Workflow Definition
// =============================================================================

// DataPipelineWorkflow orchestrates the complete ETL process.
//
// DAG structure:
//
//	Initialize
//	    │
//	    ▼
//	ExtractAll (extracts from all sources)
//	    │
//	    ▼
//	MergeData
//	    │
//	    ▼
//	TransformData
//	    │
//	    ▼
//	ValidateData
//	    │
//	    ▼
//	DeduplicateData
//	    │
//	    ▼
//	LoadData
//	    │
//	    ▼
//	Finalize
//
var DataPipelineWorkflow = workflow.Define("data-pipeline",
	Initialize.After(),
	ExtractAll.After(Initialize),
	MergeData.After(ExtractAll),
	TransformData.After(MergeData),
	ValidateData.After(TransformData),
	DeduplicateData.After(ValidateData),
	LoadData.After(DeduplicateData),
	Finalize.After(LoadData),
)
