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
	"github.com/rs/zerolog/log"
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
		}
	}

	if quality := os.Getenv("WEBP_QUALITY"); quality != "" {
		if value, err := strconv.Atoi(quality); err == nil && value > 0 && value <= 100 {
			config.WebpQuality = value
		}
	}

	if quality := os.Getenv("AVIF_QUALITY"); quality != "" {
		if value, err := strconv.Atoi(quality); err == nil && value > 0 && value <= 100 {
			config.AvifQuality = value
		}
	}

	if quality := os.Getenv("JPEG_QUALITY"); quality != "" {
		if value, err := strconv.Atoi(quality); err == nil && value > 0 && value <= 100 {
			config.JpegQuality = value
		}
	}

	if dimension := os.Getenv("MAX_DIMENSION"); dimension != "" {
		if value, err := strconv.Atoi(dimension); err == nil && value > 0 {
			config.MaxDimension = value
		}
	}

	if processes := os.Getenv("PARALLEL_PROCESSES"); processes != "" {
		if value, err := strconv.Atoi(processes); err == nil && value > 0 {
			config.ParallelProcesses = value
		}
	}

	if cpus := os.Getenv("CPU_COUNT"); cpus != "" {
		if value, err := strconv.Atoi(cpus); err == nil && value > 0 {
			config.CpuCount = value
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
		log.Error().
			Err(err).
			Str("file", filepath.Base(filePath)).
			Msg("Failed to get file info")
		return
	}

	originalSize := originalInfo.Size()
	tempPath := strings.TrimSuffix(filePath, filepath.Ext(filePath)) + "-compressed" + filepath.Ext(filePath)

	// Load image
	image, err := vips.NewImageFromFile(filePath)
	if err != nil {
		log.Error().
			Err(err).
			Str("file", filepath.Base(filePath)).
			Msg("Failed to load image")
		return
	}
	defer image.Close()

	// Resize if needed
	if image.Width() > config.MaxDimension || image.Height() > config.MaxDimension {
		scale := float64(config.MaxDimension) / float64(max(image.Width(), image.Height()))
		err = image.Resize(scale, vips.KernelLanczos3)
		if err != nil {
			log.Error().
				Err(err).
				Str("file", filepath.Base(filePath)).
				Msg("Failed to resize image")
			return
		}
	}

	// Prepare export parameters based on output format
	var exportParams *vips.ExportParams
	switch config.OutputFormat {
	case "webp":
		exportParams = &vips.ExportParams{
			Format:  vips.ImageTypeWEBP,
			Quality: config.WebpQuality,
		}
	case "avif":
		exportParams = &vips.ExportParams{
			Format:  vips.ImageTypeAVIF,
			Quality: config.AvifQuality,
		}
	case "jpg", "jpeg":
		exportParams = &vips.ExportParams{
			Format:  vips.ImageTypeJPEG,
			Quality: config.JpegQuality,
		}
	default:
		// Fallback to AVIF if unknown format specified
		exportParams = &vips.ExportParams{
			Format:  vips.ImageTypeAVIF,
			Quality: config.AvifQuality,
		}
	}

	// Export the image to buffer
	buf, _, err := image.Export(exportParams)
	if err != nil {
		log.Error().
			Err(err).
			Str("file", filepath.Base(filePath)).
			Str("format", config.OutputFormat).
			Msg("Failed to export image")
		return
	}

	// Write the compressed image to temporary file
	err = os.WriteFile(tempPath, buf, originalInfo.Mode())
	if err != nil {
		log.Error().
			Err(err).
			Str("file", filepath.Base(filePath)).
			Msg("Failed to write compressed image")
		return
	}

	// Get the size of the compressed file
	compressedInfo, err := os.Stat(tempPath)
	if err != nil {
		log.Error().
			Err(err).
			Str("file", filepath.Base(filePath)).
			Msg("Failed to get compressed file info")
		os.Remove(tempPath)
		return
	}

	compressedSize := compressedInfo.Size()
	reductionPercent := float64(originalSize-compressedSize) / float64(originalSize) * 100

	// Decide whether to keep the compressed version
	if reductionPercent >= float64(config.ReductionThreshold) {
		err = os.Rename(tempPath, filePath)
		if err != nil {
			log.Error().
				Err(err).
				Str("file", filepath.Base(filePath)).
				Msg("Failed to replace original with compressed image")
			os.Remove(tempPath)
			return
		}

		log.Info().
			Msgf("Image compressed and replaced: original_size_kb=%.1f compressed_size_kb=%.1f reduction_percent=%.1f processing_time_sec=%.1f file=%s",
				float64(originalSize)/1024, float64(compressedSize)/1024, reductionPercent, time.Since(startTime).Seconds(), filepath.Base(filePath))
	} else {
		// Remove the temporary file if the reduction doesn't meet the threshold
		os.Remove(tempPath)
		log.Info().
			Msgf("Compression below threshold, keeping original: reduction_percent=%.1f processing_time_sec=%.1f file=%s",
				reductionPercent, time.Since(startTime).Seconds(), filepath.Base(filePath))
	}
}

// waitForFileStability waits until the file size and mod time remain unchanged for a short period.
// Returns an error if the file doesn't exist or stability isn't reached within a timeout.
func waitForFileStability(filePath string, checkInterval time.Duration, stableDuration time.Duration, timeout time.Duration) error {
	startTime := time.Now()

	var lastInfo os.FileInfo
	var err error
	stableChecks := 0
	requiredStableChecks := int(stableDuration / checkInterval)
	if requiredStableChecks < 1 {
		requiredStableChecks = 1 // Ensure at least one check interval passes
	}

	for {
		// Check for overall timeout
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
			continue
		}

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
}

func main() {
	// Configure logger
	// Set default level to info
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	// Check if debug mode is enabled
	if os.Getenv("DEBUG") == "true" {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	// Log startup
	log.Info().Msg("HedgeDoc Image Compressor starting up")

	// Load configuration
	config := loadConfig()

	// Log configuration
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
	// Set logging level to warning to reduce verbosity of internal libvips messages
	vips.LoggingSettings(nil, vips.LogLevelWarning)
	vips.Startup(&vips.Config{
		ConcurrencyLevel: config.CpuCount,
		MaxCacheFiles:    0,
		MaxCacheMem:      0,
		MaxCacheSize:     0,
		ReportLeaks:      false,
	})
	defer vips.Shutdown()

	// Make sure the watch directory exists
	if _, err := os.Stat(config.WatchDir); os.IsNotExist(err) {
		log.Fatal().
			Err(err).
			Str("directory", config.WatchDir).
			Msg("Watch directory does not exist")
	}

	// Create file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Revert to standard error handling
		log.Fatal().Err(err).Msg("Failed to create file watcher")
	}
	defer watcher.Close()

	// Create wait group for parallel processing
	var wg sync.WaitGroup

	// Cleanup routine for recently processed files
	go func() {
		for {
			// Clean up entries older than 5 seconds
			now := time.Now()
			recentlyProcessed.Range(func(key, value interface{}) bool {
				processTime, ok := value.(time.Time)
				if ok && now.Sub(processTime) > 5*time.Second {
					recentlyProcessed.Delete(key)
				}
				return true
			})
			time.Sleep(5 * time.Second)
		}
	}()

	// Start watching for file events
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// We only care about newly created or modified files
				if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
					filePath := event.Name
					if isImageFile(filePath) && !isTemporaryCompressedFile(filepath.Base(filePath)) {
						// Wait for the file to be fully written by checking stability
						err := waitForFileStability(filePath, 100*time.Millisecond, 300*time.Millisecond, 5*time.Second)
						if err != nil {
							log.Error().Err(err).Str("file", filePath).Msg("Failed waiting for file stability, skipping processing")
							continue // Skip processing this file
						}

						// Check if we have capacity for a new worker
						if config.ParallelProcesses == 1 {
							// For single process mode, just process directly
							wg.Add(1)
							processImage(filePath, config, &wg)
						} else {
							// For parallel mode, spawn a goroutine
							wg.Add(1)
							go processImage(filePath, config, &wg)
						}
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Error().Err(err).Msg("Watcher error")
			}
		}
	}()

	// Add the watch directory and check error separately
	addWatchErr := watcher.Add(config.WatchDir)
	if addWatchErr != nil {
		// Revert to standard error handling
		log.Fatal().
			Err(addWatchErr).
			Str("directory", config.WatchDir).
			Msg("Failed to watch directory")
	}

	log.Info().
		Str("directory", config.WatchDir).
		Msg("Now watching for new image uploads")

	// Keep the application running
	if os.Getenv("DEBUG") == "true" {
		fmt.Println("Press Ctrl+C to stop")
	}
	select {} // Block forever
}
