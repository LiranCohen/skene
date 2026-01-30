// Package imageprocessing implements an image processing pipeline.
//
// This workflow demonstrates:
// - Fan-out processing with workflow.Map
// - Child workflows for individual image processing
// - Parallel thumbnail generation
// - Aggregation of results
package imageprocessing

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

// BatchInput is the workflow input for batch image processing.
type BatchInput struct {
	BatchID   string   `json:"batch_id"`
	ImageURLs []string `json:"image_urls"`
	Options   Options  `json:"options"`
}

type Options struct {
	GenerateThumbnails bool     `json:"generate_thumbnails"`
	ThumbnailSizes     []int    `json:"thumbnail_sizes"` // widths in pixels
	OptimizeForWeb     bool     `json:"optimize_for_web"`
	ExtractMetadata    bool     `json:"extract_metadata"`
	ApplyWatermark     bool     `json:"apply_watermark"`
	WatermarkText      string   `json:"watermark_text,omitempty"`
	OutputFormat       string   `json:"output_format"` // "jpeg", "png", "webp"
	Quality            int      `json:"quality"`       // 1-100
}

// SingleImageInput is the input for processing a single image.
type SingleImageInput struct {
	ImageURL string  `json:"image_url"`
	Index    int     `json:"index"`
	Options  Options `json:"options"`
}

// Step outputs

type ValidationOutput struct {
	Valid        bool     `json:"valid"`
	Errors       []string `json:"errors,omitempty"`
	ImageCount   int      `json:"image_count"`
	TotalSizeMB  float64  `json:"total_size_mb,omitempty"`
}

type DownloadOutput struct {
	LocalPath   string  `json:"local_path"`
	SizeBytes   int64   `json:"size_bytes"`
	ContentType string  `json:"content_type"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
}

type MetadataOutput struct {
	EXIF        map[string]string `json:"exif,omitempty"`
	Camera      string            `json:"camera,omitempty"`
	DateTaken   time.Time         `json:"date_taken,omitempty"`
	GPSLocation *GPSLocation      `json:"gps_location,omitempty"`
}

type GPSLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type ThumbnailOutput struct {
	Thumbnails []Thumbnail `json:"thumbnails"`
}

type Thumbnail struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	URL    string `json:"url"`
	Size   int64  `json:"size_bytes"`
}

type OptimizeOutput struct {
	OptimizedURL   string  `json:"optimized_url"`
	OriginalSize   int64   `json:"original_size"`
	OptimizedSize  int64   `json:"optimized_size"`
	SavingsPercent float64 `json:"savings_percent"`
}

type WatermarkOutput struct {
	WatermarkedURL string `json:"watermarked_url"`
}

type SingleImageOutput struct {
	Index        int             `json:"index"`
	OriginalURL  string          `json:"original_url"`
	ProcessedURL string          `json:"processed_url"`
	Thumbnails   []Thumbnail     `json:"thumbnails,omitempty"`
	Metadata     *MetadataOutput `json:"metadata,omitempty"`
	ProcessedAt  time.Time       `json:"processed_at"`
	Error        string          `json:"error,omitempty"`
}

type BatchOutput struct {
	BatchID       string              `json:"batch_id"`
	TotalImages   int                 `json:"total_images"`
	Successful    int                 `json:"successful"`
	Failed        int                 `json:"failed"`
	Results       []SingleImageOutput `json:"results"`
	ProcessingTime string             `json:"processing_time"`
	CompletedAt   time.Time           `json:"completed_at"`
}

// =============================================================================
// Service Interfaces
// =============================================================================

type ImageService interface {
	Download(ctx context.Context, url string) (*DownloadOutput, error)
	ExtractMetadata(ctx context.Context, path string) (*MetadataOutput, error)
	GenerateThumbnail(ctx context.Context, path string, width int) (*Thumbnail, error)
	Optimize(ctx context.Context, path string, format string, quality int) (*OptimizeOutput, error)
	ApplyWatermark(ctx context.Context, path, text string) (*WatermarkOutput, error)
	Upload(ctx context.Context, path string) (string, error)
}

type imageServiceKey struct{}

func WithImageService(ctx context.Context, svc ImageService) context.Context {
	return context.WithValue(ctx, imageServiceKey{}, svc)
}

func GetImageService(ctx context.Context) ImageService {
	svc, _ := ctx.Value(imageServiceKey{}).(ImageService)
	return svc
}

// =============================================================================
// Single Image Processing Steps
// =============================================================================

// DownloadImage downloads the image to local storage.
var DownloadImage = workflow.NewStep("download-image", func(ctx workflow.Context) (DownloadOutput, error) {
	input := workflow.Input[SingleImageInput](ctx)

	svc := GetImageService(ctx)
	if svc == nil {
		// Mock response
		return DownloadOutput{
			LocalPath:   fmt.Sprintf("/tmp/img_%d.jpg", input.Index),
			SizeBytes:   1024 * 1024,
			ContentType: "image/jpeg",
			Width:       1920,
			Height:      1080,
		}, nil
	}

	result, err := svc.Download(ctx, input.ImageURL)
	if err != nil {
		return DownloadOutput{}, fmt.Errorf("download failed: %w", err)
	}

	return *result, nil
}).WithRetry(&retry.Policy{
	MaxAttempts:  3,
	InitialDelay: time.Second,
	MaxDelay:     10 * time.Second,
	Multiplier:   2.0,
}).WithTimeout(60 * time.Second)

// ExtractMetadata extracts EXIF and other metadata.
var ExtractMetadata = workflow.NewStep("extract-metadata", func(ctx workflow.Context) (MetadataOutput, error) {
	input := workflow.Input[SingleImageInput](ctx)
	download := DownloadImage.MustOutput(ctx)

	if !input.Options.ExtractMetadata {
		return MetadataOutput{}, nil
	}

	svc := GetImageService(ctx)
	if svc == nil {
		return MetadataOutput{
			EXIF:      map[string]string{"Make": "Canon", "Model": "EOS 5D"},
			Camera:    "Canon EOS 5D",
			DateTaken: time.Now().Add(-24 * time.Hour),
		}, nil
	}

	result, err := svc.ExtractMetadata(ctx, download.LocalPath)
	if err != nil {
		// Non-fatal - continue without metadata
		return MetadataOutput{}, nil
	}

	return *result, nil
})

// GenerateThumbnails creates thumbnails at specified sizes.
var GenerateThumbnails = workflow.NewStep("generate-thumbnails", func(ctx workflow.Context) (ThumbnailOutput, error) {
	input := workflow.Input[SingleImageInput](ctx)
	download := DownloadImage.MustOutput(ctx)

	if !input.Options.GenerateThumbnails || len(input.Options.ThumbnailSizes) == 0 {
		return ThumbnailOutput{}, nil
	}

	svc := GetImageService(ctx)
	var thumbnails []Thumbnail

	for _, size := range input.Options.ThumbnailSizes {
		if svc == nil {
			// Mock response
			thumbnails = append(thumbnails, Thumbnail{
				Width:  size,
				Height: int(float64(size) * float64(download.Height) / float64(download.Width)),
				URL:    fmt.Sprintf("https://cdn.example.com/thumb_%d_%dpx.jpg", input.Index, size),
				Size:   int64(size * size / 10),
			})
			continue
		}

		thumb, err := svc.GenerateThumbnail(ctx, download.LocalPath, size)
		if err != nil {
			return ThumbnailOutput{}, fmt.Errorf("thumbnail generation failed for size %d: %w", size, err)
		}
		thumbnails = append(thumbnails, *thumb)
	}

	return ThumbnailOutput{Thumbnails: thumbnails}, nil
}).WithTimeout(30 * time.Second)

// OptimizeImage optimizes the image for web delivery.
var OptimizeImage = workflow.NewStep("optimize-image", func(ctx workflow.Context) (OptimizeOutput, error) {
	input := workflow.Input[SingleImageInput](ctx)
	download := DownloadImage.MustOutput(ctx)

	if !input.Options.OptimizeForWeb {
		return OptimizeOutput{
			OptimizedURL:   input.ImageURL,
			OriginalSize:   download.SizeBytes,
			OptimizedSize:  download.SizeBytes,
			SavingsPercent: 0,
		}, nil
	}

	svc := GetImageService(ctx)
	if svc == nil {
		optimizedSize := int64(float64(download.SizeBytes) * 0.7)
		return OptimizeOutput{
			OptimizedURL:   fmt.Sprintf("https://cdn.example.com/opt_%d.%s", input.Index, input.Options.OutputFormat),
			OriginalSize:   download.SizeBytes,
			OptimizedSize:  optimizedSize,
			SavingsPercent: 30.0,
		}, nil
	}

	result, err := svc.Optimize(ctx, download.LocalPath, input.Options.OutputFormat, input.Options.Quality)
	if err != nil {
		return OptimizeOutput{}, fmt.Errorf("optimization failed: %w", err)
	}

	return *result, nil
}).WithTimeout(60 * time.Second)

// ApplyWatermark adds watermark to the image.
var ApplyWatermark = workflow.NewStep("apply-watermark", func(ctx workflow.Context) (WatermarkOutput, error) {
	input := workflow.Input[SingleImageInput](ctx)
	optimized := OptimizeImage.MustOutput(ctx)

	if !input.Options.ApplyWatermark || input.Options.WatermarkText == "" {
		return WatermarkOutput{WatermarkedURL: optimized.OptimizedURL}, nil
	}

	svc := GetImageService(ctx)
	if svc == nil {
		return WatermarkOutput{
			WatermarkedURL: fmt.Sprintf("https://cdn.example.com/wm_%d.%s", input.Index, input.Options.OutputFormat),
		}, nil
	}

	// Note: In real implementation, we'd need the local path of the optimized image
	result, err := svc.ApplyWatermark(ctx, optimized.OptimizedURL, input.Options.WatermarkText)
	if err != nil {
		return WatermarkOutput{}, fmt.Errorf("watermark failed: %w", err)
	}

	return *result, nil
}).WithTimeout(30 * time.Second)

// FinalizeImage creates the final output for a single image.
var FinalizeImage = workflow.NewStep("finalize-image", func(ctx workflow.Context) (SingleImageOutput, error) {
	input := workflow.Input[SingleImageInput](ctx)
	thumbnails := GenerateThumbnails.MustOutput(ctx)
	metadata := ExtractMetadata.MustOutput(ctx)
	watermark := ApplyWatermark.MustOutput(ctx)

	output := SingleImageOutput{
		Index:        input.Index,
		OriginalURL:  input.ImageURL,
		ProcessedURL: watermark.WatermarkedURL,
		Thumbnails:   thumbnails.Thumbnails,
		ProcessedAt:  time.Now(),
	}

	if input.Options.ExtractMetadata && metadata.Camera != "" {
		output.Metadata = &metadata
	}

	return output, nil
})

// SingleImageWorkflow processes a single image.
//
// DAG structure:
//
//	DownloadImage
//	      │
//	      ├───────────────┬───────────────┐
//	      ▼               ▼               ▼
//	ExtractMetadata  GenerateThumbnails  OptimizeImage  (parallel)
//	      │               │               │
//	      │               │               ▼
//	      │               │         ApplyWatermark
//	      │               │               │
//	      └───────────────┴───────────────┘
//	                      │
//	                      ▼
//	               FinalizeImage
//
var SingleImageWorkflow = workflow.Define("single-image-processing",
	DownloadImage.After(),
	ExtractMetadata.After(DownloadImage),
	GenerateThumbnails.After(DownloadImage),
	OptimizeImage.After(DownloadImage),
	ApplyWatermark.After(OptimizeImage),
	FinalizeImage.After(ExtractMetadata, GenerateThumbnails, ApplyWatermark),
)

// =============================================================================
// Batch Processing Steps
// =============================================================================

// ValidateBatch validates the batch input.
var ValidateBatch = workflow.NewStep("validate-batch", func(ctx workflow.Context) (ValidationOutput, error) {
	input := workflow.Input[BatchInput](ctx)

	var errs []string

	if input.BatchID == "" {
		errs = append(errs, "batch_id is required")
	}
	if len(input.ImageURLs) == 0 {
		errs = append(errs, "at least one image URL is required")
	}
	if len(input.ImageURLs) > 100 {
		errs = append(errs, "maximum 100 images per batch")
	}

	if input.Options.Quality < 1 || input.Options.Quality > 100 {
		input.Options.Quality = 85 // Default
	}

	if input.Options.OutputFormat == "" {
		input.Options.OutputFormat = "webp" // Default
	}

	return ValidationOutput{
		Valid:      len(errs) == 0,
		Errors:     errs,
		ImageCount: len(input.ImageURLs),
	}, nil
})

// ProcessAllImages processes all images using Map.
var ProcessAllImages = workflow.NewStep("process-all-images", func(ctx workflow.Context) ([]SingleImageOutput, error) {
	input := workflow.Input[BatchInput](ctx)
	validation := ValidateBatch.MustOutput(ctx)

	if !validation.Valid {
		return nil, fmt.Errorf("batch validation failed: %v", validation.Errors)
	}

	// Create inputs for each image
	imageInputs := make([]SingleImageInput, len(input.ImageURLs))
	for i, url := range input.ImageURLs {
		imageInputs[i] = SingleImageInput{
			ImageURL: url,
			Index:    i,
			Options:  input.Options,
		}
	}

	// Process all images using Map (fan-out) with explicit type parameters
	results, err := workflow.Map[SingleImageInput, SingleImageOutput](ctx, imageInputs, SingleImageWorkflow)
	if err != nil {
		return nil, fmt.Errorf("batch processing failed: %w", err)
	}

	return results, nil
})

// AggregateBatch aggregates results into final batch output.
var AggregateBatch = workflow.NewStep("aggregate-batch", func(ctx workflow.Context) (BatchOutput, error) {
	input := workflow.Input[BatchInput](ctx)
	results := ProcessAllImages.MustOutput(ctx)

	successful := 0
	failed := 0
	for _, r := range results {
		if r.Error != "" {
			failed++
		} else {
			successful++
		}
	}

	return BatchOutput{
		BatchID:        input.BatchID,
		TotalImages:    len(input.ImageURLs),
		Successful:     successful,
		Failed:         failed,
		Results:        results,
		ProcessingTime: "calculated from events",
		CompletedAt:    time.Now(),
	}, nil
})

// =============================================================================
// Batch Workflow Definition
// =============================================================================

// BatchImageWorkflow processes a batch of images.
//
// DAG structure:
//
//	ValidateBatch
//	      │
//	      ▼
//	ProcessAllImages (uses Map to fan-out to SingleImageWorkflow)
//	      │
//	      ▼
//	AggregateBatch
//
var BatchImageWorkflow = workflow.Define("batch-image-processing",
	ValidateBatch.After(),
	ProcessAllImages.After(ValidateBatch),
	AggregateBatch.After(ProcessAllImages),
)
