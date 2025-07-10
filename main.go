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
	workers     int          // Number of concurrent workers
	downloaded  int          // Count of successfully downloaded tracks
	skipped     int          // Count of skipped tracks (already exist)
	failed      int          // Count of failed downloads
	mutex       sync.RWMutex // Mutex for thread-safe counter updates
	retryCount  int          // Number of retry attempts for failed downloads
	skipExists  bool         // Whether to skip existing files
	proxy       string       // Proxy URL (empty string means no proxy)
	totalTracks int          // Total number of tracks to download
}

// NewDownloader creates a new downloader with specified number of workers
func NewDownloader(workers int) *Downloader {
	return &Downloader{
		workers:    workers,
		retryCount: 2,         // Retry failed downloads up to 2 times
		skipExists: true,      // Skip files that already exist
		proxy:      PROXY_URL, // Use configured proxy (empty string = no proxy)
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
	results := make(chan Track, len(tracks))
	var wg sync.WaitGroup

	// Start worker goroutines
	for w := 0; w < d.workers; w++ {
		wg.Add(1)
		go d.worker(jobs, results, outputDir, &wg)
	}

	// Send all tracks as jobs
	go func() {
		for _, track := range tracks {
			jobs <- track
		}
		close(jobs)
	}()

	// Wait for all workers to finish and close results channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect failed tracks from results channel
	var failed []Track
	for result := range results {
		if result.Raw != "" {
			failed = append(failed, result)
		}
	}

	return failed
}

// worker processes tracks from jobs channel with retry logic
func (d *Downloader) worker(jobs <-chan Track, results chan<- Track, outputDir string, wg *sync.WaitGroup) {
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
			results <- track // Send failed track to results channel
		}
		d.mutex.Unlock()
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

	// Check if file already exists with any extension
	if d.skipExists {
		// Check for common audio formats
		extensions := []string{".mp3", ".webm", ".m4a", ".ogg", ".opus"}
		for _, ext := range extensions {
			if _, err := os.Stat(filepath.Join(outputDir, safeName+ext)); err == nil {
				return DownloadResult{Success: true, Skipped: true, Message: "File already exists"}
			}
		}
	}

	// Prepare search query and output path template
	query := fmt.Sprintf("%s %s", track.Artist, track.Title)
	templatePath := filepath.Join(outputDir, safeName+".%(ext)s")

	// Prepare yt-dlp command arguments (removed --quiet and --no-warnings for detailed output)
	args := []string{
		"--extract-audio",       // Extract audio only
		"--audio-format", "mp3", // Prefer MP3 format
		"--audio-quality", "192K", // Set quality to 192kbps
		"--prefer-ffmpeg",        // Use ffmpeg for conversion if available
		"--output", templatePath, // Output filename template
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

	// Execute yt-dlp command and capture output
	cmd := exec.Command(ytDlpCmd[0], allArgs...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	cmdErr := cmd.Run()

	// Check if any audio file was downloaded
	extensions := []string{".mp3", ".webm", ".m4a", ".ogg", ".opus"}
	for _, ext := range extensions {
		if _, err := os.Stat(filepath.Join(outputDir, safeName+ext)); err == nil {
			return DownloadResult{Success: true, Message: "Downloaded successfully"}
		}
	}

	// Analyze failure reason
	errorOutput := stderr.String()
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

	failed := downloader.Download(tracks, outputDir)

	// Display final statistics
	fmt.Printf("\nDownload completed:\n")
	fmt.Printf("✅ Downloaded: %d\n", downloader.downloaded)
	fmt.Printf("⏭️  Skipped (already existed): %d\n", downloader.skipped)
	fmt.Printf("❌ Failed: %d\n", downloader.failed)

	// Save failed tracks for later retry
	if len(failed) > 0 {
		if err := saveFailedTracks(failed, "vk-playlist-failed.txt"); err != nil {
			log.Printf("Failed to save failed tracks: %v", err)
		} else {
			fmt.Printf("Failed tracks saved to vk-playlist-failed.txt\n")
		}
	}
}
