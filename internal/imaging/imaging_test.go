package imaging_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/imaging"
)

var capturedAt = time.Date(2026, time.April, 5, 6, 7, 8, 0, time.FixedZone("UTC+8", 8*60*60))

func TestEncodeDownscalesAndMetadata(t *testing.T) {
	const sourceW, sourceH = 1080, 2400
	source := device.ImageSource{
		Data:       pngBytes(t, sourceW, sourceH),
		CapturedAt: capturedAt,
	}
	display := device.Display{Width: sourceW, Height: sourceH}

	meta, data, err := imaging.Encode(source, display, imaging.Policy{})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Encode returned no JPEG bytes")
	}
	if len(data) > 512*1024 {
		t.Fatalf("default byte budget exceeded: %d bytes", len(data))
	}
	if meta == nil {
		t.Fatal("Encode returned nil metadata")
	}
	if meta.Width != 461 || meta.Height != 1024 {
		t.Fatalf("metadata size = %dx%d, want 461x1024", meta.Width, meta.Height)
	}
	if meta.MediaType != imaging.MediaTypeJPEG {
		t.Fatalf("media type = %q, want %q", meta.MediaType, imaging.MediaTypeJPEG)
	}
	if meta.Base64 != "" {
		t.Fatalf("metadata carries base64: %q", meta.Base64)
	}
	if meta.Screen != nil {
		t.Fatal("metadata carries a screen")
	}
	if meta.Error != nil {
		t.Fatal("metadata carries an error")
	}
	if meta.ReadOnly {
		t.Fatal("metadata marked read-only")
	}
	wantCapturedAt := capturedAt.UTC().Format(time.RFC3339)
	if meta.CapturedAt != wantCapturedAt {
		t.Fatalf("captured_at = %q, want %q", meta.CapturedAt, wantCapturedAt)
	}
	parsed, err := time.Parse(time.RFC3339, meta.CapturedAt)
	if err != nil {
		t.Fatalf("parse captured_at: %v", err)
	}
	if parsed.Location() != time.UTC {
		t.Fatalf("captured_at location = %v, want UTC", parsed.Location())
	}
	if !parsed.Equal(capturedAt) {
		t.Fatalf("captured_at instant = %v, want %v", parsed, capturedAt)
	}
	if got := decodeJPEG(t, data).Bounds(); got.Dx() != 461 || got.Dy() != 1024 {
		t.Fatalf("decoded JPEG = %dx%d, want 461x1024", got.Dx(), got.Dy())
	}
}

// TestEncodeDefaultLongSideIs1024 pins the documented default long-side budget:
// a 1080x2400 portrait screen becomes 461x1024, the landscape mirror becomes
// 1024x461, and a source already inside the budget is never upscaled.
func TestEncodeDefaultLongSideIs1024(t *testing.T) {
	tests := []struct {
		name             string
		sourceW, sourceH int
		wantW, wantH     int
	}{
		{name: "portrait phone", sourceW: 1080, sourceH: 2400, wantW: 461, wantH: 1024},
		{name: "landscape phone", sourceW: 2400, sourceH: 1080, wantW: 1024, wantH: 461},
		{name: "below budget is never upscaled", sourceW: 800, sourceH: 600, wantW: 800, wantH: 600},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := device.ImageSource{
				Data:       pngBytes(t, tc.sourceW, tc.sourceH),
				CapturedAt: capturedAt,
			}
			display := device.Display{Width: tc.sourceW, Height: tc.sourceH}
			meta, data, err := imaging.Encode(source, display, imaging.Policy{})
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if meta.Width != tc.wantW || meta.Height != tc.wantH {
				t.Fatalf("metadata size = %dx%d, want %dx%d", meta.Width, meta.Height, tc.wantW, tc.wantH)
			}
			if got := decodeJPEG(t, data).Bounds(); got.Dx() != tc.wantW || got.Dy() != tc.wantH {
				t.Fatalf("decoded JPEG = %dx%d, want %dx%d", got.Dx(), got.Dy(), tc.wantW, tc.wantH)
			}
			if longest := max(meta.Width, meta.Height); longest > 1024 {
				t.Fatalf("longest edge = %d, want <= 1024", longest)
			}
		})
	}
}

func TestEncodeNeverUpscales(t *testing.T) {
	const sourceW, sourceH = 100, 200
	source := device.ImageSource{
		Data:       pngBytes(t, sourceW, sourceH),
		CapturedAt: capturedAt,
	}
	meta, data, err := imaging.Encode(source, device.Display{Width: sourceW, Height: sourceH}, imaging.Policy{MaxLongSide: 1024})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if meta.Width != sourceW || meta.Height != sourceH {
		t.Fatalf("metadata size = %dx%d, want %dx%d", meta.Width, meta.Height, sourceW, sourceH)
	}
	if got := decodeJPEG(t, data).Bounds(); got.Dx() != sourceW || got.Dy() != sourceH {
		t.Fatalf("decoded JPEG = %dx%d, want %dx%d", got.Dx(), got.Dy(), sourceW, sourceH)
	}
}

func TestEncodeBoundsExtremeAspectRatios(t *testing.T) {
	tests := []struct {
		name             string
		sourceW, sourceH int
		maxLongSide      int
		wantW, wantH     int
	}{
		{name: "wide", sourceW: 1000, sourceH: 2, maxLongSide: 3, wantW: 3, wantH: 1},
		{name: "tall", sourceW: 2, sourceH: 1000, maxLongSide: 3, wantW: 1, wantH: 3},
		{name: "very wide", sourceW: 16000, sourceH: 1, maxLongSide: 7, wantW: 7, wantH: 1},
		{name: "very tall", sourceW: 1, sourceH: 16000, maxLongSide: 7, wantW: 1, wantH: 7},
		{name: "single pixel bound", sourceW: 100, sourceH: 200, maxLongSide: 1, wantW: 1, wantH: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := device.ImageSource{
				Data:       pngBytes(t, tc.sourceW, tc.sourceH),
				CapturedAt: capturedAt,
			}
			display := device.Display{Width: tc.sourceW, Height: tc.sourceH}
			meta, data, err := imaging.Encode(source, display, imaging.Policy{MaxLongSide: tc.maxLongSide})
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if meta.Width != tc.wantW || meta.Height != tc.wantH {
				t.Fatalf("metadata size = %dx%d, want %dx%d", meta.Width, meta.Height, tc.wantW, tc.wantH)
			}
			if meta.Width < 1 || meta.Height < 1 {
				t.Fatalf("output has a zero edge: %dx%d", meta.Width, meta.Height)
			}
			if longest := max(meta.Width, meta.Height); longest > tc.maxLongSide {
				t.Fatalf("longest edge = %d, want <= %d", longest, tc.maxLongSide)
			}
			if got := decodeJPEG(t, data).Bounds(); got.Dx() != meta.Width || got.Dy() != meta.Height {
				t.Fatalf("decoded JPEG = %dx%d, want %dx%d", got.Dx(), got.Dy(), meta.Width, meta.Height)
			}
		})
	}
}

func TestEncodeRejectsGeometryMismatch(t *testing.T) {
	tests := []struct {
		name               string
		sourceW, sourceH   int
		displayW, displayH int
	}{
		{name: "source smaller than display", sourceW: 100, sourceH: 100, displayW: 1080, displayH: 2400},
		{name: "source larger than display", sourceW: 100, sourceH: 200, displayW: 50, displayH: 100},
		{name: "swapped orientation", sourceW: 100, sourceH: 200, displayW: 200, displayH: 100},
		{name: "zero display width", sourceW: 100, sourceH: 100, displayW: 0, displayH: 100},
		{name: "negative display height", sourceW: 100, sourceH: 100, displayW: 100, displayH: -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := device.ImageSource{
				Data:       pngBytes(t, tc.sourceW, tc.sourceH),
				CapturedAt: capturedAt,
			}
			display := device.Display{Width: tc.displayW, Height: tc.displayH}
			meta, data, err := imaging.Encode(source, display, imaging.Policy{})
			if !errors.Is(err, imaging.ErrGeometry) {
				t.Fatalf("Encode = (%v, %d bytes, %v), want ErrGeometry", meta, len(data), err)
			}
			if meta != nil || data != nil {
				t.Fatalf("Encode returned meta=%v data=%d bytes with ErrGeometry", meta, len(data))
			}
		})
	}
}

func TestEncodeRejectsInvalidSource(t *testing.T) {
	valid := pngBytes(t, 100, 200)
	truncated := append([]byte(nil), valid[:len(valid)/2]...)
	tests := []struct {
		name  string
		data  []byte
		stamp time.Time
	}{
		{name: "empty data", data: nil, stamp: capturedAt},
		{name: "garbage bytes", data: []byte("this is definitely not a png"), stamp: capturedAt},
		{name: "truncated png", data: truncated, stamp: capturedAt},
		{name: "zero captured time", data: valid, stamp: time.Time{}},
	}
	display := device.Display{Width: 100, Height: 200}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := device.ImageSource{Data: tc.data, CapturedAt: tc.stamp}
			meta, data, err := imaging.Encode(source, display, imaging.Policy{})
			if !errors.Is(err, imaging.ErrInvalidSource) {
				t.Fatalf("Encode = (%v, %d bytes, %v), want ErrInvalidSource", meta, len(data), err)
			}
			if meta != nil || data != nil {
				t.Fatalf("Encode returned meta=%v data=%d bytes with ErrInvalidSource", meta, len(data))
			}
		})
	}
}

func TestEncodeRejectsOversizedSources(t *testing.T) {
	t.Run("tiny byte budget", func(t *testing.T) {
		source := device.ImageSource{
			Data:       pngBytes(t, 100, 100),
			CapturedAt: capturedAt,
		}
		display := device.Display{Width: 100, Height: 100}
		meta, data, err := imaging.Encode(source, display, imaging.Policy{MaxBytes: 10})
		if !errors.Is(err, imaging.ErrTooLarge) {
			t.Fatalf("Encode = (%v, %d bytes, %v), want ErrTooLarge", meta, len(data), err)
		}
		if meta != nil || data != nil {
			t.Fatalf("Encode returned meta=%v data=%d bytes with ErrTooLarge", meta, len(data))
		}
	})

	t.Run("too many source pixels", func(t *testing.T) {
		source := device.ImageSource{
			Data:       pngHeader(5000, 4000),
			CapturedAt: capturedAt,
		}
		display := device.Display{Width: 5000, Height: 4000}
		meta, data, err := imaging.Encode(source, display, imaging.Policy{})
		if !errors.Is(err, imaging.ErrTooLarge) {
			t.Fatalf("Encode = (%v, %d bytes, %v), want ErrTooLarge", meta, len(data), err)
		}
	})

	t.Run("pixel count exactly at limit decodes", func(t *testing.T) {
		source := device.ImageSource{
			Data:       pngHeader(4000, 4000),
			CapturedAt: capturedAt,
		}
		display := device.Display{Width: 4000, Height: 4000}
		meta, data, err := imaging.Encode(source, display, imaging.Policy{})
		if !errors.Is(err, imaging.ErrInvalidSource) {
			t.Fatalf("Encode = (%v, %d bytes, %v), want ErrInvalidSource", meta, len(data), err)
		}
	})

	t.Run("source bytes above limit", func(t *testing.T) {
		source := device.ImageSource{
			Data:       make([]byte, (24<<20)+1),
			CapturedAt: capturedAt,
		}
		display := device.Display{Width: 100, Height: 100}
		meta, data, err := imaging.Encode(source, display, imaging.Policy{})
		if !errors.Is(err, imaging.ErrTooLarge) {
			t.Fatalf("Encode = (%v, %d bytes, %v), want ErrTooLarge", meta, len(data), err)
		}
	})
}

func TestPolicyDefaultsAndClamping(t *testing.T) {
	const sourceW, sourceH = 64, 64
	source := device.ImageSource{
		Data:       pngBytes(t, sourceW, sourceH),
		CapturedAt: capturedAt,
	}
	display := device.Display{Width: sourceW, Height: sourceH}

	tests := []struct {
		name   string
		policy imaging.Policy
	}{
		{name: "zero values use defaults", policy: imaging.Policy{}},
		{name: "high quality clamps", policy: imaging.Policy{Quality: 1000}},
		{name: "minimum quality", policy: imaging.Policy{Quality: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta, data, err := imaging.Encode(source, display, tc.policy)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if meta == nil || meta.Width != sourceW || meta.Height != sourceH {
				t.Fatalf("metadata = %+v, want %dx%d", meta, sourceW, sourceH)
			}
			if got := decodeJPEG(t, data).Bounds(); got.Dx() != sourceW || got.Dy() != sourceH {
				t.Fatalf("decoded JPEG = %dx%d, want %dx%d", got.Dx(), got.Dy(), sourceW, sourceH)
			}
		})
	}

	t.Run("quality default and clamp match explicit values", func(t *testing.T) {
		encode := func(t *testing.T, policy imaging.Policy) []byte {
			t.Helper()
			_, data, err := imaging.Encode(source, display, policy)
			if err != nil {
				t.Fatalf("Encode(%+v): %v", policy, err)
			}
			return data
		}
		if got, want := encode(t, imaging.Policy{}), encode(t, imaging.Policy{Quality: 80}); !bytes.Equal(got, want) {
			t.Fatal("zero quality did not use the default quality 80")
		}
		if got, want := encode(t, imaging.Policy{Quality: 1000}), encode(t, imaging.Policy{Quality: 100}); !bytes.Equal(got, want) {
			t.Fatal("quality 1000 did not clamp to 100")
		}
	})

	negative := []struct {
		name   string
		policy imaging.Policy
	}{
		{name: "negative max long side", policy: imaging.Policy{MaxLongSide: -1}},
		{name: "negative quality", policy: imaging.Policy{Quality: -1}},
		{name: "negative max bytes", policy: imaging.Policy{MaxBytes: -1}},
	}
	for _, tc := range negative {
		t.Run(tc.name, func(t *testing.T) {
			meta, data, err := imaging.Encode(source, display, tc.policy)
			if err == nil {
				t.Fatalf("Encode = (%v, %d bytes, nil), want an error", meta, len(data))
			}
		})
	}
}

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	if width <= 0 || height <= 0 {
		t.Fatalf("invalid test image size %dx%d", width, height)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := img.PixOffset(x, y)
			img.Pix[offset+0] = ramp(x, width)
			img.Pix[offset+1] = ramp(y, height)
			img.Pix[offset+2] = 96
			img.Pix[offset+3] = 0xff
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

func ramp(value, span int) uint8 {
	if span <= 1 {
		return 0
	}
	return uint8(value * 255 / (span - 1))
}

func decodeJPEG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode JPEG output: %v", err)
	}
	return img
}

// pngHeader builds a valid PNG signature and IHDR without any image data. It is
// enough for DecodeConfig and deliberately cannot be fully decoded.
func pngHeader(width, height uint32) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 6 // color type: truecolour with alpha
	writePNGChunk(&buf, "IHDR", ihdr)
	return buf.Bytes()
}

func writePNGChunk(buf *bytes.Buffer, chunkType string, payload []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	buf.Write(length[:])
	buf.WriteString(chunkType)
	buf.Write(payload)
	crc := crc32.NewIEEE()
	crc.Write([]byte(chunkType))
	crc.Write(payload)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	buf.Write(sum[:])
}
