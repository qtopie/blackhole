package album

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"runtime"
)

// EnsureThumbnail generates a lightweight 400px JPEG thumbnail for a photo if it doesn't exist yet.
func (m *Manager) EnsureThumbnail(photoPath, photoID string) (string, error) {
	cacheBase := filepath.Dir(m.albumDir)
	if cacheBase == "." {
		cacheBase = m.albumDir
	}
	thumbDir := filepath.Join(cacheBase, ".cache", "photos", "thumbnails")
	_ = os.MkdirAll(thumbDir, 0755)

	thumbPath := filepath.Join(thumbDir, photoID+".jpg")
	photo, photoErr := m.store.GetPhoto(photoID)
	if fi, err := os.Stat(thumbPath); err == nil && fi.Size() > 0 {
		if photoErr == nil && photo != nil && photo.Luminance > 0 {
			return thumbPath, nil
		}
		// Fast path: calculate luminance directly from small 10KB thumbnail file
		if f, err := os.Open(thumbPath); err == nil {
			if img, _, err := image.Decode(f); err == nil {
				avgLum, isDark := CalculateLuminance(img)
				if avgLum <= 0 {
					avgLum = 0.001
				}
				if photo != nil {
					photo.Luminance = avgLum
					photo.IsDark = isDark
					_ = m.store.SavePhoto(photo)
				}
				f.Close()
				return thumbPath, nil
			}
			f.Close()
		}
	}

	file, err := os.Open(photoPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	srcImg, _, err := image.Decode(file)
	if err != nil {
		return "", fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := srcImg.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	targetW := 400
	targetH := 300
	if srcW > 0 && srcH > 0 {
		targetH = int(float64(srcH) * (float64(targetW) / float64(srcW)))
		if targetH <= 0 {
			targetH = 1
		}
	}

	dstImg := scaleBilinear(srcImg, targetW, targetH)
	srcImg = nil
	runtime.GC()
	avgLum, isDark := CalculateLuminance(dstImg)
	if photo, err := m.store.GetPhoto(photoID); err == nil && photo != nil {
		photo.Luminance = avgLum
		photo.IsDark = isDark
		_ = m.store.SavePhoto(photo)
	}

	outFile, err := os.Create(thumbPath)
	if err != nil {
		return "", err
	}
	defer outFile.Close()

	opts := &jpeg.Options{Quality: 70}
	if err := jpeg.Encode(outFile, dstImg, opts); err != nil {
		return "", err
	}

	return thumbPath, nil
}

// Fast zero-dependency bilinear image scaler
func scaleBilinear(src image.Image, targetW, targetH int) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	if srcW == 0 || srcH == 0 {
		return dst
	}

	for y := 0; y < targetH; y++ {
		sy := float64(y) * float64(srcH) / float64(targetH)
		y0 := int(sy)
		y1 := y0 + 1
		if y1 >= srcH {
			y1 = srcH - 1
		}
		dy := sy - float64(y0)

		for x := 0; x < targetW; x++ {
			sx := float64(x) * float64(srcW) / float64(targetW)
			x0 := int(sx)
			x1 := x0 + 1
			if x1 >= srcW {
				x1 = srcW - 1
			}
			dx := sx - float64(x0)

			r00, g00, b00, a00 := src.At(bounds.Min.X+x0, bounds.Min.Y+y0).RGBA()
			r10, g10, b10, a10 := src.At(bounds.Min.X+x1, bounds.Min.Y+y0).RGBA()
			r01, g01, b01, a01 := src.At(bounds.Min.X+x0, bounds.Min.Y+y1).RGBA()
			r11, g11, b11, a11 := src.At(bounds.Min.X+x1, bounds.Min.Y+y1).RGBA()

			r := lerp(lerp(float64(r00), float64(r10), dx), lerp(float64(r01), float64(r11), dx), dy)
			g := lerp(lerp(float64(g00), float64(g10), dx), lerp(float64(g01), float64(g11), dx), dy)
			b := lerp(lerp(float64(b00), float64(b10), dx), lerp(float64(b01), float64(b11), dx), dy)
			a := lerp(lerp(float64(a00), float64(a10), dx), lerp(float64(a01), float64(a11), dx), dy)

			dst.Set(x, y, color.RGBA64{
				R: uint16(r),
				G: uint16(g),
				B: uint16(b),
				A: uint16(a),
			})
		}
	}

	return dst
}

func lerp(v0, v1, t float64) float64 {
	return v0 + t*(v1-v0)
}

func CalculateLuminance(img image.Image) (float64, bool) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return 0, false
	}

	var totalLum float64
	sampleCount := 0

	for y := bounds.Min.Y; y < bounds.Max.Y; y += 4 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 4 {
			r, g, b, _ := img.At(x, y).RGBA()
			r8 := float64(r >> 8)
			g8 := float64(g >> 8)
			b8 := float64(b >> 8)
			lum := 0.299*r8 + 0.587*g8 + 0.114*b8
			totalLum += lum
			sampleCount++
		}
	}

	if sampleCount == 0 {
		return 0, false
	}

	avgLum := totalLum / float64(sampleCount)
	isDark := avgLum <= 75.0
	return avgLum, isDark
}
