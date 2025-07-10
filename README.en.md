# Music Downloader

Application for downloading music from a playlist.

## Requirements

1. Go 1.21+
2. yt-dlp (for downloading audio from YouTube)

## Installation

### Installing yt-dlp

```bash
# Windows
pip install yt-dlp

# macOS
brew install yt-dlp

# Linux
pip install yt-dlp
```

### Installing ffmpeg (for MP3 conversion)

**Windows:**
1. Download ffmpeg from https://ffmpeg.org/download.html
2. Extract and add to PATH
3. Or install via chocolatey: `choco install ffmpeg`

**macOS:** `brew install ffmpeg`

**Linux:** `sudo apt install ffmpeg` (Ubuntu/Debian)

**Without ffmpeg:** Files will be downloaded in WebM format (compatible with most players)

**Troubleshooting yt-dlp on Windows:**

If you get "yt-dlp not found" error, the application will automatically try alternative launch methods:
1. First tries to run `yt-dlp` directly
2. If that fails, uses `python -m yt_dlp`

This solves issues when yt-dlp is installed via pip but not available in PATH.

### Running the application

```bash
go run main.go
```

### Building executable

```bash
go build -o music-downloader.exe main.go
./music-downloader.exe
```

## Troubleshooting

### "yt-dlp not found" on Windows
**Symptom:** Error on startup: `yt-dlp not found. Please install it: pip install yt-dlp`

**Solution:** The application now automatically supports different ways to launch yt-dlp:
- If `yt-dlp` is not found in PATH, automatically uses `python -m yt_dlp`
- Make sure Python is installed and accessible from command line

### Files download as WebM instead of MP3
**Symptom:** Files download and play, but in .webm format instead of .mp3

**Solution:** 
1. Install ffmpeg for automatic MP3 conversion
2. WebM files are fully functional and play in most players
3. The application now correctly recognizes WebM as successfully downloaded files

### Proxy issues
**Symptom:** All downloads fail when using proxy

**Solution:** 
1. Make sure the proxy server is running on the specified address
2. Temporarily disable proxy for testing:
   ```go
   PROXY_URL = ""  // Change in main.go
   ```

### Example output

```
Found 1500 unique tracks
Found 25 existing files, skipping them
Using proxy: http://localhost:8881
✅ [1/1475] Downloaded: Michael Nyman - Memorial
✅ [2/1475] Downloaded: Raul Malo - Moonlight Kiss Album Version
❌ [3/1475] Failed: Very rare song - Not found
...

Download completed:
✅ Downloaded: 1200
❌ Failed: 275
📁 Already existed: 25
Failed tracks saved to vk-playlist-failed.txt
```

## Configuration

You can change the following parameters in the code:

- `workers: 4` - number of simultaneous downloads
- `retryCount: 2` - number of download attempts
- `audio-quality: 192K` - audio quality
- `skipExists: true` - skip existing files
- `PROXY_URL` - proxy URL (constant at the beginning of the file)

### Proxy configuration

Find the `PROXY_URL` constant at the beginning of `main.go` and change its value:

```go
// Configuration
const (
	// Set proxy URL here (empty string = no proxy)
	// Examples:
	// PROXY_URL = "http://localhost:8881"    // Use proxy
	// PROXY_URL = ""                         // No proxy (direct connection)
	PROXY_URL = "http://localhost:8881"  // ← Change here
)
```

**Configuration examples:**
- `PROXY_URL = "http://localhost:8881"` - use proxy
- `PROXY_URL = ""` - direct connection (no proxy)
- `PROXY_URL = "http://your-proxy:8080"` - different proxy server

**Quick switching:**
To quickly switch between proxy and direct connection, just change one line:
```go
// With proxy
PROXY_URL = "http://localhost:8881"

// Without proxy
PROXY_URL = ""
```

## Input file format

The `vk-playlist.txt` file should contain lines in the format:
```
Artist - Song Title
```

## How it works

1. Reads the `vk-playlist.txt` file
2. Removes duplicates
3. For each song, performs a search on YouTube
4. Downloads found files to the `music` folder in MP3 format
5. Saves unsuccessful tracks to `vk-playlist-failed.txt`

## Project structure

```
.
├── main.go                    # Main application
├── go.mod                     # Go module
├── test-playlist.txt          # Test playlist
├── vk-playlist.txt            # Input file with list
├── music/                     # Folder for downloaded files
├── vk-playlist-failed.txt     # File with failed downloads
├── music-downloader.exe       # Compiled executable
└── test/                      # Test folder
    ├── test_runner.go         # Test script
    └── test-playlist.txt      # Copy of test playlist
```

## Working principle

1. **Playlist parsing**: Reads `vk-playlist.txt` and removes duplicates
2. **Multithreading**: Launches 4 goroutines for parallel downloads
3. **Proxy**: Automatically uses proxy `http://localhost:8881` (if configured)
4. **Search**: Uses `ytsearch1:` to find the first suitable result on YouTube
5. **Download**: Extracts audio and converts to MP3 via yt-dlp
6. **Retries**: On failure, performs up to 2 additional attempts with delay
7. **Statistics**: Keeps track of successful, failed, and skipped files

## Features

- Automatic duplicate removal from playlist
- Safe file names (replacement of invalid characters)
- File name length limit (100 characters)
- Audio quality 192kbps
- Search only the first result for each song
- **Parallel downloads** (4 threads simultaneously)
- **Automatic retries** on failed downloads (up to 2 attempts)
- **Skip existing files** on restart
- **Statistics tracking** (downloaded/failed/already exists)
- **Improved error handling** with timeouts between attempts
- **Proxy support** (default http://localhost:8881)
- **Automatic yt-dlp detection** (supports `python -m yt_dlp`)
- **Multiple format support** (MP3, WebM, M4A, OGG, Opus)
- **Intelligent success checking** (by file presence, not return code) 