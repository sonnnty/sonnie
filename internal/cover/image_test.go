package cover

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checkCover(t *testing.T, out []byte) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" || cfg.Width != Width || cfg.Height != Height {
		t.Fatalf("got %s %d×%d, want jpeg %d×%d", format, cfg.Width, cfg.Height, Width, Height)
	}
}

func TestProcess_OutputIs800x320JPEG(t *testing.T) {
	webp, err := os.ReadFile("testdata/sample.webp")
	if err != nil {
		t.Fatal(err)
	}
	inputs := map[string][]byte{
		"wide png":   pngOf(t, 1600, 900),
		"tall png":   pngOf(t, 300, 900),
		"small jpeg": jpegOf(t, 200, 100), // upscaled
		"exact png":  pngOf(t, 800, 320),
		"webp":       webp,
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			out, err := Process(in)
			if err != nil {
				t.Fatal(err)
			}
			checkCover(t, out)
		})
	}
}

func TestProcess_RejectsOtherFormats(t *testing.T) {
	var buf bytes.Buffer
	if err := gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 10, 10), []color.Color{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]byte{"gif": buf.Bytes(), "text": []byte("hello"), "empty": nil} {
		if _, err := Process(in); !errors.Is(err, ErrNotImage) {
			t.Errorf("%s: error = %v, want ErrNotImage", name, err)
		}
	}
}

func TestCropRect_Centred5x2(t *testing.T) {
	tests := []struct {
		in, want image.Rectangle
	}{
		{image.Rect(0, 0, 1000, 1000), image.Rect(0, 300, 1000, 700)},
		{image.Rect(0, 0, 2000, 400), image.Rect(500, 0, 1500, 400)},
		{image.Rect(0, 0, 800, 320), image.Rect(0, 0, 800, 320)},
		{image.Rect(10, 10, 510, 210), image.Rect(10, 10, 510, 210)},
	}
	for _, tt := range tests {
		if got := CropRect(tt.in); got != tt.want {
			t.Errorf("CropRect(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestDataURI(t *testing.T) {
	got := DataURI([]byte{1, 2, 3})
	if !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Fatalf("got %q", got)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, "data:image/jpeg;base64,"))
	if err != nil || !bytes.Equal(raw, []byte{1, 2, 3}) {
		t.Fatalf("round trip: %v %v", raw, err)
	}
}

func TestDownload(t *testing.T) {
	img := pngOf(t, 40, 16)
	big := append(append([]byte{}, img...), make([]byte, MaxDownloadBytes)...)
	mux := http.NewServeMux()
	serve := func(path, ct string, body []byte) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Write(body)
		})
	}
	serve("/ok.png", "image/png", img)
	serve("/octet", "application/octet-stream", img)
	serve("/html", "text/html", []byte("<html></html>"))
	serve("/lying", "image/png", []byte("<html>not an image</html>"))
	serve("/big", "image/png", big)
	mux.HandleFunc("/chunked-big", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		// Without Content-Length the cap is enforced while reading.
		w.(http.Flusher).Flush()
		w.Write(big)
	})
	mux.HandleFunc("/missing", http.NotFound)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	for _, p := range []string{"/ok.png", "/octet"} {
		data, err := Download(ctx, srv.Client(), srv.URL+p)
		if err != nil || !bytes.Equal(data, img) {
			t.Errorf("%s: %v", p, err)
		}
	}
	for p, want := range map[string]error{"/html": ErrNotImage, "/lying": ErrNotImage, "/big": ErrTooLarge, "/chunked-big": ErrTooLarge} {
		if _, err := Download(ctx, srv.Client(), srv.URL+p); !errors.Is(err, want) {
			t.Errorf("%s: error = %v, want %v", p, err, want)
		}
	}
	if _, err := Download(ctx, srv.Client(), srv.URL+"/missing"); err == nil {
		t.Error("/missing: want an error")
	}
}
