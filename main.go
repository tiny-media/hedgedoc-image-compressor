package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log" // Use zerolog's global logger directly
)

// Recently processed files cache to avoid double-processing
var recentlyProcessed sync.Map
var processingCount atomic.Int32

// Configuration struct to hold all application settings
type Config struct {
	OutputFormat       string
	ReductionThreshold int
	WebpQuality        int
	AvifQuality        int
	JpegQuality        int
	MaxDimension       int
	ParallelProcesses  int
	CpuCount           int
	WatchDir           string
}

// Load configuration from environment variables with sensible defaults
func loadConfig() Config {
	// Set defaults
	config := Config{
		OutputFormat:       "avif",
		ReductionThreshold: 15,
		WebpQuality:        70,
		AvifQuality:        60,
		JpegQuality:        75,
		MaxDimension:       1600,
		ParallelProcesses:  1,
		CpuCount:           1,
		WatchDir:           "/hedgedoc/public/uploads",
	}

	// Override with environment variables if present
	if format := os.Getenv("OUTPUT_FORMAT"); format != "" {
		config.OutputFormat = strings.ToLower(format)
	}

	if threshold := os.Getenv("REDUCTION_THRESHOLD"); threshold != "" {
		if value, err := strconv.Atoi(threshold); err == nil && value >= 0 && value <= 100 {
			config.ReductionThreshold = value
		} else if err != nil {
			log.Warn().Err(err).Str("value", threshold).Msg("Invalid REDUCTION_THRESHOLD, using default")
		}
	}

	if quality := os.Getenv("WEBP_QUALITY"); quality != "" {
		if value, err := strconv.Atoi(quality); err == nil && value > 0 && value <= 100 {
			config.WebpQuality = value
		} else if err != nil {
			log.Warn().Err(err).Str("value", quality).Msg("Invalid WEBP_QUALITY, using default")
		}
	}

	if quality := os.Getenv("AVIF_QUALITY"); quality != "" {
		if value, err := strconv.Atoi(quality); err == nil && value > 0 && value <= 100 {
			config.AvifQuality = value
		} else if err != nil {
			log.Warn().Err(err).Str("value", quality).Msg("Invalid AVIF_QUALITY, using default")
		}
	}

	if quality := os.Getenv("JPEG_QUALITY"); quality != "" {
		if value, err := strconv.Atoi(quality); err == nil && value > 0 && value <= 100 {
			config.JpegQuality = value
		} else if err != nil {
			log.Warn().Err(err).Str("value", quality).Msg("Invalid JPEG_QUALITY, using default")
		}
	}

	if dimension := os.Getenv("MAX_DIMENSION"); dimension != "" {
		if value, err := strconv.Atoi(dimension); err == nil && value > 0 {
			config.MaxDimension = value
		} else if err != nil {
			log.Warn().Err(err).Str("value", dimension).Msg("Invalid MAX_DIMENSION, using default")
		}
	}

	if processes := os.Getenv("PARALLEL_PROCESSES"); processes != "" {
		if value, err := strconv.Atoi(processes); err == nil && value > 0 {
			config.ParallelProcesses = value
		} else if err != nil {
			log.Warn().Err(err).Str("value", processes).Msg("Invalid PARALLEL_PROCESSES, using default")
		}
	}

	if cpus := os.Getenv("CPU_COUNT"); cpus != "" {
		if value, err := strconv.Atoi(cpus); err == nil && value > 0 {
			config.CpuCount = value
		} else if err != nil {
			log.Warn().Err(err).Str("value", cpus).Msg("Invalid CPU_COUNT, using default")
		}
	}

	if dir := os.Getenv("WATCH_DIR"); dir != "" {
		config.WatchDir = dir
	}

	return config
}

// Checks if a file is an image based on its extension
func isImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tiff", ".svg", ".avif":
		return true
	default:
		return false
	}
}

// Checks if a filename indicates it's a temporary compressed file
func isTemporaryCompressedFile(filename string) bool {
	return strings.Contains(filename, "-compressed.")
}

// Process a single image file
func processImage(filePath string, config Config, wg *sync.WaitGroup) {
	defer wg.Done()

	// Check if this file was recently processed
	if _, exists := recentlyProcessed.Load(filePath); exists {
		log.Debug().
			Str("file", filepath.Base(filePath)).
			Msg("Skipping recently processed file")
		return
	}

	// Mark as being processed
	processingCount.Add(1)
	defer processingCount.Add(-1)
	recentlyProcessed.Store(filePath, time.Now())

	// Skip temporary compressed files
	if isTemporaryCompressedFile(filepath.Base(filePath)) {
		return
	}

	log.Info().
		Str("file", filepath.Base(filePath)).
		Msg("Processing new image")

	startTime := time.Now()

	// Get original file info and stats
	originalInfo, err := os.Stat(filePath)
	if err != nil {
		log.Error().Err(err).Str("file", filepath.Base(filePath)).Msg("Failed to get file info")
		return
	}

	originalSize := originalInfo.Size()
	tempPath := strings.TrimSuffix(filePath, filepath.Ext(filePath)) + "-compressed" + filepath.Ext(filePath)

	// Load image
	image, err := vips.NewImageFromFile(filePath)
	if err != nil {
		log.Error().Err(err).Str("file", filepath.Base(filePath)).Msg("Failed to load image")
		return
	}
	defer image.Close()

	// Resize if needed (using built-in max since Go 1.21+)
	if newWidth, newHeight := image.Width(), image.Height(); newWidth > config.MaxDimension || newHeight > config.MaxDimension {
		scale := float64(config.MaxDimension) / float64(max(newWidth, newHeight)) // Use built-in max
		if err := image.Resize(scale, vips.KernelLanczos3); err != nil {
			log.Error().Err(err).Str("file", filepath.Base(filePath)).Msg("Failed to resize image")
			return
		}
	}

	// Prepare export parameters based on output format
	var exportParams vips.ExportParams // Use zero value initially
	switch config.OutputFormat {
	case "webp":
		exportParams = vips.ExportParams{
			Format:  vips.ImageTypeWEBP,
			Quality: config.WebpQuality,
		}
	case "avif":
		exportParams = vips.ExportParams{
			Format:  vips.ImageTypeAVIF,
			Quality: config.AvifQuality,
		}
	case "jpg", "jpeg":
		exportParams = vips.ExportParams{
			Format:  vips.ImageTypeJPEG,
			Quality: config.JpegQuality,
		}
	default:
		log.Warn().Str("format", config.OutputFormat).Msg("Unknown OUTPUT_FORMAT specified, falling back to avif")
		exportParams = vips.ExportParams{ // Fallback to AVIF
			Format:  vips.ImageTypeAVIF,
			Quality: config.AvifQuality,
		}
	}

	// Export the image to buffer
	buf, _, err := image.Export(&exportParams) // Pass pointer
	if err != nil {
		log.Error().Err(err).Str("file", filepath.Base(filePath)).Str("format", config.OutputFormat).Msg("Failed to export image")
		return
	}

	// Write the compressed image to temporary file
	if err := os.WriteFile(tempPath, buf, originalInfo.Mode()); err != nil {
		log.Error().Err(err).Str("file", filepath.Base(filePath)).Msg("Failed to write compressed image")
		return
	}

	// Get the size of the compressed file
	compressedInfo, err := os.Stat(tempPath)
	if err != nil {
		log.Error().Err(err).Str("file", filepath.Base(filePath)).Msg("Failed to get compressed file info")
		_ = os.Remove(tempPath) // Attempt cleanup, ignore error
		return
	}

	compressedSize := compressedInfo.Size()
	reductionPercent := 0.0
	if originalSize > 0 { // Avoid division by zero
		reductionPercent = float64(originalSize-compressedSize) / float64(originalSize) * 100
	}

	// Decide whether to keep the compressed version
	if reductionPercent >= float64(config.ReductionThreshold) && compressedSize < originalSize { // Also ensure it's actually smaller
		if err := os.Rename(tempPath, filePath); err != nil {
			log.Error().Err(err).Str("file", filepath.Base(filePath)).Msg("Failed to replace original with compressed image")
			_ = os.Remove(tempPath) // Attempt cleanup, ignore error
			return
		}

		log.Info().
			Float64("original_kb", float64(originalSize)/1024).
			Float64("compressed_kb", float64(compressedSize)/1024).
			Float64("reduction_pct", reductionPercent).
			Float64("processing_sec", time.Since(startTime).Seconds()).
			Str("file", filepath.Base(filePath)).
			Msg("Image compressed and replaced")
	} else {
		// Remove the temporary file if the reduction doesn't meet the threshold or if it's larger
		_ = os.Remove(tempPath) // Attempt cleanup, ignore error
		log.Info().
			Float64("reduction_pct", reductionPercent).
			Float64("processing_sec", time.Since(startTime).Seconds()).
			Str("file", filepath.Base(filePath)).
			Msg("Compression below threshold or not smaller, keeping original")
	}
}

// waitForFileStability waits until the file size and mod time remain unchanged for a short period.
// Returns an error if the file doesn't exist or stability isn't reached within a timeout.
func waitForFileStability(filePath string, checkInterval time.Duration, stableDuration time.Duration, timeout time.Duration) error {
	startTime := time.Now()

	var lastInfo os.FileInfo

	stableChecks := 0
	requiredStableChecks := int(stableDuration / checkInterval)
	if requiredStableChecks < 1 {
		requiredStableChecks = 1 // Ensure at least one check interval passes
	}

	for {
		// Check for overall timeout first
		if time.Since(startTime) > timeout {
			return fmt.Errorf("timeout waiting for file stability: %s", filePath)
		}

		currentInfo, err := os.Stat(filePath)
		if err != nil {
			// If the file doesn't exist (maybe deleted quickly), return error
			if os.IsNotExist(err) {
				return fmt.Errorf("file disappeared while waiting for stability: %s", filePath)
			}
			// Log other stat errors but continue trying
			log.Warn().Err(err).Str("file", filePath).Msg("Error stating file during stability check, retrying...")
			time.Sleep(checkInterval) // Wait before retrying stat
			continue                  // Continue the loop
		}

		// Check for stability
		if lastInfo != nil && currentInfo.Size() == lastInfo.Size() && currentInfo.ModTime() == lastInfo.ModTime() {
			stableChecks++
			if stableChecks >= requiredStableChecks {
				log.Debug().Str("file", filePath).Msg("File stability confirmed")
				return nil // File is stable
			}
		} else {
			// Reset stable count if size or mod time changed
			stableChecks = 0
		}

		lastInfo = currentInfo
		time.Sleep(checkInterval)
	}
	// Note: The outer 'err' declared earlier is intentionally unused if the loop exits normally or via timeout return.
}

func main() {
	// Configure logger (using zerolog's global logger)
	// Set default level to info
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	// Check if debug mode is enabled
	if os.Getenv("DEBUG") == "true" {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	// Use console writer for human-friendly output
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	// Log startup
	log.Info().Msg("HedgeDoc Image Compressor starting up")

	// Load configuration
	config := loadConfig()

	// Log configuration details
	log.Info().
		Str("output_format", config.OutputFormat).
		Int("reduction_threshold", config.ReductionThreshold).
		Int("webp_quality", config.WebpQuality).
		Int("avif_quality", config.AvifQuality).
		Int("jpeg_quality", config.JpegQuality).
		Int("max_dimension", config.MaxDimension).
		Int("parallel_processes", config.ParallelProcesses).
		Int("cpu_count", config.CpuCount).
		Str("watch_dir", config.WatchDir).
		Msg("Configuration loaded")

	// Initialize vips
	vips.LoggingSettings(nil, vips.LogLevelWarning) // Reduce vips internal logging
	vips.Startup(&vips.Config{
		ConcurrencyLevel: config.CpuCount, // Let vips use configured CPU count
		// Keep other vips cache settings at default (0 means default/auto)
	})
	defer vips.Shutdown()

	// --- Startup Checks ---

	// Make sure the watch directory exists (using standard "if with short statement")
	if _, err := os.Stat(config.WatchDir); err != nil {
		if os.IsNotExist(err) {
			log.Fatal().Err(err).Str("directory", config.WatchDir).Msg("Watch directory does not exist")
		}
		// Log other Stat errors but continue if it's not a NotExist error
		log.Warn().Err(err).Str("directory", config.WatchDir).Msg("Warning: Error stating watch directory (but it exists)")
	}

	// Create file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Use standard zerolog fatal error handling
		log.Fatal().Err(err).Msg("Failed to create file watcher")
	}
	defer watcher.Close()

	// Add the watch directory
	if err := watcher.Add(config.WatchDir); err != nil {
		// Use standard zerolog fatal error handling
		log.Fatal().Err(err).Str("directory", config.WatchDir).Msg("Failed to watch directory")
	}

	// --- Goroutines ---

	// Create wait group for parallel processing
	var wg sync.WaitGroup

	// Cleanup routine for recently processed files cache
	go func() {
		ticker := time.NewTicker(5 * time.Second) // Use a ticker for periodic cleanup
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			recentlyProcessed.Range(func(key, value interface{}) bool {
				if processTime, ok := value.(time.Time); ok {
					if now.Sub(processTime) > 5*time.Second {
						recentlyProcessed.Delete(key)
						log.Debug().Str("file", key.(string)).Msg("Removed file from recently processed cache")
					}
				} else {
					// Clean up potentially invalid entries
					recentlyProcessed.Delete(key)
				}
				return true // Continue iteration
			})
		}
	}()

	// Start watching for file events
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					log.Info().Msg("Watcher events channel closed, exiting event loop.")
					return // Exit goroutine if channel is closed
				}

				// We only care about newly created or written files (ignoring CHMOD etc.)
				if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
					filePath := event.Name
					// Basic filtering
					if !isImageFile(filePath) || isTemporaryCompressedFile(filepath.Base(filePath)) {
						continue // Skip non-images or our temp files
					}

					// Wait for the file to be fully written by checking stability
					// Use slightly longer intervals for stability check
					stabilityErr := waitForFileStability(filePath, 200*time.Millisecond, 500*time.Millisecond, 10*time.Second)
					if stabilityErr != nil {
						log.Error().Err(stabilityErr).Str("file", filePath).Msg("Skipping processing due to file stability issue")
						continue // Skip processing this file
					}

					// Process the image (potentially in parallel)
					wg.Add(1)
					if config.ParallelProcesses <= 1 {
						// Run directly if parallelism is 1 or less
						processImage(filePath, config, &wg)
					} else {
						// Run in a new goroutine for parallel processing
						go processImage(filePath, config, &wg)
						// Optional: Add logic here to limit the number of active goroutines
						// if PARALLEL_PROCESSES needs to be a strict limit, e.g., using a semaphore.
						// For simplicity, this currently allows potentially more than PARALLEL_PROCESSES
						// goroutines if events arrive very rapidly, but WaitGroup still tracks completion.
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					log.Info().Msg("Watcher errors channel closed, exiting event loop.")
					return // Exit goroutine if channel is closed
				}
				log.Error().Err(err).Msg("Watcher error")
			}
		}
	}()

	log.Info().Str("directory", config.WatchDir).Msg("Now watching for new image uploads...")

	// Keep the application running indefinitely
	select {} // Block forever
}
