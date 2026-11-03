// Package cover finds a cover picture for a Discord scheduled event on free
// image libraries and turns it into the 800×320 JPEG Discord shows.
package cover

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // registered for image.Decode
	"io"
	"mime"
	"net/http"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registered for image.Decode
)

// Discord shows event covers at 800×320 (5:2).
const (
	Width  = 800
	Height = 320
)

const (
	// MaxDownloadBytes caps a downloaded or attached image.
	MaxDownloadBytes = 10 << 20
	// maxPixels refuses images that would take too much memory to decode.
	maxPixels       = 50_000_000
	jpegQuality     = 85
	downloadTimeout = 15 * time.Second
	userAgent       = "SONNIE-discord-bot/1.0 (+https://github.com/Sonty15/SONNIE)"
)

var (
	ErrTooLarge = errors.New("cover: image is larger than 10 MB")
	ErrNotImage = errors.New("cover: not a JPEG, PNG or WebP image")
)

var allowedTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

// Download fetches an image of at most MaxDownloadBytes. Both the declared
// Content-Type and the bytes themselves must be JPEG, PNG or WebP.
func Download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: downloadTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cover: download: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cover: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover: download: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxDownloadBytes {
		return nil, ErrTooLarge
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		mt, _, _ := mime.ParseMediaType(ct)
		// Some image hosts label everything octet-stream; the bytes decide then.
		if !allowedTypes[mt] && mt != "application/octet-stream" {
			return nil, ErrNotImage
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cover: download: %w", err)
	}
	if len(data) > MaxDownloadBytes {
		return nil, ErrTooLarge
	}
	if !allowedTypes[http.DetectContentType(data)] {
		return nil, ErrNotImage
	}
	return data, nil
}

// Process decodes a JPEG, PNG or WebP image, crops its centre to 5:2 and
// scales it to Width×Height, and returns it as a JPEG.
func Process(data []byte) ([]byte, error) {
	if !allowedTypes[http.DetectContentType(data)] {
		return nil, ErrNotImage
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cover: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("cover: image is %d×%d pixels, too large to process", cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cover: %w", err)
	}
	dst := image.NewRGBA(image.Rect(0, 0, Width, Height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, CropRect(src.Bounds()), draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("cover: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// CropRect is the largest centred 5:2 rectangle inside b.
func CropRect(b image.Rectangle) image.Rectangle {
	w, h := b.Dx(), b.Dy()
	cw, ch := w, w*Height/Width
	if ch > h {
		cw, ch = h*Width/Height, h
	}
	x := b.Min.X + (w-cw)/2
	y := b.Min.Y + (h-ch)/2
	return image.Rect(x, y, x+cw, y+ch)
}

// DataURI is how Discord takes an event's image.
func DataURI(jpegData []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegData)
}
