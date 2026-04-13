package main

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Config struct {
	S3Bucket    string
	S3Region    string
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	ListenAddr  string
	SiteURL     string
	SiteTitle   string
}

func loadConfig() Config {
	cfg := Config{
		S3Bucket:    getEnv("S3_BUCKET", ""),
		S3Region:    getEnv("S3_REGION", "us-east-1"),
		S3Endpoint:  getEnv("S3_ENDPOINT", ""),
		S3AccessKey: getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey: getEnv("S3_SECRET_KEY", ""),
		ListenAddr:  getEnv("LISTEN_ADDR", ":8080"),
		SiteURL:     getEnv("SITE_URL", "http://localhost:8080"),
		SiteTitle:   getEnv("SITE_TITLE", "S3-Viewer"),
	}
	if cfg.S3Bucket == "" {
		slog.Error("S3_BUCKET environment variable is required")
		os.Exit(1)
	}
	if !strings.HasPrefix(cfg.SiteURL, "http://") && !strings.HasPrefix(cfg.SiteURL, "https://") {
		slog.Error("SITE_URL must start with http:// or https://", "value", cfg.SiteURL)
		os.Exit(1)
	}
	cfg.SiteURL = strings.TrimRight(cfg.SiteURL, "/")
	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func formatSize(b int64) string {
	if b == 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	exp := min(int(math.Log(float64(b))/math.Log(1024)), len(units)-1)
	if exp == 0 {
		return fmt.Sprintf("%d B", b)
	}
	return fmt.Sprintf("%.1f %s", float64(b)/math.Pow(1024, float64(exp)), units[exp])
}

// inlineTypes are served as text/plain so browsers render them inline.
var inlineTypes = map[string]bool{
	"application/json":       true,
	"application/javascript": true,
	"application/xml":        true,
}

func servedMime(mimeType string) string {
	if strings.HasPrefix(mimeType, "text/") || inlineTypes[mimeType] {
		return "text/plain; charset=utf-8"
	}
	return mimeType
}

func safeName(raw string) (string, bool) {
	name := filepath.ToSlash(raw)
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return "", false
	}
	return name, true
}

type App struct {
	cfg  Config
	s3   *s3.Client
	tmpl *template.Template
}

func newApp(cfg Config) (*App, error) {
	awsOpts := []func(*config.LoadOptions) error{config.WithRegion(cfg.S3Region)}
	if cfg.S3AccessKey != "" && cfg.S3SecretKey != "" {
		awsOpts = append(awsOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		))
	}
	awsCfg, err := config.LoadDefaultConfig(context.Background(), awsOpts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	s3Opts := []func(*s3.Options){}
	if cfg.S3Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.S3Endpoint)
			o.UsePathStyle = true
		})
	}

	tmpl, err := template.New("watch").Funcs(template.FuncMap{
		"formatSize": formatSize,
		"hasPrefix":  strings.HasPrefix,
	}).Parse(watchTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	return &App{cfg: cfg, s3: s3.NewFromConfig(awsCfg, s3Opts...), tmpl: tmpl}, nil
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleWatch)
	mux.HandleFunc("/e/", a.handleEmbed)
	return mux
}

type WatchData struct {
	Name, Title, SiteTitle, VideoURL, PageURL, MimeType, Description string
	Size                                                              int64
}

func (a *App) handleWatch(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/")
	name, ok := safeName(raw)
	if !ok {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}

	head, err := a.s3.HeadObject(r.Context(), &s3.HeadObjectInput{
		Bucket: aws.String(a.cfg.S3Bucket),
		Key:    aws.String(name),
	})
	if err != nil {
		slog.Error("HeadObject", "key", name, "err", err)
		http.NotFound(w, r)
		return
	}

	title := head.Metadata["title"]
	if title == "" {
		title = strings.TrimSuffix(name, filepath.Ext(name))
	}

	mimeType := mime.TypeByExtension(filepath.Ext(name))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	size := int64(0)
	if head.ContentLength != nil {
		size = *head.ContentLength
	}

	data := WatchData{
		Name:        name,
		Title:       title,
		SiteTitle:   a.cfg.SiteTitle,
		VideoURL:    a.cfg.SiteURL + "/e/" + name,
		PageURL:     a.cfg.SiteURL + "/watch/" + name,
		MimeType:    mimeType,
		Size:        size,
		Description: head.Metadata["description"],
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.Execute(w, data); err != nil {
		slog.Error("template", "err", err)
	}
}

func (a *App) handleEmbed(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/e/")
	name, ok := safeName(raw)
	if !ok {
		http.NotFound(w, r)
		return
	}

	input := &s3.GetObjectInput{
		Bucket: aws.String(a.cfg.S3Bucket),
		Key:    aws.String(name),
	}
	if rng := r.Header.Get("Range"); rng != "" {
		input.Range = aws.String(rng)
	}

	result, err := a.s3.GetObject(r.Context(), input)
	if err != nil {
		slog.Error("GetObject", "key", name, "err", err)
		http.NotFound(w, r)
		return
	}
	defer result.Body.Close()


	mimeType := mime.TypeByExtension(filepath.Ext(name))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", servedMime(mimeType))
	w.Header().Set("Accept-Ranges", "bytes")
	if result.ContentLength != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", *result.ContentLength))
	}
	if result.ContentRange != nil {
		w.Header().Set("Content-Range", *result.ContentRange)
		w.WriteHeader(http.StatusPartialContent)
	}

	const maxStream = 500 << 20 // 500 MB cap for non-range requests
	reader := io.LimitReader(result.Body, maxStream)
	if _, err := io.Copy(w, reader); err != nil {
		slog.Error("stream error", "key", name, "err", err)
	}
}

func main() {
	cfg := loadConfig()
	app, err := newApp(cfg)
	if err != nil {
		slog.Error("init", "err", err)
		os.Exit(1)
	}
	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      app.routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	slog.Info("server started", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
