package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/download", downloadHandler)

	log.Printf("Music download server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// GET /health
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "2.0"})
}

// POST /download
// Body: {"title":"...", "artist":"...", "album":"...", "artworkUrl":"..."}
func downloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Title      string `json:"title"`
		Artist     string `json:"artist"`
		Album      string `json:"album"`
		ArtworkURL string `json:"artworkUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Title == "" {
		http.Error(w, `{"error":"need {\"title\":\"...\",\"artist\":\"...\"}"}`, http.StatusBadRequest)
		return
	}

	tmpDir, err := os.MkdirTemp("", "ytdlp-*")
	if err != nil {
		http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	query := fmt.Sprintf("%s %s", req.Title, req.Artist)
	log.Printf("Downloading: %s", query)

	// yt-dlp로 YouTube Music에서 다운로드
	outputTemplate := filepath.Join(tmpDir, "%(title)s.%(ext)s")
	cmd := exec.Command(
		"yt-dlp",
		"--no-playlist",
		"--extract-audio",
		"--audio-format", "m4a",
		"--audio-quality", "0",
		"--output", outputTemplate,
		"--no-progress",
		"--quiet",
		"ytsearch1:"+query+" audio",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	select {
	case err := <-done:
		if err != nil {
			log.Printf("yt-dlp error: %v", err)
			http.Error(w, fmt.Sprintf(`{"error":"download failed: %v"}`, err), http.StatusInternalServerError)
			return
		}
	case <-time.After(5 * time.Minute):
		cmd.Process.Kill()
		http.Error(w, `{"error":"download timeout"}`, http.StatusGatewayTimeout)
		return
	}

	filePath, err := findAudioFile(tmpDir)
	if err != nil || filePath == "" {
		http.Error(w, `{"error":"no audio file found"}`, http.StatusInternalServerError)
		return
	}

	// iTunes 메타데이터를 ffmpeg으로 파일에 임베드
	safeTitle := sanitizeFilename(req.Title)
	safeArtist := sanitizeFilename(req.Artist)
	outputFilename := fmt.Sprintf("%s - %s.m4a", safeTitle, safeArtist)
	outputPath := filepath.Join(tmpDir, "tagged_"+outputFilename)

	ffmpegArgs := []string{
		"-i", filePath,
		"-c", "copy",
		"-metadata", "title=" + req.Title,
		"-metadata", "artist=" + req.Artist,
		"-metadata", "album=" + req.Album,
	}

	// 아트워크 다운로드 후 임베드
	if req.ArtworkURL != "" {
		artPath := filepath.Join(tmpDir, "artwork.jpg")
		if err := downloadFile(req.ArtworkURL, artPath); err == nil {
			ffmpegArgs = append(ffmpegArgs,
				"-i", artPath,
				"-map", "0:a",
				"-map", "1:v",
				"-disposition:v", "attached_pic",
			)
		}
	}

	ffmpegArgs = append(ffmpegArgs, "-y", outputPath)
	ffCmd := exec.Command("ffmpeg", ffmpegArgs...)
	var ffErr bytes.Buffer
	ffCmd.Stderr = &ffErr

	if err := ffCmd.Run(); err != nil {
		log.Printf("ffmpeg error: %v — %s", err, ffErr.String())
		// ffmpeg 실패 시 원본 파일 그대로 서빙
		outputPath = filePath
		outputFilename = filepath.Base(filePath)
	}

	file, err := os.Open(outputPath)
	if err != nil {
		http.Error(w, `{"error":"failed to read file"}`, http.StatusInternalServerError)
		return
	}
	defer file.Close()

	stat, _ := file.Stat()
	ext := strings.ToLower(filepath.Ext(outputFilename))
	contentType := map[string]string{
		".m4a":  "audio/mp4",
		".mp3":  "audio/mpeg",
		".flac": "audio/flac",
		".ogg":  "audio/ogg",
	}[ext]
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, outputFilename))
	w.Header().Set("X-Filename", outputFilename)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))

	io.Copy(w, file)
	log.Printf("Served: %s (%d bytes)", outputFilename, stat.Size())
}

func findAudioFile(dir string) (string, error) {
	audioExts := map[string]bool{".m4a": true, ".mp3": true, ".flac": true, ".ogg": true, ".webm": true, ".opus": true}
	var found string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return err
		}
		if audioExts[strings.ToLower(filepath.Ext(path))] && !strings.HasPrefix(filepath.Base(path), "tagged_") {
			found = path
		}
		return nil
	})
	return found, err
}

func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer("/", "-", ":", "-", "\\", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "-")
	return strings.TrimSpace(replacer.Replace(s))
}

func downloadFile(rawURL, dest string) error {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("invalid URL")
	}
	resp, err := http.Get(rawURL) // #nosec G107 — URL validated above
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
