package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Configuration
const (
	// Set proxy URL here (empty string = no proxy)
	// Examples:
	// PROXY_URL = "http://localhost:8881"    // Use proxy
	// PROXY_URL = ""                         // No proxy (direct connection)
	// PROXY_URL = "http://your-proxy:8080"   // Custom proxy
	PROXY_URL = "http://localhost:8881"
)

// Track represents a music track with artist, title, and raw string
type Track struct {
	Artist string // Artist name
	Title  string // Song title
	Raw    string // Original string from playlist file
}

// FailureReason represents the reason why a track failed to download
type FailureReason int

const (
	NetworkError FailureReason = iota // Network/proxy issues
	NotFound                          // Track not found on YouTube
	UnknownError                      // Other errors
)

// Downloader handles concurrent downloading of music tracks
type Downloader struct {
	workers          int            // Number of concurrent workers
	downloaded       int            // Count of successfully downloaded tracks
	skipped          int            // Count of skipped tracks (already exist)
	failed           int            // Count of failed downloads
	mutex            sync.RWMutex   // Mutex for thread-safe counter updates
	retryCount       int            // Number of retry attempts for failed downloads
	skipExists       bool           // Whether to skip existing files
	proxy            string         // Proxy URL (empty string = no proxy)
	totalTracks      int            // Total number of tracks to download
	failedTracksChan chan Track     // Channel to send failed tracks for immediate saving
	saveWg           sync.WaitGroup // WaitGroup for the saving goroutine
}

// NewDownloader creates a new downloader with specified number of workers
func NewDownloader(workers int) *Downloader {
	return &Downloader{
		workers:          workers,
		retryCount:       2,                // Retry failed downloads up to 2 times
		skipExists:       true,             // Skip files that already exist
		proxy:            PROXY_URL,        // Use configured proxy (empty string = no proxy)
		failedTracksChan: make(chan Track), // Initialize the channel
	}
}

// SetProxy sets the proxy URL for downloads (empty string disables proxy)
func (d *Downloader) SetProxy(proxyURL string) {
	d.proxy = proxyURL
}

// Download processes all tracks concurrently and returns failed tracks
func (d *Downloader) Download(tracks []Track, outputDir string) []Track {
	d.totalTracks = len(tracks) // Set total tracks for progress display
	jobs := make(chan Track, len(tracks))
	// No longer need results channel as failures are streamed
	var wg sync.WaitGroup // Use a local waitgroup for workers

	// Start a goroutine to continuously save failed tracks
	d.saveWg.Add(1)
	go d.streamSaveFailedTracks("vk-playlist-failed.txt")

	// Start worker goroutines
	for w := 0; w < d.workers; w++ {
		wg.Add(1)
		go d.worker(jobs, outputDir, &wg)
	}

	// Send all tracks as jobs
	go func() {
		for _, track := range tracks {
			jobs <- track
		}
		close(jobs)
	}()

	// Wait for all workers to finish and close the failed tracks channel
	go func() {
		wg.Wait()
		close(d.failedTracksChan) // Signal that no more failed tracks will be sent
	}()

	// Wait for the failed tracks saving goroutine to finish
	d.saveWg.Wait()

	return nil // Failed tracks are now saved directly, no return needed here
}

// worker processes tracks from jobs channel with retry logic
func (d *Downloader) worker(jobs <-chan Track, outputDir string, wg *sync.WaitGroup) {
	defer wg.Done()

	for track := range jobs {
		var result DownloadResult
		// Try downloading with retries
		for attempt := 0; attempt <= d.retryCount; attempt++ {
			result = d.downloadTrack(track, outputDir)
			if result.Success {
				break
			}
			// Wait between retry attempts with exponential backoff
			if attempt < d.retryCount {
				time.Sleep(time.Second * time.Duration(attempt+1))
			}
		}

		// Update counters and display progress (thread-safe)
		d.mutex.Lock()
		if result.Success {
			if result.Skipped {
				d.skipped++
				fmt.Printf("⏭️  [%d/%d] Already exists: %s\n", d.downloaded+d.skipped+d.failed, d.totalTracks, track.Raw)
			} else {
				d.downloaded++
				fmt.Printf("✅ [%d/%d] Downloaded: %s\n", d.downloaded+d.skipped+d.failed, d.totalTracks, track.Raw)
			}
		} else {
			d.failed++
			reasonStr := ""
			switch result.Reason {
			case NetworkError:
				reasonStr = "Network/proxy error"
			case NotFound:
				reasonStr = "Track not found"
			case UnknownError:
				reasonStr = "Unknown error"
			}
			fmt.Printf("❌ [%d/%d] Failed: %s [%s]\n", d.downloaded+d.skipped+d.failed, d.totalTracks, track.Raw, reasonStr)
			d.failedTracksChan <- track // Send failed track to the channel
		}
		d.mutex.Unlock()
	}
}

// streamSaveFailedTracks continuously writes failed tracks to the specified file
func (d *Downloader) streamSaveFailedTracks(filename string) {
	defer d.saveWg.Done()

	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open failed tracks file for streaming: %v", err)
		return
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush() // Ensure any buffered data is written when done

	for track := range d.failedTracksChan {
		fmt.Fprintln(writer, track.Raw)
		writer.Flush() // Flush after each write for immediate persistence
	}
}

// DownloadResult represents the result of a download attempt
type DownloadResult struct {
	Success bool
	Skipped bool // True if file was skipped due to already existing
	Reason  FailureReason
	Message string
}

// downloadTrack downloads a single track using yt-dlp
func (d *Downloader) downloadTrack(track Track, outputDir string) DownloadResult {
	safeName := sanitizeFilename(fmt.Sprintf("%s - %s", track.Artist, track.Title))

	// Check if file already exists with any extension (excluding .tmp files)
	if d.skipExists {
		// Check for common audio formats
		extensions := []string{".mp3", ".webm", ".m4a", ".ogg", ".opus"}
		for _, ext := range extensions {
			if _, err := os.Stat(filepath.Join(outputDir, safeName+ext)); err == nil {
				return DownloadResult{Success: true, Skipped: true, Message: "File already exists"}
			}
		}
	}

	// Prepare search query and output path template with .tmp extension
	query := fmt.Sprintf("%s %s", track.Artist, track.Title)
	templatePath := filepath.Join(outputDir, safeName+".%(ext)s.tmp")

	// Prepare yt-dlp command arguments (removed --quiet and --no-warnings for detailed output)
	args := []string{
		"--extract-audio",       // Extract audio only
		"--audio-format", "mp3", // Prefer MP3 format
		"--audio-quality", "192K", // Set quality to 192kbps
		"--prefer-ffmpeg",        // Use ffmpeg for conversion if available
		"--output", templatePath, // Output filename template with .tmp extension
		"--no-playlist",        // Download single video only
		"--max-downloads", "1", // Limit to first result
		"--ignore-errors", // Continue on errors
	}

	// Add proxy if configured
	if d.proxy != "" {
		args = append(args, "--proxy", d.proxy)
	}

	// Add search query as final argument
	args = append(args, fmt.Sprintf("ytsearch1:%s", query))

	// Get the appropriate yt-dlp command
	ytDlpCmd := getYtDlpCommand()

	// Combine yt-dlp command with arguments
	allArgs := append(ytDlpCmd[1:], args...)

	// Log download attempt
	fmt.Printf("🔍 Searching: %s\n", query)

	// Execute yt-dlp command and capture output in real-time
	cmd := exec.Command(ytDlpCmd[0], allArgs...)

	// Set up pipes for real-time output monitoring
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return DownloadResult{Success: false, Reason: UnknownError, Message: fmt.Sprintf("Failed to create stdout pipe: %v", err)}
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return DownloadResult{Success: false, Reason: UnknownError, Message: fmt.Sprintf("Failed to create stderr pipe: %v", err)}
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return DownloadResult{Success: false, Reason: UnknownError, Message: fmt.Sprintf("Failed to start command: %v", err)}
	}

	// Monitor stdout for progress updates
	var stdoutBuilder, stderrBuilder strings.Builder
	var foundShown, downloadingShown bool

	// Read stdout in real-time
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			stdoutBuilder.WriteString(line + "\n")

			// Check if track was found and show message immediately
			if !foundShown && (strings.Contains(line, "Downloading") || strings.Contains(line, "Extracting") ||
				strings.Contains(line, "[youtube]") || strings.Contains(line, "has already been downloaded")) {
				fmt.Printf("📁 Found: %s\n", query)
				foundShown = true
			}

			// Show downloading progress only once
			if !downloadingShown && strings.Contains(line, "Downloading") && !strings.Contains(line, "Downloading webpage") {
				fmt.Printf("⬇️  Downloading: %s\n", query)
				downloadingShown = true
			}
		}
	}()

	// Read stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			stderrBuilder.WriteString(line + "\n")

			// Also check stderr for progress messages
			if !foundShown && (strings.Contains(line, "Downloading") || strings.Contains(line, "Extracting") ||
				strings.Contains(line, "[youtube]") || strings.Contains(line, "has already been downloaded")) {
				fmt.Printf("📁 Found: %s\n", query)
				foundShown = true
			}

			if !downloadingShown && strings.Contains(line, "Downloading") && !strings.Contains(line, "Downloading webpage") {
				fmt.Printf("⬇️  Downloading: %s\n", query)
				downloadingShown = true
			}
		}
	}()

	// Wait for command to finish
	cmdErr := cmd.Wait()

	// Check if any temporary audio file was downloaded and rename it
	extensions := []string{".mp3", ".webm", ".m4a", ".ogg", ".opus"}
	for _, ext := range extensions {
		tempPath := filepath.Join(outputDir, safeName+ext+".tmp")
		if _, err := os.Stat(tempPath); err == nil {
			// Rename from .tmp to final extension
			finalPath := filepath.Join(outputDir, safeName+ext)
			if err := os.Rename(tempPath, finalPath); err != nil {
				// If rename fails, remove temp file and return error
				os.Remove(tempPath)
				return DownloadResult{Success: false, Reason: UnknownError, Message: fmt.Sprintf("Failed to rename temp file: %v", err)}
			}
			return DownloadResult{Success: true, Message: "Downloaded successfully"}
		}
	}

	// Analyze failure reason
	errorOutput := stderrBuilder.String()
	reason, message := analyzeFailure(errorOutput, cmdErr)

	return DownloadResult{Success: false, Reason: reason, Message: message}
}

// analyzeFailure analyzes yt-dlp error output to determine failure reason
func analyzeFailure(errorOutput string, cmdErr error) (FailureReason, string) {
	errorLower := strings.ToLower(errorOutput)

	// Check for network-related errors
	networkKeywords := []string{
		"connection", "proxy", "timeout", "network", "dns", "ssl", "tls", "certificate",
		"host", "refused", "unreachable", "blocked", "403", "503", "502", "500",
		"unable to download", "httperror", "urlerror", "no such host",
	}

	for _, keyword := range networkKeywords {
		if strings.Contains(errorLower, keyword) {
			return NetworkError, fmt.Sprintf("Network error: %s", strings.TrimSpace(errorOutput))
		}
	}

	// Check for "not found" errors
	notFoundKeywords := []string{
		"no video", "not found", "no matches", "no results", "unable to find",
		"no suitable", "this video is not available", "video unavailable",
		"private video", "deleted video", "age-restricted",
	}

	for _, keyword := range notFoundKeywords {
		if strings.Contains(errorLower, keyword) {
			return NotFound, fmt.Sprintf("Track not found: %s", strings.TrimSpace(errorOutput))
		}
	}

	// Default to unknown error
	if errorOutput != "" {
		return UnknownError, fmt.Sprintf("Unknown error: %s", strings.TrimSpace(errorOutput))
	}

	return UnknownError, "Unknown error occurred"
}

// sanitizeFilename removes invalid characters from filename and limits length
func sanitizeFilename(name string) string {
	// Characters not allowed in filenames
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\n", "\r", "\t"}
	result := name

	// Replace invalid characters with underscores
	for _, char := range invalid {
		result = strings.ReplaceAll(result, char, "_")
	}

	// Trim whitespace and limit length to 100 characters
	result = strings.TrimSpace(result)
	if utf8.RuneCountInString(result) > 100 {
		runes := []rune(result)
		result = string(runes[:100])
	}

	return result
}

// readPlaylist reads and parses the playlist file, removing duplicates
func readPlaylist(filename string) ([]Track, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var tracks []Track
	seen := make(map[string]bool) // Track duplicates
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and duplicates
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true

		// Parse "Artist - Title" format
		parts := strings.Split(line, " - ")
		if len(parts) < 2 {
			continue // Skip malformed lines
		}

		artist := strings.TrimSpace(parts[0])
		// Handle titles with multiple " - " separators
		title := strings.TrimSpace(strings.Join(parts[1:], " - "))

		tracks = append(tracks, Track{
			Artist: artist,
			Title:  title,
			Raw:    line,
		})
	}

	return tracks, scanner.Err()
}

// saveFailedTracks writes failed tracks to a file
func saveFailedTracks(failed []Track, filename string) error {
	if len(failed) == 0 {
		return nil
	}

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Write each failed track in original format
	for _, track := range failed {
		fmt.Fprintln(writer, track.Raw)
	}

	return nil
}

// getYtDlpCommand returns the appropriate yt-dlp command arguments
// This function handles the common Windows issue where yt-dlp is installed
// via pip but not available in PATH
func getYtDlpCommand() []string {
	// Try direct yt-dlp command first (works if yt-dlp is in PATH)
	cmd := exec.Command("yt-dlp", "--version")
	if cmd.Run() == nil {
		return []string{"yt-dlp"}
	}

	// Use python -m yt_dlp as fallback (works when installed via pip)
	return []string{"python", "-m", "yt_dlp"}
}

// checkYtDlp verifies that yt-dlp is installed and available
func checkYtDlp() bool {
	cmd := getYtDlpCommand()
	testCmd := exec.Command(cmd[0], append(cmd[1:], "--version")...)
	return testCmd.Run() == nil
}

// cleanupTempFiles removes all temporary files (.tmp extension) from output directory
func cleanupTempFiles(outputDir string) {
	pattern := filepath.Join(outputDir, "*.tmp")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		log.Printf("Error finding temp files: %v", err)
		return
	}

	for _, file := range matches {
		if err := os.Remove(file); err != nil {
			log.Printf("Error removing temp file %s: %v", file, err)
		} else {
			fmt.Printf("🗑️  Removed incomplete download: %s\n", filepath.Base(file))
		}
	}

	if len(matches) > 0 {
		fmt.Printf("Cleaned up %d incomplete downloads\n", len(matches))
	}
}

// main function orchestrates the entire download process
func main() {
	// Verify yt-dlp is installed
	if !checkYtDlp() {
		log.Fatal("yt-dlp not found. Please install it: pip install yt-dlp")
	}

	// Create output directory
	outputDir := "music"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatal("Failed to create music directory:", err)
	}

	// Clean up any incomplete downloads from previous runs
	cleanupTempFiles(outputDir)

	// Read and parse playlist
	tracks, err := readPlaylist("vk-playlist.txt")
	if err != nil {
		log.Fatal("Failed to read playlist:", err)
	}

	fmt.Printf("Found %d unique tracks\n", len(tracks))

	// Start concurrent download process
	downloader := NewDownloader(4) // 4 concurrent downloads

	// Display proxy status
	if downloader.proxy != "" {
		fmt.Printf("Using proxy: %s\n", downloader.proxy)
	} else {
		fmt.Printf("Direct connection (no proxy)\n")
	}

	downloader.Download(tracks, outputDir)

	// Display final statistics
	fmt.Printf("\nDownload completed:\n")
	fmt.Printf("✅ Downloaded: %d\n", downloader.downloaded)
	fmt.Printf("⏭️  Skipped (already existed): %d\n", downloader.skipped)
	fmt.Printf("❌ Failed: %d\n", downloader.failed)

	// No longer call saveFailedTracks here, it's streamed
	// if len(failed) > 0 {
	// 	if err := saveFailedTracks(failed, "vk-playlist-failed.txt"); err != nil {
	// 		log.Printf("Failed to save failed tracks: %v", err)
	// 	}
	// }
}
