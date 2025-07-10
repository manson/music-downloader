package test

// Note: This file should be run separately from main.go
// Run as: go run test_runner.go

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	// Copy test playlist to vk-playlist.txt
	input, err := os.ReadFile("test-playlist.txt")
	if err != nil {
		log.Fatal("Failed to read test-playlist.txt:", err)
	}

	err = os.WriteFile("../vk-playlist.txt", input, 0644)
	if err != nil {
		log.Fatal("Failed to write vk-playlist.txt:", err)
	}

	fmt.Println("Starting test with 5 popular songs...")
	fmt.Println("Note: Test runs with default proxy settings (http://localhost:8881)")
	fmt.Println("To disable proxy, change PROXY_URL to empty string in main.go")

	// Run the downloader from parent directory
	cmd := exec.Command("go", "run", "../main.go")
	cmd.Dir = ".."
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if err != nil {
		log.Fatal("Failed to run downloader:", err)
	}

	// Check results
	fmt.Println("\nChecking results...")

	files, err := filepath.Glob("../music/*.mp3")
	if err != nil {
		log.Fatal("Failed to check music directory:", err)
	}

	fmt.Printf("Downloaded %d MP3 files:\n", len(files))
	for _, file := range files {
		fmt.Printf("  - %s\n", filepath.Base(file))
	}

	// Clean up
	os.Remove("../vk-playlist.txt")
	fmt.Println("\nTest completed!")
}
