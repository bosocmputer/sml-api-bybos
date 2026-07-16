package handlers

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestDecodeSMLSignatureAcceptsRawJPEGAndDataURLPNG(t *testing.T) {
	jpegBytes := encodeTestSignatureJPEG(t, 64, 32)
	raw := base64.StdEncoding.EncodeToString(jpegBytes)
	decoded, err := decodeSMLSignature(raw)
	if err != nil {
		t.Fatalf("decode raw JPEG: %v", err)
	}
	if decoded.ContentType != "image/jpeg" || decoded.Width != 64 || decoded.Height != 32 || decoded.Version == "" {
		t.Fatalf("decoded JPEG = %+v", decoded)
	}

	pngBytes := encodeTestSignaturePNG(t, 48, 24)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	decoded, err = decodeSMLSignature(dataURL)
	if err != nil {
		t.Fatalf("decode PNG data URL: %v", err)
	}
	if decoded.ContentType != "image/png" || decoded.Width != 48 || decoded.Height != 24 {
		t.Fatalf("decoded PNG = %+v", decoded)
	}
}

func TestDecodeSMLSignatureFingerprintUsesDecodedBytes(t *testing.T) {
	data := encodeTestSignaturePNG(t, 32, 16)
	encoded := base64.StdEncoding.EncodeToString(data)
	first, err := decodeSMLSignature(encoded)
	if err != nil {
		t.Fatal(err)
	}
	withWhitespace := strings.Join([]string{encoded[:20], encoded[20:40], encoded[40:]}, "\n")
	second, err := decodeSMLSignature("data:image/png;base64," + withWhitespace)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != second.Version {
		t.Fatalf("versions differ: %s != %s", first.Version, second.Version)
	}
}

func TestDecodeSMLSignatureRejectsMissingInvalidAndOversizedValues(t *testing.T) {
	if _, err := decodeSMLSignature(" "); err != errSMLSignatureMissing {
		t.Fatalf("missing err = %v", err)
	}
	if _, err := decodeSMLSignature(base64.StdEncoding.EncodeToString([]byte("not-an-image"))); err != errSMLSignatureInvalid {
		t.Fatalf("invalid err = %v", err)
	}
	if _, err := decodeSMLSignature(strings.Repeat("A", maxSMLSignatureEncodedBytes+1)); err != errSMLSignatureInvalid {
		t.Fatalf("oversized err = %v", err)
	}
}

func encodeTestSignatureJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.White)
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encodeTestSignaturePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
