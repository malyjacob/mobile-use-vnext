// Package imaging provides pure image post-processing with no device I/O and
// no publication.
package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// MediaTypeJPEG is the media type carried with every encoded image.
const MediaTypeJPEG = "image/jpeg"

var (
	// ErrInvalidSource reports that the source bytes are not one decodable PNG
	// or that the source timestamp is missing.
	ErrInvalidSource = errors.New("image source is not a valid PNG")
	// ErrGeometry reports that the decoded source does not match the target
	// display geometry.
	ErrGeometry = errors.New("image source does not match the target display geometry")
	// ErrTooLarge reports that the source or the encoded output exceeds its
	// budget.
	ErrTooLarge = errors.New("image exceeds the output budget")
)

const (
	defaultMaxLongSide = 1024
	defaultQuality     = 80
	defaultMaxBytes    = 512 * 1024
	maxSourceBytes     = 24 << 20
	maxSourcePixels    = 16_000_000
)

// Policy bounds the single JPEG output produced by Encode. A zero field selects
// that field's default.
type Policy struct {
	MaxLongSide int // longest output edge in pixels
	Quality     int // JPEG quality 1..100
	MaxBytes    int // encoded byte budget
}

// Encode turns one device image source into the single bounded JPEG output.
// It validates the source geometry against display (same orientation, exact
// pixel size), scales proportionally down exactly once (never upscales), and
// encodes one JPEG. The returned metadata has CapturedAt from source (UTC,
// RFC3339), the actual output width/height, MediaType=MediaTypeJPEG and no
// Base64 (the caller owns the transport carrier). Rotation is not applied: the
// source must already be in the display orientation.
func Encode(source device.ImageSource, display device.Display, policy Policy) (*protocol.ImageMeta, []byte, error) {
	policy, err := normalizePolicy(policy)
	if err != nil {
		return nil, nil, err
	}
	if len(source.Data) == 0 {
		return nil, nil, ErrInvalidSource
	}
	if len(source.Data) > maxSourceBytes {
		return nil, nil, ErrTooLarge
	}
	if source.CapturedAt.IsZero() {
		return nil, nil, ErrInvalidSource
	}

	config, err := png.DecodeConfig(bytes.NewReader(source.Data))
	if err != nil {
		return nil, nil, ErrInvalidSource
	}
	if config.Width <= 0 || config.Height <= 0 {
		return nil, nil, ErrInvalidSource
	}
	if int64(config.Width)*int64(config.Height) > maxSourcePixels {
		return nil, nil, ErrTooLarge
	}

	img, err := png.Decode(bytes.NewReader(source.Data))
	if err != nil {
		return nil, nil, ErrInvalidSource
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, nil, ErrInvalidSource
	}
	if display.Width <= 0 || display.Height <= 0 {
		return nil, nil, ErrGeometry
	}
	if width != display.Width || height != display.Height {
		return nil, nil, ErrGeometry
	}

	dstW, dstH := outputSize(width, height, policy.MaxLongSide)
	var out image.Image
	if dstW == width && dstH == height {
		out = img
	} else {
		out = boxResample(img, dstW, dstH)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: policy.Quality}); err != nil {
		return nil, nil, fmt.Errorf("imaging: encode JPEG: %w", err)
	}
	if buf.Len() > policy.MaxBytes {
		return nil, nil, ErrTooLarge
	}

	data := make([]byte, buf.Len())
	copy(data, buf.Bytes())

	meta := &protocol.ImageMeta{
		CapturedAt: source.CapturedAt.UTC().Format(time.RFC3339),
		Width:      dstW,
		Height:     dstH,
		MediaType:  MediaTypeJPEG,
	}
	return meta, data, nil
}

// normalizePolicy applies zero-value defaults and rejects negative values.
func normalizePolicy(policy Policy) (Policy, error) {
	if policy.MaxLongSide < 0 || policy.Quality < 0 || policy.MaxBytes < 0 {
		return Policy{}, errors.New("imaging: policy values must not be negative")
	}
	if policy.MaxLongSide == 0 {
		policy.MaxLongSide = defaultMaxLongSide
	}
	if policy.Quality == 0 {
		policy.Quality = defaultQuality
	}
	if policy.MaxBytes == 0 {
		policy.MaxBytes = defaultMaxBytes
	}
	if policy.Quality < 1 {
		policy.Quality = 1
	}
	if policy.Quality > 100 {
		policy.Quality = 100
	}
	return policy, nil
}

// outputSize returns the proportional destination size. The source is never
// upscaled and both destination edges stay at least one pixel.
func outputSize(width, height, maxLongSide int) (int, int) {
	longSide := max(width, height)
	scale := 1.0
	if longSide > maxLongSide {
		scale = float64(maxLongSide) / float64(longSide)
	}
	dstW := max(1, int(math.Round(float64(width)*scale)))
	dstH := max(1, int(math.Round(float64(height)*scale)))
	if max(dstW, dstH) > maxLongSide {
		if dstW >= dstH {
			dstW = maxLongSide
			dstH = max(1, int(math.Round(float64(height)*float64(maxLongSide)/float64(width))))
		} else {
			dstH = maxLongSide
			dstW = max(1, int(math.Round(float64(width)*float64(maxLongSide)/float64(height))))
		}
	}
	return dstW, dstH
}

// boxResample averages each destination pixel's source rectangle. It is
// deterministic integer arithmetic and allocates only the destination image and
// two small boundary tables per axis.
func boxResample(src image.Image, dstW, dstH int) *image.RGBA {
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	x0 := make([]int, dstW)
	x1 := make([]int, dstW)
	for x := 0; x < dstW; x++ {
		start := bounds.Min.X + int(int64(x)*int64(srcW)/int64(dstW))
		end := bounds.Min.X + int(int64(x+1)*int64(srcW)/int64(dstW))
		if end <= start {
			end = start + 1
		}
		x0[x], x1[x] = start, end
	}
	y0 := make([]int, dstH)
	y1 := make([]int, dstH)
	for y := 0; y < dstH; y++ {
		start := bounds.Min.Y + int(int64(y)*int64(srcH)/int64(dstH))
		end := bounds.Min.Y + int(int64(y+1)*int64(srcH)/int64(dstH))
		if end <= start {
			end = start + 1
		}
		y0[y], y1[y] = start, end
	}

	for y := 0; y < dstH; y++ {
		row := dst.Pix[y*dst.Stride : y*dst.Stride+dstW*4]
		for x := 0; x < dstW; x++ {
			var sumR, sumG, sumB, sumA uint64
			for sy := y0[y]; sy < y1[y]; sy++ {
				for sx := x0[x]; sx < x1[x]; sx++ {
					r, g, b, a := src.At(sx, sy).RGBA()
					sumR += uint64(r)
					sumG += uint64(g)
					sumB += uint64(b)
					sumA += uint64(a)
				}
			}
			count := uint64((x1[x] - x0[x]) * (y1[y] - y0[y]))
			offset := x * 4
			row[offset+0] = uint8((sumR / count) >> 8)
			row[offset+1] = uint8((sumG / count) >> 8)
			row[offset+2] = uint8((sumB / count) >> 8)
			row[offset+3] = uint8((sumA / count) >> 8)
		}
	}
	return dst
}
